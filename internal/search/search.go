// Package search maintains a persistent Bleve index of chat messages,
// powering ranked full-text search over the entire chat history. One
// document per message — ranks better than one blob per chat and enables
// jump-to-message later — keyed "<chatName>#<messageIndex>".
package search

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/analysis/analyzer/keyword"
	"github.com/blevesearch/bleve/v2/mapping"
	"github.com/blevesearch/bleve/v2/search/highlight/highlighter/ansi"
	"github.com/blevesearch/bleve/v2/search/query"
	aipb "github.com/malonaz/core/genproto/ai/v1"
)

// tagsAnnotation mirrors store.TagsAnnotation: the store hooks index writes,
// so importing it from here would be an import cycle.
const tagsAnnotation = "sgpt.com/tags"

const (
	// maxChatDocuments bounds the per-chat document scan used for deletion.
	maxChatDocuments = 10000
	// maxFragmentsPerResult caps highlighted snippets carried per chat.
	maxFragmentsPerResult = 3
	// overfetchFactor compensates for hits being per message: they collapse
	// to per chat below, so more hits than `limit` must be requested.
	overfetchFactor = 4
)

// Index wraps a persistent bleve index of chat messages.
type Index struct {
	index    bleve.Index
	syncPath string
}

// Result is one matching chat, deduped from message-level hits.
type Result struct {
	ChatName string
	Score    float64
	// Fragments are ANSI-highlighted snippets for the detail pane.
	Fragments []string
}

// messageDocument is the indexed representation of one chat message.
type messageDocument struct {
	ChatName   string    `json:"chat_name"`
	Title      string    `json:"title"`
	Tags       []string  `json:"tags"`
	Role       string    `json:"role"`
	Text       string    `json:"text"`
	UpdateTime time.Time `json:"update_time"`
}

// DefaultPath returns the standard index location.
func DefaultPath() (string, error) {
	cacheDirectory, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolving cache directory: %w", err)
	}
	return filepath.Join(cacheDirectory, "sgpt", "search.bleve"), nil
}

// Open opens the index at path, creating it (with the mapping) if absent.
func Open(path string) (*Index, error) {
	index, err := bleve.Open(path)
	if err == bleve.ErrorIndexPathDoesNotExist {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return nil, fmt.Errorf("creating index directory: %w", err)
		}
		index, err = bleve.New(path, buildIndexMapping())
		if err != nil {
			return nil, fmt.Errorf("creating index: %w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("opening index: %w", err)
	}
	return &Index{index: index, syncPath: path + ".sync"}, nil
}

// Close releases the underlying index.
func (i *Index) Close() error {
	return i.index.Close()
}

func buildIndexMapping() mapping.IndexMapping {
	// Keyword fields are matched exactly (deletion scans, `tags:`/`role:`
	// query-string filters); text fields go through the standard analyzer.
	keywordFieldMapping := bleve.NewTextFieldMapping()
	keywordFieldMapping.Analyzer = keyword.Name
	textFieldMapping := bleve.NewTextFieldMapping()
	dateFieldMapping := bleve.NewDateTimeFieldMapping()

	documentMapping := bleve.NewDocumentMapping()
	documentMapping.AddFieldMappingsAt("chat_name", keywordFieldMapping)
	documentMapping.AddFieldMappingsAt("title", textFieldMapping)
	documentMapping.AddFieldMappingsAt("tags", keywordFieldMapping)
	documentMapping.AddFieldMappingsAt("role", keywordFieldMapping)
	documentMapping.AddFieldMappingsAt("text", textFieldMapping)
	documentMapping.AddFieldMappingsAt("update_time", dateFieldMapping)

	indexMapping := bleve.NewIndexMapping()
	indexMapping.DefaultMapping = documentMapping
	return indexMapping
}

// IndexChat (re)indexes every message of a chat in one batch. Existing
// documents are removed first: forks and edits can shrink the message count,
// which would otherwise leave orphaned trailing documents.
func (i *Index) IndexChat(chat *aipb.Chat) error {
	chatName := chat.GetName()
	if chatName == "" {
		return nil
	}
	staleIDs, err := i.chatDocumentIDs(chatName)
	if err != nil {
		return err
	}
	batch := i.index.NewBatch()
	for _, id := range staleIDs {
		batch.Delete(id)
	}

	var tags []string
	if raw := chat.GetAnnotations()[tagsAnnotation]; raw != "" {
		tags = strings.Split(raw, ",")
	}
	updateTime := chat.GetUpdateTime().AsTime()
	for messageIndex, message := range chat.GetMetadata().GetMessages() {
		text := messageText(message)
		if text == "" {
			continue
		}
		document := &messageDocument{
			ChatName:   chatName,
			Title:      chat.GetTitle(),
			Tags:       tags,
			Role:       roleString(message.GetRole()),
			Text:       text,
			UpdateTime: updateTime,
		}
		if err := batch.Index(fmt.Sprintf("%s#%d", chatName, messageIndex), document); err != nil {
			return err
		}
	}
	return i.index.Batch(batch)
}

// DeleteChat removes every document belonging to a chat.
func (i *Index) DeleteChat(chatName string) error {
	ids, err := i.chatDocumentIDs(chatName)
	if err != nil {
		return err
	}
	batch := i.index.NewBatch()
	for _, id := range ids {
		batch.Delete(id)
	}
	return i.index.Batch(batch)
}

// chatDocumentIDs lists document IDs of a chat via an exact chat_name match.
func (i *Index) chatDocumentIDs(chatName string) ([]string, error) {
	termQuery := bleve.NewTermQuery(chatName)
	termQuery.SetField("chat_name")
	searchRequest := bleve.NewSearchRequestOptions(termQuery, maxChatDocuments, 0, false)
	searchResult, err := i.index.Search(searchRequest)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(searchResult.Hits))
	for _, hit := range searchResult.Hits {
		ids = append(ids, hit.ID)
	}
	return ids, nil
}

// Search returns up to limit chats ranked by their best-matching message.
func (i *Index) Search(queryString string, limit int) ([]Result, error) {
	queryString = strings.TrimSpace(queryString)
	if queryString == "" || limit <= 0 {
		return nil, nil
	}
	// Query-string syntax gives power users field filters (`tags:owner/repo`,
	// `role:user`, phrases); it is OR'd with fuzzy and prefix matches so
	// plain terms survive typos and partially typed words.
	stringQuery := bleve.NewQueryStringQuery(queryString)
	textMatchQuery := bleve.NewMatchQuery(queryString)
	textMatchQuery.SetField("text")
	textMatchQuery.SetFuzziness(1)
	titleMatchQuery := bleve.NewMatchQuery(queryString)
	titleMatchQuery.SetField("title")
	titleMatchQuery.SetBoost(2)
	textPrefixQuery := bleve.NewPrefixQuery(strings.ToLower(queryString))
	textPrefixQuery.SetField("text")

	results, err := i.search(bleve.NewDisjunctionQuery(stringQuery, textMatchQuery, titleMatchQuery, textPrefixQuery), limit)
	if err != nil {
		// Query-string parse errors (unbalanced quotes, bad field syntax)
		// only surface at search time; retry with the plain-term queries.
		return i.search(bleve.NewDisjunctionQuery(textMatchQuery, titleMatchQuery, textPrefixQuery), limit)
	}
	return results, nil
}

func (i *Index) search(searchQuery query.Query, limit int) ([]Result, error) {
	searchRequest := bleve.NewSearchRequestOptions(searchQuery, limit*overfetchFactor, 0, false)
	searchRequest.Fields = []string{"chat_name"}
	searchRequest.Highlight = bleve.NewHighlightWithStyle(ansi.Name)
	searchRequest.Highlight.AddField("text")

	searchResult, err := i.index.Search(searchRequest)
	if err != nil {
		return nil, err
	}

	// Dedupe message-level hits by chat, keeping the best score (hits arrive
	// score-descending) — the menu lists chats, not messages.
	chatNameToResultIndex := map[string]int{}
	var results []Result
	for _, hit := range searchResult.Hits {
		chatName, _ := hit.Fields["chat_name"].(string)
		if chatName == "" {
			continue
		}
		var fragments []string
		for _, fieldFragments := range hit.Fragments {
			fragments = append(fragments, fieldFragments...)
		}
		resultIndex, ok := chatNameToResultIndex[chatName]
		if !ok {
			if len(results) >= limit {
				continue
			}
			chatNameToResultIndex[chatName] = len(results)
			results = append(results, Result{ChatName: chatName, Score: hit.Score, Fragments: fragments})
			continue
		}
		results[resultIndex].Fragments = append(results[resultIndex].Fragments, fragments...)
	}
	for resultIndex := range results {
		if len(results[resultIndex].Fragments) > maxFragmentsPerResult {
			results[resultIndex].Fragments = results[resultIndex].Fragments[:maxFragmentsPerResult]
		}
	}
	return results, nil
}

// messageText joins a message's text blocks; thoughts and tool payloads are
// deliberately excluded — they pollute ranking with low-signal noise.
func messageText(message *aipb.Message) string {
	var parts []string
	for _, block := range message.GetBlocks() {
		if text := block.GetText(); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func roleString(role aipb.Role) string {
	switch role {
	case aipb.Role_ROLE_USER:
		return "user"
	case aipb.Role_ROLE_ASSISTANT:
		return "assistant"
	case aipb.Role_ROLE_SYSTEM:
		return "system"
	case aipb.Role_ROLE_TOOL:
		return "tool"
	default:
		return "unknown"
	}
}

// LastSyncTime returns the persisted backfill watermark; zero on first run
// (which triggers a full backfill).
func (i *Index) LastSyncTime() time.Time {
	bytes, err := os.ReadFile(i.syncPath)
	if err != nil {
		return time.Time{}
	}
	watermark, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(bytes)))
	if err != nil {
		return time.Time{}
	}
	return watermark
}

// SetLastSyncTime persists the backfill watermark.
func (i *Index) SetLastSyncTime(watermark time.Time) error {
	if watermark.IsZero() {
		return nil
	}
	return os.WriteFile(i.syncPath, []byte(watermark.Format(time.RFC3339Nano)), 0644)
}
