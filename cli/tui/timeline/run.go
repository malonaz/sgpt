package timeline

import (
	"fmt"
	"sort"
	"strings"

	"github.com/malonaz/sgpt/cli/tui/styles"
)

// foldThreshold: resolved runs longer than this fold into one summary row.
const foldThreshold = 3

// ToolRunItem stands for a resolved run of tool calls (and the folded
// thoughts between them) as one summary row. Expanded, the model lays its
// children out below it; the run itself still renders only its row.
type ToolRunItem struct {
	id       string
	children []Item
	calls    []*ToolCallItem
}

func (r *ToolRunItem) ID() string { return r.id }

func (r *ToolRunItem) DefaultCollapsed() bool { return true }

func (r *ToolRunItem) Compact(RenderContext) bool { return true }

// CacheKey: a run only ever grows at its tail, and resolved calls are fixed.
func (r *ToolRunItem) CacheKey() string { return fmt.Sprintf("%s|%d", r.id, len(r.children)) }

func (r *ToolRunItem) Content() (string, string) {
	parts := make([]string, 0, len(r.calls))
	for _, call := range r.calls {
		content, _ := call.Content()
		parts = append(parts, content)
	}
	return strings.Join(parts, "\n\n"), "md"
}

// Render: `✓ 12 tool calls · Read 6 · Shell 4 · Edit 2        1 failed`.
func (r *ToolRunItem) Render(ctx RenderContext) string {
	type verbCount struct {
		verb  string
		count int
	}
	var counts []verbCount
	index := map[string]int{}
	failed := 0
	for _, call := range r.calls {
		summary := call.summary()
		if call.failed(summary) {
			failed++
		}
		if position, ok := index[summary.Verb]; ok {
			counts[position].count++
			continue
		}
		index[summary.Verb] = len(counts)
		counts = append(counts, verbCount{verb: summary.Verb, count: 1})
	}
	// Most frequent first; ties keep first appearance.
	sort.SliceStable(counts, func(a, b int) bool { return counts[a].count > counts[b].count })

	glyph, outcome := successStyle.Render(styles.GlyphSuccess), ""
	if failed > 0 {
		glyph, outcome = dangerStyle.Render(styles.GlyphFailure), fmt.Sprintf("%d failed", failed)
	}
	width := ctx.rowWidth()
	label := fmt.Sprintf("%d tool calls", len(r.calls))
	parts := make([]string, 0, len(counts))
	for _, count := range counts {
		parts = append(parts, fmt.Sprintf("%s %d", count.verb, count.count))
	}
	room := width - markerWidth - len(label) - 3 - len(outcome) - 2
	detail := truncate(strings.Join(parts, " "+styles.GlyphSeparator+" "), room)
	row := gutter(ctx) + glyph + " " + styles.TextStyle.Bold(true).Render(label)
	used := markerWidth + len(label)
	if detail != "" {
		row += styles.SubtleStyle.Render(" "+styles.GlyphSeparator+" ") + styles.MutedStyle.Render(detail)
		used += 3 + len([]rune(detail))
	}
	if outcome != "" {
		row += strings.Repeat(" ", max(width-used-len(outcome), 2)) + dangerStyle.Render(outcome)
	}
	return row
}

// foldRuns replaces each resolved run of more than foldThreshold calls with a
// ToolRunItem, laying its children out after it when expanded. owner maps a
// folded child to its run so a cursor inside it can land on the run.
func foldRuns(items []Item, collapsed func(Item) bool) (folded []Item, owner map[string]string) {
	folded = make([]Item, 0, len(items))
	owner = map[string]string{}
	for start := 0; start < len(items); {
		end, lastCall, calls := start, -1, 0
		for ; end < len(items) && foldable(items[end]); end++ {
			if _, ok := items[end].(*ToolCallItem); ok {
				lastCall, calls = end, calls+1
			}
		}
		// Trailing thoughts lead into what follows, not into the run; a run
		// still at the tail of the list may grow, so it stays open.
		if calls <= foldThreshold || end == len(items) || !allResolved(items[start:lastCall+1]) {
			if end == start {
				end++
			}
			folded = append(folded, items[start:end]...)
			start = end
			continue
		}
		children := items[start : lastCall+1]
		run := &ToolRunItem{id: "run-" + children[0].ID(), children: children}
		for _, child := range children {
			if call, ok := child.(*ToolCallItem); ok {
				run.calls = append(run.calls, call)
			}
			owner[child.ID()] = run.id
		}
		folded = append(folded, run)
		if !collapsed(run) {
			folded = append(folded, children...)
		}
		folded = append(folded, items[lastCall+1:end]...)
		start = end
	}
	return folded, owner
}

func foldable(item Item) bool {
	switch item.(type) {
	case *ToolCallItem, *ThoughtItem:
		return true
	}
	return false
}

func allResolved(items []Item) bool {
	for _, item := range items {
		switch item := item.(type) {
		case *ToolCallItem:
			if !item.resolved() {
				return false
			}
		case *ThoughtItem:
			if !item.finalized {
				return false
			}
		}
	}
	return true
}
