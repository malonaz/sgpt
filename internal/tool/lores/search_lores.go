// Package lores implements the search_lores tool: grep-style search over
// the selected repos' lore libraries (.sgpt/lores), the agent-curated
// knowledge base shared from repo to repo via imports.
package lores

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	aipb "github.com/malonaz/core/genproto/ai/v1"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/lore"
	"github.com/malonaz/sgpt/internal/store"
	"github.com/malonaz/sgpt/internal/tool"
)

// SearchLores is the tool definition, built from ToolService.SearchLores.
var SearchLores = tool.MustBuildTool("search_lores", tool.HandlerIDSearchLores, "sgpt.v1.ToolService.SearchLores")

func parseSearchLoresArguments(toolCall *aipb.ToolCall) (*sgptpb.SearchLoresRequest, error) {
	searchLoresRequest := &sgptpb.SearchLoresRequest{}
	if err := tool.UnmarshalArguments(toolCall, searchLoresRequest); err != nil {
		return nil, err
	}
	if searchLoresRequest.GetQuery() == "" {
		return nil, fmt.Errorf("no query specified")
	}
	return searchLoresRequest, nil
}

// Tool searches the selected lore libraries. Stateless: what the model has
// already seen is derived from the session's message history at each call,
// never tracked — the history is the source of truth, and the same tool
// instance serves every session (main chat, sub-agents, tabs).
type Tool struct {
	// Index over the reachable libraries: the enclosing repo's and/or each
	// "@{import}" repo's.
	Index *lore.Index
}

// returnedNameSet derives, from the session's message history, the canonical
// names of every lore already in the model's context: lore files injected
// directly (e.g. the configured default lores) and matches returned by
// earlier searches. A lore is worth returning once — after that it is in
// the context, and repeating it buries the hits that are not.
func (t *Tool) returnedNameSet(messages []*aipb.Message) map[string]bool {
	returned := map[string]bool{}
	for _, message := range messages {
		// Lore files injected as plain context files.
		if path := store.InjectedFilePath(message); path != "" && message.GetDeleteTime() == nil {
			if name, ok := t.Index.NameForPath(path); ok {
				returned[name] = true
			}
			continue
		}
		for _, block := range message.GetBlocks() {
			// Results live on tool-result blocks in committed history, and
			// on the tool calls themselves mid-turn.
			for _, toolResult := range []*aipb.ToolResult{block.GetToolResult(), block.GetToolCall().GetResult()} {
				if toolResult.GetToolName() != SearchLores.GetName() {
					continue
				}
				searchLoresResponse := &sgptpb.SearchLoresResponse{}
				if err := tool.UnmarshalResult(toolResult, searchLoresResponse); err != nil {
					continue
				}
				for _, match := range searchLoresResponse.GetMatches() {
					returned[match.GetName()] = true
				}
			}
		}
	}
	return returned
}

func (t *Tool) Review(_ context.Context, toolCall *aipb.ToolCall) (*sgptpb.ToolCallMetadata, error) {
	if _, err := parseSearchLoresArguments(toolCall); err != nil {
		return nil, err
	}
	// Auto-execution is declared on the proto method (NO_SIDE_EFFECTS).
	return &sgptpb.ToolCallMetadata{
		DisplayMessage: &sgptpb.DisplayMessage{},
		AutoExecute:    tool.NoSideEffects(toolCall),
	}, nil
}

func (t *Tool) Execute(ctx context.Context, toolCall *aipb.ToolCall) (*aipb.ToolResult, error) {
	searchLoresRequest, err := parseSearchLoresArguments(toolCall)
	if err != nil {
		return nil, err
	}
	var lores []*sgptpb.Lore
	for _, library := range t.Index.Libraries() {
		libraryLores, err := library.Load()
		if err != nil {
			return nil, err
		}
		// Searching is the natural sync point: the library is only ever
		// written between searches, so the label vocabulary stays fresh.
		// Imported repos are read-only: only the enclosing repo's labels
		// file is rewritten.
		if library.Prefix == "" {
			if err := library.SyncLabels(libraryLores); err != nil {
				return nil, err
			}
		}
		lores = append(lores, libraryLores...)
	}
	matches, err := lore.Search(lores, searchLoresRequest.GetQuery(), int(searchLoresRequest.GetTopN()))
	if err != nil {
		return nil, err
	}
	returnedNameSet := t.returnedNameSet(tool.History(ctx))
	searchLoresResponse := &sgptpb.SearchLoresResponse{}
	for _, match := range matches {
		if returnedNameSet[match.Lore.GetName()] {
			continue
		}
		searchLoresResponse.Matches = append(searchLoresResponse.Matches, &sgptpb.SearchLoresResponse_Match{
			Name:        match.Lore.GetName(),
			Title:       match.Lore.GetTitle(),
			Description: match.Lore.GetDescription(),
			Labels:      match.Lore.GetLabels(),
			Snippets:    match.Snippets,
			MatchCount:  int32(match.MatchCount),
		})
	}
	return tool.NewStructuredToolResult(toolCall, searchLoresResponse)
}

// Summarize quotes the query and counts the new lores it surfaced.
func (t *Tool) Summarize(toolCall *aipb.ToolCall, toolResult *aipb.ToolResult) (tool.Summary, bool) {
	summary := tool.Summary{Verb: "Search lores", Subject: tool.Quote(tool.Argument(toolCall, "query").GetStringValue())}
	if toolResult != nil {
		if matches := len(tool.ResultField(toolResult, "matches").GetListValue().GetValues()); matches > 0 {
			summary.Outcome = tool.Count(matches, "match", "matches")
		} else {
			summary.Outcome = "no matches"
		}
	}
	return summary, true
}

var (
	_ tool.Tool           = (*Tool)(nil)
	_ tool.Summarizer     = (*Tool)(nil)
	_ tool.ResultRenderer = (*Tool)(nil)
)

func init() { tool.RegisterBuiltin(SearchLores) }

// RenderResult renders each match as its title and name over its snippets,
// with the matched text highlighted.
func (t *Tool) RenderResult(toolCall *aipb.ToolCall, toolResult *aipb.ToolResult) (string, bool) {
	searchLoresResponse := &sgptpb.SearchLoresResponse{}
	if err := tool.UnmarshalResult(toolResult, searchLoresResponse); err != nil {
		return "", false
	}
	if len(searchLoresResponse.GetMatches()) == 0 {
		return "No new lores match.", true
	}
	// Re-derive the pattern to highlight the matched text in snippets; on a
	// bad query we still render, just without highlights.
	pattern, _ := regexp.Compile("(?i)" + tool.Argument(toolCall, "query").GetStringValue())
	sections := make([]string, 0, len(searchLoresResponse.GetMatches()))
	for _, match := range searchLoresResponse.GetMatches() {
		var b strings.Builder
		fmt.Fprintf(&b, "**%s** `%s`", match.GetTitle(), match.GetName())
		if match.GetMatchCount() > 0 {
			b.WriteString(" · " + tool.Count(int(match.GetMatchCount()), "hit", "hits"))
		}
		// One item per snippet: a quote would join the lines into one.
		for _, snippet := range match.GetSnippets() {
			b.WriteString("\n- " + highlight(defuse(snippet), pattern))
		}
		sections = append(sections, b.String())
	}
	return strings.Join(sections, "\n\n"), true
}

// fencePattern matches code-fence openers within a snippet line.
var fencePattern = regexp.MustCompile("`{3,}")

// blockMarkerPattern matches a leading markdown block marker: heading,
// quote, list item.
var blockMarkerPattern = regexp.MustCompile(`^(#|>|[-*+]\s)`)

// defuse keeps a snippet line plain text inside its list item: a lore's own ```
// would open a fenced block mid-render and swallow the rest of the result,
// and a leading "#", "-" or indent would make a heading, a list or a code
// block. A zero-width space between backticks is invisible but stops fence
// parsing.
func defuse(snippet string) string {
	snippet = fencePattern.ReplaceAllStringFunc(strings.TrimLeft(snippet, " \t"), func(fence string) string {
		return strings.Join(strings.Split(fence, ""), "\u200b")
	})
	if blockMarkerPattern.MatchString(snippet) {
		snippet = `\` + snippet
	}
	return snippet
}

// highlight bolds every pattern match within a snippet line.
func highlight(snippet string, pattern *regexp.Regexp) string {
	if pattern == nil {
		return snippet
	}
	return pattern.ReplaceAllStringFunc(snippet, func(matched string) string {
		return "**" + matched + "**"
	})
}
