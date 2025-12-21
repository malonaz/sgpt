package markdown

import (
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
)

// Renderer handles markdown rendering with syntax highlighting.
type Renderer struct {
	glamour                    *glamour.TermRenderer
	width                      int
	mdCache                    map[int]string
	blockCache                 map[int]Block
	blockMdCache               map[int]string
	incrementalBlockIndex      int
	incrementalBlockLineOffset int
	incrementalBlockMdCache    string
}

// NewRenderer creates a new markdown renderer.
func NewRenderer(width int) (*Renderer, error) {
	gr, err := glamour.NewTermRenderer(
		glamour.WithStyles(customStyle()),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return nil, err
	}

	return &Renderer{
		glamour:      gr,
		width:        width,
		mdCache:      map[int]string{},
		blockCache:   map[int]Block{},
		blockMdCache: map[int]string{},
	}, nil
}

// ToMarkdown renders markdown content with syntax highlighting.
// The index is used for caching. Use -1 for non-cached rendering.
// Set finalized to true when the content is complete (enables full caching).
func (r *Renderer) ToMarkdown(messageIndex int, finalized bool, blocks ...Block) string {
	// Check cache first for the full content
	if md, ok := r.mdCache[messageIndex]; ok {
		return md
	}

	var sb strings.Builder

	for i, block := range blocks {
		blockIndex := messageIndex*1_000_000_000 + i
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
			md = r.toMarkdownBlock(block.md())
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
		r.mdCache[messageIndex] = result
	}

	return result
}

// SetWidth updates the renderer width, recreating internals if needed.
func (r *Renderer) SetWidth(width int) error {
	if r.width == width {
		return nil
	}
	newRenderer, err := NewRenderer(width)
	if err != nil {
		return err
	}
	*r = *newRenderer
	return nil
}

// toMarkdownBlockIncremental renders markdown content incrementally.
// Complete lines are rendered with full markdown, the current incomplete line is plain text.
func (r *Renderer) toMarkdownBlockIncremental(block Block, blockIndex int) string {
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
		content = b.code
		isCodeBlock = true
		language = b.language
	default:
		return r.incrementalBlockMdCache
	}

	if content == "" {
		return r.incrementalBlockMdCache
	}

	lines := strings.Split(content, "\n")
	numLines := len(lines)

	// Determine how many complete lines we have
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

	if r.incrementalBlockMdCache == "" {
		return latestLine
	}
	return r.incrementalBlockMdCache + latestLine
}

// toMarkdownBlock renders a single block of markdown content.
func (r *Renderer) toMarkdownBlock(content string) string {
	rendered, err := r.glamour.Render(content)
	if err != nil {
		return content
	}
	return strings.Trim(rendered, "\n")
}

// customStyle returns a modified glamour style for cleaner output.
func customStyle() ansi.StyleConfig {
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

	style.Paragraph.BlockPrefix = ""
	style.Paragraph.BlockSuffix = ""

	return style
}
