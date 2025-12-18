package chat

import (
	"regexp"
	"strings"
)

var (
	// Regex to match code blocks with capture groups:
	// Group 1: language (optional)
	// Group 2: code content
	codeBlockRegexp = regexp.MustCompile("(?sm)^```([a-zA-Z]*)\\n(.*?)^```")
)

type Block interface {
	String() string
}

// TextBlock represents plain text content
type TextBlock struct {
	Text string
}

func (b *TextBlock) String() string { return b.Text }

// CodeBlock represents a code block with optional language
type CodeBlock struct {
	Language string
	Code     string
}

func (b *CodeBlock) String() string { return "```" + b.Language + "\n" + b.Code + "\n```" }

// ParseContent parses markdown content into a list of TextBlock and CodeBlock segments
func (r *renderer) ParseBlocks(content string) []Block {
	var result []Block

	matches := codeBlockRegexp.FindAllStringSubmatchIndex(content, -1)
	if len(matches) == 0 {
		if content != "" {
			result = append(result, &TextBlock{Text: content})
		}
		return result
	}

	lastEnd := 0

	for _, match := range matches {
		fullStart, fullEnd := match[0], match[1]
		langStart, langEnd := match[2], match[3]
		codeStart, codeEnd := match[4], match[5]

		// Add plain text before this code block
		if fullStart > lastEnd {
			text := content[lastEnd:fullStart]
			if text != "" {
				result = append(result, &TextBlock{Text: text})
			}
		}

		// Extract language (may be empty if no capture)
		var language string
		if langStart >= 0 && langEnd >= 0 {
			language = content[langStart:langEnd]
		}
		if language == "" {
			language = "text" // Fallback to text.
		}

		// Extract code
		var code string
		if codeStart >= 0 && codeEnd >= 0 {
			code = content[codeStart:codeEnd]
		}

		result = append(result, &CodeBlock{
			Language: language,
			Code:     strings.Trim(code, "\n"),
		})

		lastEnd = fullEnd
	}

	// Add any remaining plain text after the last code block
	if lastEnd < len(content) {
		text := content[lastEnd:]
		if text != "" {
			result = append(result, &TextBlock{Text: text})
		}
	}

	return result
}
