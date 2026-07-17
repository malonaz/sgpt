package markdown

import (
	"strings"

	"charm.land/glamour/v2"
	"charm.land/glamour/v2/ansi"
	"charm.land/glamour/v2/styles"
)

// Renderer handles markdown rendering with syntax highlighting.
type Renderer struct {
	glamour      *glamour.TermRenderer
	width        int
	mdCache      map[int]string
	blockCache   map[int]Block
	blockMdCache map[int]string

	// Streaming state for the block currently being streamed. Stable content is
	// rendered once and appended; only the small tail is ever re-rendered, so
	// per-chunk cost is O(new lines) instead of O(block size).
	incrementalBlockIndex    int
	incrementalStableOffset  int
	incrementalStableMdCache string
	incrementalTailLineCount int
	incrementalTailMdCache   string
}

// NewRenderer creates a new markdown renderer.
func NewRenderer(width int) (*Renderer, error) {
	gr, err := glamour.NewTermRenderer(
		glamour.WithStyles(customStyle()),
		glamour.WithWordWrap(width),
		glamour.WithInlineTableLinks(true),
		glamour.WithHiddenLinks(),
	)
	if err != nil {
		return nil, err
	}

	return &Renderer{
		glamour:               gr,
		width:                 width,
		mdCache:               map[int]string{},
		blockCache:            map[int]Block{},
		blockMdCache:          map[int]string{},
		incrementalBlockIndex: -1, // avoid colliding with blockIndex 0
	}, nil
}

// ToMarkdown renders markdown content with syntax highlighting.
// The index is used for caching. Use -1 for non-cached rendering.
// Set finalized to true when the content is complete (enables full caching).
func (r *Renderer) ToMarkdown(messageIndex int, finalized bool, blocks ...Block) string {
	disableCache := messageIndex == -1

	// Check cache first for the full content
	if md, ok := r.mdCache[messageIndex]; ok && !disableCache {
		return md
	}

	var sb strings.Builder

	for i, block := range blocks {
		blockIndex := messageIndex*1_000_000_000 + i
		r.blockCache[blockIndex] = block
		if md, ok := r.blockMdCache[blockIndex]; ok && !disableCache {
			sb.WriteString(md)
			continue
		}

		isLastBlock := i == len(blocks)-1
		// Never use streaming state for uncached (-1) renders: distinct blocks
		// would collide on the same incremental state.
		incremental := !finalized && isLastBlock && !disableCache

		var md string
		if incremental {
			md = r.toMarkdownBlockIncremental(block, blockIndex)
		} else {
			md = r.toMarkdownBlock(block.md())
			if !disableCache {
				r.blockMdCache[blockIndex] = md
			}
		}
		sb.WriteString(md)

		if i < len(blocks)-1 {
			sb.WriteString("\n")
		}
	}

	// Store in cache
	result := sb.String()
	if finalized && !disableCache {
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

// toMarkdownBlockIncremental renders streaming content incrementally,
// delegating to append-only strategies per block type. The finalized pass in
// ToMarkdown re-renders the full block once, correcting any streaming
// artifacts (multi-line tokens, paragraph re-wraps).
func (r *Renderer) toMarkdownBlockIncremental(block Block, blockIndex int) string {
	// Reset state if the block changed, or if content shrank (block was
	// replaced/edited) — otherwise stale offsets would slice out of bounds.
	if r.incrementalBlockIndex != blockIndex || r.blockLineCount(block) < r.incrementalStableOffset {
		r.incrementalBlockIndex = blockIndex
		r.incrementalStableOffset = 0
		r.incrementalStableMdCache = ""
		r.incrementalTailLineCount = 0
		r.incrementalTailMdCache = ""
	}

	switch b := block.(type) {
	case *TextBlock:
		return r.incrementalText(b.Text)
	case *CodeBlock:
		return r.incrementalCode(b.code, b.language)
	default:
		return r.incrementalStableMdCache
	}
}

func (r *Renderer) blockLineCount(block Block) int {
	return strings.Count(block.Content(), "\n") + 1
}

// incrementalCode highlights only newly completed lines and appends them to
// the stable cache, keeping per-chunk cost O(new lines) instead of O(block size).
func (r *Renderer) incrementalCode(code, language string) string {
	if code == "" {
		return r.incrementalStableMdCache
	}
	lines := strings.Split(code, "\n")
	completeLineCount := len(lines) - 1

	if completeLineCount > r.incrementalStableOffset {
		newContent := strings.Join(lines[r.incrementalStableOffset:completeLineCount], "\n")
		rendered := strings.TrimSuffix(r.toMarkdownBlock("```"+language+"\n"+newContent+"\n```"), "\n")
		r.appendStable(rendered)
		r.incrementalStableOffset = completeLineCount
	}

	return r.withPartialLine(lines[len(lines)-1])
}

// incrementalText promotes fully-formed paragraphs (up to the last blank line)
// into the stable cache; only the trailing paragraph is re-rendered, and only
// when a new line completes.
func (r *Renderer) incrementalText(text string) string {
	if text == "" {
		return r.withPartialLine("")
	}
	lines := strings.Split(text, "\n")
	completeLineCount := len(lines) - 1

	// Promote everything up to the last blank line: it is stable markdown
	// context that no future chunk can change.
	stableOffset := r.incrementalStableOffset
	for i := completeLineCount - 1; i >= r.incrementalStableOffset; i-- {
		if strings.TrimSpace(lines[i]) == "" {
			stableOffset = i + 1
			break
		}
	}
	if stableOffset > r.incrementalStableOffset {
		newContent := strings.Join(lines[r.incrementalStableOffset:stableOffset], "\n")
		if strings.TrimSpace(newContent) != "" {
			r.appendStable(strings.TrimSuffix(r.toMarkdownBlock(newContent), "\n"))
		}
		r.incrementalStableOffset = stableOffset
		r.incrementalTailLineCount = 0
		r.incrementalTailMdCache = ""
	}

	// Re-render the trailing paragraph only when a new line completes, not on
	// every mid-line chunk.
	tailLineCount := completeLineCount - r.incrementalStableOffset
	if tailLineCount != r.incrementalTailLineCount {
		tailContent := strings.Join(lines[r.incrementalStableOffset:completeLineCount], "\n")
		if strings.TrimSpace(tailContent) == "" {
			r.incrementalTailMdCache = ""
		} else {
			r.incrementalTailMdCache = strings.TrimSuffix(r.toMarkdownBlock(tailContent), "\n")
		}
		r.incrementalTailLineCount = tailLineCount
	}

	return r.withPartialLine(lines[len(lines)-1])
}

func (r *Renderer) appendStable(rendered string) {
	if r.incrementalStableMdCache != "" {
		r.incrementalStableMdCache += "\n"
	}
	r.incrementalStableMdCache += rendered
}

// withPartialLine assembles stable + tail caches plus the current incomplete
// line as plain wrapped text — glamour is never invoked for mid-line chunks.
func (r *Renderer) withPartialLine(line string) string {
	out := r.incrementalStableMdCache
	if r.incrementalTailMdCache != "" {
		if out != "" {
			out += "\n"
		}
		out += r.incrementalTailMdCache
	}
	if line == "" {
		return out
	}
	// Wrap to renderer width so block indicators appear on each visual line
	line = wrapLine(line, r.width)
	if out == "" {
		return line
	}
	return out + "\n" + line
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

// wrapLine wraps a single line of text to the specified width using word boundaries.
func wrapLine(text string, width int) string {
	if width <= 0 || len(text) <= width {
		return text
	}

	var result strings.Builder
	var lineLen int

	for _, word := range strings.Fields(text) {
		wordLen := len(word)

		if lineLen == 0 {
			result.WriteString(word)
			lineLen = wordLen
		} else if lineLen+1+wordLen <= width {
			result.WriteString(" ")
			result.WriteString(word)
			lineLen += 1 + wordLen
		} else {
			result.WriteString("\n")
			result.WriteString(word)
			lineLen = wordLen
		}
	}

	return result.String()
}
