package timeline

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/malonaz/sgpt/cli/tui/format"
	"github.com/malonaz/sgpt/cli/tui/styles"
)

// WorkItem stands for a finished turn's work (tool calls, thoughts and notes
// between the user's message and the answer) as one row. Expanded, the model
// lays its children out below it, nested; the row itself never grows.
type WorkItem struct {
	id       string
	children []Item
	calls    []*ToolCallItem
	elapsed  time.Duration
}

func (w *WorkItem) ID() string { return w.id }

func (w *WorkItem) DefaultCollapsed() bool { return true }

func (w *WorkItem) Compact(RenderContext) bool { return true }

// CacheKey: a finished turn's work is fixed; the count guards a re-fold.
func (w *WorkItem) CacheKey() string {
	return fmt.Sprintf("%s|%d|%d", w.id, len(w.children), w.elapsed)
}

func (w *WorkItem) Content() (string, string) {
	parts := make([]string, 0, len(w.children))
	for _, child := range w.children {
		content, _ := child.Content()
		parts = append(parts, content)
	}
	return strings.Join(parts, "\n\n"), "md"
}

// Render: `✓ Worked 2m 13s · 13 tool calls · Shell 6 · Read 4 · Edit 3 · 1 failed`.
func (w *WorkItem) Render(ctx RenderContext) string {
	type verbCount struct {
		verb  string
		count int
	}
	var counts []verbCount
	index := map[string]int{}
	failed := 0
	for _, call := range w.calls {
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

	// A failed step the turn recovered from isn't a failed turn: the count says it.
	glyph := markerStyle(ctx, successStyle).Render(styles.GlyphSuccess)
	label := "Worked"
	if w.elapsed >= time.Second {
		label += " " + format.Duration(w.elapsed)
	}
	// Counted in tool calls, so the per-verb counts add up to the total.
	parts := []string{plural(len(w.calls), "tool call")}
	if len(w.calls) == 0 {
		parts = []string{plural(len(w.children), "step")}
	}
	for _, count := range counts {
		parts = append(parts, fmt.Sprintf("%s %d", count.verb, count.count))
	}
	outcome := ""
	if failed > 0 {
		outcome = fmt.Sprintf("%d failed", failed)
	}
	room := ctx.rowWidth() - markerWidth - len(label) - 3
	if outcome != "" {
		room -= len(outcome) + 3
	}
	row := gutter(ctx) + glyph + " " + styles.TextStyle.Bold(true).Render(label)
	if detail := truncate(strings.Join(parts, " "+styles.GlyphSeparator+" "), room); detail != "" {
		row += separator + styles.MutedStyle.Render(detail)
	}
	if outcome != "" {
		row += separator + dangerStyle.Render(outcome)
	}
	return row
}

// foldTurns collapses each finished turn's work into a WorkItem, laying its
// children out after it when expanded. The last turn stays open while live.
// owner maps a folded child to its row, so a cursor inside lands on it;
// nested marks children shown under an expanded row.
func foldTurns(items []Item, live bool, collapsed func(Item) bool) (folded []Item, owner map[string]string, nested map[string]bool) {
	folded = make([]Item, 0, len(items))
	owner, nested = map[string]string{}, map[string]bool{}
	// A live turn's work stays in sight: nothing after its prompt folds.
	open := len(items)
	if live {
		open = 0
		for index, item := range items {
			if isTurn(item) {
				open = index
			}
		}
	}
	for start := 0; start < len(items); {
		if !isWork(items[start]) {
			folded = append(folded, items[start])
			start++
			continue
		}
		end := start
		for end < len(items) && isWork(items[end]) {
			end++
		}
		// The trailing assistant text is the answer: it stays visible.
		answer := end
		for answer > start && isAnswer(items[answer-1]) {
			answer--
		}
		work := items[start:answer]
		if end > open || len(work) < 2 {
			folded = append(folded, items[start:end]...)
			start = end
			continue
		}
		row := &WorkItem{id: "work-" + work[0].ID(), children: work, elapsed: elapsed(items, start, end)}
		for _, child := range work {
			if call, ok := child.(*ToolCallItem); ok {
				row.calls = append(row.calls, call)
			}
			owner[child.ID()] = row.id
		}
		folded = append(folded, row)
		if !collapsed(row) {
			for _, child := range work {
				nested[child.ID()] = true
			}
			folded = append(folded, work...)
		}
		folded = append(folded, items[answer:end]...)
		start = end
	}
	return folded, owner, nested
}

// isWork: what a turn does between the user's message and its answer. A call
// awaiting review is never work: it must stay in sight.
func isWork(item Item) bool {
	switch item := item.(type) {
	case *ToolCallItem:
		return !item.Pending
	case *ThoughtItem:
		return true
	case *TextItem:
		return item.voice == voiceAssistant
	}
	return false
}

func isAnswer(item Item) bool {
	text, ok := item.(*TextItem)
	return ok && text.voice == voiceAssistant
}

// elapsed spans from the turn's user message to its last message; zero when
// either timestamp is unknown.
func elapsed(items []Item, start, end int) time.Duration {
	last, ok := items[end-1].(*TextItem)
	if !ok || last.created.IsZero() {
		return 0
	}
	for index := start - 1; index >= 0; index-- {
		if user, ok := items[index].(*TextItem); ok && user.voice != voiceAssistant {
			if user.created.IsZero() {
				return 0
			}
			return last.created.Sub(user.created)
		}
	}
	return 0
}
