package markdown

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

// Block represents a parsed content block.
type Block interface {
	md() string
	Content() string
	Extension() string
}

// TextBlock represents plain text content.
type TextBlock struct {
	Text string
}

// String returns the text content.
func (b *TextBlock) md() string { return b.Text }

// String returns the text content.
func (b *TextBlock) Content() string { return b.Text }

// Extension returns the file extension of a text block.
func (b *TextBlock) Extension() string { return "txt" }

// CodeBlock represents a code block with optional language.
type CodeBlock struct {
	language string
	code     string
}

// String returns the code block as markdown.
func (b *CodeBlock) md() string {
	return "```" + b.language + "\n" + b.code + "\n```"
}

// String returns the code block.
func (b *CodeBlock) Content() string {
	return b.code
}

// Extension returns the file extension of a code block.
func (b *CodeBlock) Extension() string {
	return b.language
}

// ParseBlocks parses markdown content into a list of TextBlock and CodeBlock segments.
func ParseBlocks(content string) []Block {
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
			language = "md" // Fallback to text.
		}

		// Extract code
		var code string
		if codeStart >= 0 && codeEnd >= 0 {
			code = content[codeStart:codeEnd]
		}

		result = append(result, &CodeBlock{
			language: language,
			code:     strings.ReplaceAll(strings.Trim(code, "\n"), "\t", "  "), // tabs cause issues.
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
