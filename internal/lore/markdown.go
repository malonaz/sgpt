package lore

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
)

// frontMatterDelimiter opens and closes the YAML front matter block.
const frontMatterDelimiter = "---"

// frontMatter is the metadata header of a markdown lore. The body below it
// is the lore's content, kept as plain markdown so diffs stay readable —
// the reason markdown is preferred over a JSON payload.
type frontMatter struct {
	Title       string            `yaml:"title"`
	Description string            `yaml:"description,omitempty"`
	Labels      map[string]string `yaml:"labels,omitempty"`
}

// splitFrontMatter separates the YAML front matter from the markdown body.
// Front matter is optional: a lore with no header is all body.
func splitFrontMatter(data string) (string, string, bool) {
	// Tolerate a leading BOM/blank lines before the opening delimiter.
	trimmed := strings.TrimLeft(data, "\ufeff\r\n\t ")
	if !strings.HasPrefix(trimmed, frontMatterDelimiter) {
		return "", data, false
	}
	rest := trimmed[len(frontMatterDelimiter):]
	// The opening delimiter must own its line, else "---" is a horizontal rule.
	index := strings.IndexByte(rest, '\n')
	if index < 0 || strings.TrimSpace(rest[:index]) != "" {
		return "", data, false
	}
	rest = rest[index+1:]
	for offset := 0; offset < len(rest); {
		lineEnd := strings.IndexByte(rest[offset:], '\n')
		line := rest[offset:]
		next := len(rest)
		if lineEnd >= 0 {
			line = rest[offset : offset+lineEnd]
			next = offset + lineEnd + 1
		}
		if strings.TrimSpace(line) == frontMatterDelimiter {
			return rest[:offset], rest[next:], true
		}
		offset = next
	}
	// Unterminated front matter: treat the whole file as body.
	return "", data, false
}

// UnmarshalMarkdown parses a markdown lore: optional YAML front matter for
// the metadata, the remaining markdown as the content.
func UnmarshalMarkdown(data []byte) (*sgptpb.Lore, error) {
	header, body, hasFrontMatter := splitFrontMatter(string(data))
	lore := &sgptpb.Lore{}
	if hasFrontMatter {
		parsedFrontMatter := &frontMatter{}
		if err := yaml.Unmarshal([]byte(header), parsedFrontMatter); err != nil {
			return nil, fmt.Errorf("parsing front matter: %w", err)
		}
		lore.Title = parsedFrontMatter.Title
		lore.Description = parsedFrontMatter.Description
		lore.Labels = parsedFrontMatter.Labels
	}
	lore.Content = strings.TrimLeft(body, "\n")
	return lore, nil
}

// MarshalMarkdown renders a lore as markdown with YAML front matter. The
// resource name is omitted: it is derived from the file's location.
func MarshalMarkdown(lore *sgptpb.Lore) ([]byte, error) {
	header, err := yaml.Marshal(&frontMatter{
		Title:       lore.GetTitle(),
		Description: lore.GetDescription(),
		Labels:      lore.GetLabels(),
	})
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	b.WriteString(frontMatterDelimiter + "\n")
	b.Write(header)
	b.WriteString(frontMatterDelimiter + "\n\n")
	b.WriteString(strings.TrimRight(lore.GetContent(), "\n") + "\n")
	return []byte(b.String()), nil
}
