package chat

import (
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
)

var (
	customStyle = getCustomStyle()
)

// renderer handles markdown rendering with syntax highlighting
type renderer struct {
	glamour                    *glamour.TermRenderer
	width                      int
	mdCache                    map[int]string
	blockCache                 map[int]Block
	blockMdCache               map[int]string
	incrementalBlockIndex      int
	incrementalBlockLineOffset int
	incrementalBlockMdCache    string
}

// newRenderer creates a new markdown renderer
func newRenderer(width int) (*renderer, error) {
	// Create glamour renderer with dark theme
	gr, err := glamour.NewTermRenderer(
		glamour.WithStyles(customStyle),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return nil, err
	}

	return &renderer{
		glamour:      gr,
		width:        width,
		mdCache:      map[int]string{},
		blockCache:   map[int]Block{},
		blockMdCache: map[int]string{},
	}, nil
}

// toMarkdownBlockIncremental renders markdown content incrementally
// Complete lines are rendered with full markdown, the current incomplete line is plain text
func (r *renderer) toMarkdownBlockIncremental(block Block, blockIndex int) string {
	// Reset cache if block changed
	if r.incrementalBlockIndex != blockIndex {
		r.incrementalBlockIndex = blockIndex
		r.incrementalBlockLineOffset = 0
		r.incrementalBlockMdCache = ""
	}

	var content string
	var isCodeBlock bool
	var language string

	switch b := block.(type) {
	case *TextBlock:
		content = b.Text
	case *CodeBlock:
		content = b.Code
		isCodeBlock = true
		language = b.Language
	default:
		return r.incrementalBlockMdCache
	}

	if content == "" {
		return r.incrementalBlockMdCache
	}

	lines := strings.Split(content, "\n")
	numLines := len(lines)

	// Determine how many complete lines we have
	// If content ends with \n, last element is empty string (all others are complete)
	// If content doesn't end with \n, last element is the incomplete line
	var completeLinesCount int
	if numLines > 1 {
		completeLinesCount = numLines - 1
	}

	// Re-render all complete lines when a new line is added
	if completeLinesCount > r.incrementalBlockLineOffset {
		completeContent := strings.Join(lines[:completeLinesCount], "\n")
		if completeContent != "" {
			var toRender string
			if isCodeBlock {
				// Reconstruct code block for proper syntax highlighting
				toRender = "```" + language + "\n" + completeContent + "\n```"
			} else {
				toRender = completeContent
			}

			rendered := r.toMarkdownBlock(toRender)
			r.incrementalBlockMdCache = strings.TrimSuffix(rendered, "\n")
		}
		r.incrementalBlockLineOffset = completeLinesCount
	}

	// Output the latest (potentially incomplete) line as plain text
	latestLine := lines[numLines-1]
	if latestLine == "" {
		return r.incrementalBlockMdCache
	}

	// Add indentation for code blocks to match glamour's rendering
	if isCodeBlock {
		latestLine = latestLine
	}

	// Just append the latest line as plain text (no markdown rendering)
	if r.incrementalBlockMdCache == "" {
		return latestLine
	}
	return r.incrementalBlockMdCache + "\n" + latestLine
}

// toMarkdown renders markdown content with syntax highlighting
func (r *renderer) toMarkdownBlock(content string) string {
	rendered, err := r.glamour.Render(content)
	if err != nil {
		// Fall back to plain text on error
		return content
	}

	// Trim trailing newlines that glamour adds
	result := strings.Trim(rendered, "\n")

	return result
}

// toMarkdown renders only code blocks with syntax highlighting
// while leaving other text as plain text
func (r *renderer) toMarkdown(content string, index int, finalized bool) string {
	// Check cache first for the full content
	if md, ok := r.mdCache[index]; ok {
		return md
	}

	var sb strings.Builder
	blocks := r.ParseBlocks(content)

	for i, block := range blocks {
		blockIndex := index*1_000_000_000 + i
		r.blockCache[blockIndex] = block
		if md, ok := r.blockMdCache[blockIndex]; ok {
			sb.WriteString(md)
			continue
		}

		isLastBlock := i == len(blocks)-1
		incremental := !finalized && isLastBlock

		var md string
		if incremental {
			md = r.toMarkdownBlockIncremental(block, blockIndex)
		} else {
			md = r.toMarkdownBlock(block.String())
			r.blockMdCache[blockIndex] = md
		}
		sb.WriteString(md)

		if i < len(blocks)-1 {
			sb.WriteString("\n")
		}
	}

	// Store in cache
	result := sb.String()
	if finalized {
		r.mdCache[index] = result
	}

	return result
}

// updateWidth updates the renderer width
func (r *renderer) SetWidth(width int) error {
	if r.width == width {
		return nil
	}
	new, err := newRenderer(width)
	if err != nil {
		return err
	}
	*r = *new
	return nil
}

func getCustomStyle() ansi.StyleConfig {
	// Start with dark style and modify
	style := styles.DraculaStyleConfig
	zero := uint(0)
	style.Document.Margin = &zero
	style.CodeBlock.Margin = &zero
	style.CodeBlock.Indent = &zero
	style.CodeBlock.Prefix = ""
	style.CodeBlock.BlockPrefix = ""

	style.Code.Margin = &zero
	style.Code.Indent = &zero
	style.Code.Prefix = ""
	style.Code.Suffix = ""

	// Remove paragraph block prefix/suffix that adds newlines
	style.Paragraph.BlockPrefix = ""
	style.Paragraph.BlockSuffix = ""

	return style
}
