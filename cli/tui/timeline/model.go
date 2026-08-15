package timeline

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"golang.design/x/clipboard"

	"github.com/malonaz/sgpt/cli/tui/editor"
	"github.com/malonaz/sgpt/cli/tui/keymap"
	"github.com/malonaz/sgpt/cli/tui/styles"
	"github.com/malonaz/sgpt/internal/markdown"
)

var (
	KeyPrevItem       = keymap.New("alt+[", "Previous fence/block")
	KeyNextItem       = keymap.New("alt+]", "Next fence/block")
	KeyToTop          = keymap.New("alt+<", "Jump to top")
	KeyToBottom       = keymap.New("alt+>", "Jump to bottom")
	KeyScrollUp       = keymap.New("ctrl+p", "Scroll up")
	KeyScrollDown     = keymap.New("ctrl+n", "Scroll down")
	KeyToggleCollapse = keymap.New("alt+z", "Collapse/expand item")
	KeyToggleNavMode  = keymap.New("alt+a", "Toggle fence/API-block navigation")
	KeyCopy           = keymap.New("alt+w", "Copy selection to clipboard")
	KeyOpenEditor     = keymap.New("alt+o", "Open selection in $EDITOR")
)

func Keymap() keymap.Map {
	return keymap.Map{
		Name: "Timeline",
		Bindings: []keymap.Binding{
			KeyPrevItem, KeyNextItem, KeyToTop, KeyToBottom,
			KeyScrollUp, KeyScrollDown, KeyToggleCollapse, KeyToggleNavMode,
			KeyCopy, KeyOpenEditor,
		},
	}
}

// NavMode selects navigation granularity.
type NavMode int

const (
	// NavModeFence steps through individual code fences (default).
	NavModeFence NavMode = iota
	// NavModeBlock steps through whole API blocks.
	NavModeBlock
)

// renderEntry caches an item's rendered output as lines, so both measurement
// and the visible-window assembly are map lookups + slicing.
type renderEntry struct {
	lines []string
	// usedGeneration marks the last rerender pass that touched this entry;
	// entries unused for a full pass are evicted once the cache grows past
	// its budget.
	usedGeneration uint64
}

// renderCacheBudget bounds the render cache. Entries are keyed by content
// fingerprint + render state, so a long chat with changing selection would
// otherwise grow it without limit.
const renderCacheBudget = 512

// Model owns items, a cursor (item ID + fence index), collapse state and a
// virtualized scroll window: items are measured once (cached), and View only
// materializes the lines intersecting the viewport.
type Model struct {
	items     []Item
	cursorID  string
	cursorSub int
	navMode   NavMode
	collapsed map[string]bool

	// Virtualized scroll state.
	offsets    []int
	heights    []int
	totalLines int
	yOffset    int

	// renderCache holds rendered lines keyed by content fingerprint + render
	// state (selection/collapse/focus/width).
	renderCache map[string]renderEntry
	// generation increments per rerender pass, driving cache eviction.
	generation uint64
	// volatileLines memoizes non-cacheable (streaming) items for the duration
	// of one rerender pass so measure + View don't render twice.
	volatileLines map[string][]string

	renderer *markdown.Renderer
	width    int
	height   int
	focused  bool
	ready    bool
}

func New() *Model {
	renderer, _ := markdown.NewRenderer(styles.DefaultTextareaWidth)
	return &Model{
		collapsed:     map[string]bool{},
		renderCache:   map[string]renderEntry{},
		volatileLines: map[string][]string{},
		renderer:      renderer,
	}
}

func (m *Model) SetItems(items []Item) {
	m.items = items
	m.rerender()
}

func (m *Model) SetFocused(focused bool) {
	m.focused = focused
	m.rerender()
}

func (m *Model) SetSize(width, height int) {
	// No-op on unchanged dimensions: ChatScreen.refresh() calls this on every
	// stream tick, which previously forced a second full rerender.
	if m.ready && width == m.width && height == m.height {
		return
	}
	if width != m.width {
		// Reserve 2 columns for the permanent gutter.
		m.renderer.SetWidth(width - styles.MessageHorizontalFrameSize() - 2)
		// Wrapping changed — every cached render is stale.
		m.renderCache = map[string]renderEntry{}
		// Volatile (streaming) memos were measured at the old width too.
		m.volatileLines = map[string][]string{}
	}
	m.width = width
	m.height = height
	m.ready = true
	m.rerender()
}

func (m *Model) maxYOffset() int {
	if m.totalLines <= m.height {
		return 0
	}
	return m.totalLines - m.height
}

func (m *Model) setYOffset(y int) {
	if y < 0 {
		y = 0
	}
	if maxY := m.maxYOffset(); y > maxY {
		y = maxY
	}
	m.yOffset = y
}

func (m *Model) AtBottom() bool { return m.yOffset >= m.maxYOffset() }
func (m *Model) GotoBottom()    { m.yOffset = m.maxYOffset() }

func (m *Model) ClearSelection() {
	m.cursorID = ""
	m.cursorSub = 0
	m.rerender()
}

func (m *Model) SelectedItem() Item {
	if index := m.cursorIndex(); index != -1 {
		return m.items[index]
	}
	return nil
}

func (m *Model) SelectLast() {
	if len(m.items) > 0 {
		m.setCursor(len(m.items)-1, m.lastSub(m.items[len(m.items)-1]))
	}
}

// SelectFunc selects (and scrolls to) the first item matching the predicate.
func (m *Model) SelectFunc(match func(Item) bool) bool {
	for i, item := range m.items {
		if match(item) {
			m.setCursor(i, 0)
			return true
		}
	}
	return false
}

// SelectedMessageName returns the chat message behind the selection, or "" when
// nothing selected-and-message-backed is under the cursor. Grouped
// injected-file items resolve to the file the sub-cursor is on, so a delete
// targets exactly what is highlighted.
func (m *Model) SelectedMessageName() string {
	item := m.SelectedItem()
	if item == nil {
		return ""
	}
	if fileItem, ok := item.(*InjectedFileItem); ok && m.navMode == NavModeFence {
		return fileItem.MessageNameAt(m.cursorSub)
	}
	owned, ok := item.(MessageOwned)
	if !ok {
		return ""
	}
	return owned.MessageName()
}

// View assembles only the visible window — O(viewport height), not O(chat).
func (m *Model) View() string {
	if !m.ready {
		return ""
	}
	visible := make([]string, 0, m.height)
	top, bottom := m.yOffset, m.yOffset+m.height
	for i, item := range m.items {
		// Measurement (rerender) and assembly are separate passes; a resize or
		// an item mutated between them leaves offsets/heights describing a
		// different render than itemLines returns now. Clamp instead of
		// trusting the cached geometry.
		if i >= len(m.offsets) || i >= len(m.heights) {
			break
		}
		start := m.offsets[i]
		if start >= bottom {
			break
		}
		if start+m.heights[i] <= top {
			continue
		}
		lines := m.itemLines(item)
		from := min(max(0, top-start), len(lines))
		to := min(len(lines), max(bottom-start, from))
		visible = append(visible, lines[from:to]...)
	}
	// Pad so the input stays pinned at the bottom of the layout.
	for len(visible) < m.height {
		visible = append(visible, "")
	}
	return strings.Join(visible, "\n")
}

func (m *Model) HandleKey(msg tea.KeyPressMsg, alert func(string) tea.Cmd) tea.Cmd {
	switch {
	case key.Matches(msg, KeyPrevItem.Key):
		m.moveCursor(-1)
	case key.Matches(msg, KeyNextItem.Key):
		m.moveCursor(1)
	case key.Matches(msg, KeyToTop.Key):
		if len(m.items) > 0 {
			m.setCursor(0, 0)
		}
	case key.Matches(msg, KeyToBottom.Key):
		m.SelectLast()
	case key.Matches(msg, KeyScrollUp.Key):
		m.setYOffset(m.yOffset - 3)
	case key.Matches(msg, KeyScrollDown.Key):
		m.setYOffset(m.yOffset + 3)
	case key.Matches(msg, KeyToggleCollapse.Key):
		m.toggleCollapse()
	case key.Matches(msg, KeyToggleNavMode.Key):
		if m.navMode == NavModeFence {
			m.navMode = NavModeBlock
		} else {
			m.navMode = NavModeFence
		}
		m.cursorSub = 0
		m.rerender()
		if m.navMode == NavModeBlock {
			return alert("Navigation: API blocks")
		}
		return alert("Navigation: code fences")
	case key.Matches(msg, KeyCopy.Key):
		if content, _ := m.selectedContent(); content != "" {
			clipboard.Write(clipboard.FmtText, []byte(content))
			return alert("Copied to clipboard!")
		}
	case key.Matches(msg, KeyOpenEditor.Key):
		if content, extension := m.selectedContent(); content != "" {
			return editor.Open(content, extension)
		}
	default:
		// Delegate unhandled keys to the selected item (e.g. review actions).
		if interactive, ok := m.SelectedItem().(Interactive); ok {
			return interactive.HandleKey(msg)
		}
	}
	return nil
}

// selectedContent returns the fence content in fence mode, otherwise the
// whole API block's content.
func (m *Model) selectedContent() (string, string) {
	item := m.SelectedItem()
	if item == nil {
		return "", ""
	}
	if m.navMode == NavModeFence {
		if sub, ok := item.(SubNavigable); ok && sub.SubCount() > 0 && m.cursorSub < sub.SubCount() {
			return sub.SubContent(m.cursorSub)
		}
	}
	return item.Content()
}

func (m *Model) IsCollapsed(item Item) bool {
	if value, ok := m.collapsed[item.ID()]; ok {
		return value
	}
	if collapsible, ok := item.(Collapsible); ok {
		return collapsible.DefaultCollapsed()
	}
	return false
}

func (m *Model) cursorIndex() int {
	if m.cursorID == "" {
		return -1
	}
	for i, item := range m.items {
		if item.ID() == m.cursorID {
			return i
		}
	}
	return -1
}

// lastSub is the final fence index of an item under the current nav mode.
func (m *Model) lastSub(item Item) int {
	if m.navMode != NavModeFence {
		return 0
	}
	if sub, ok := item.(SubNavigable); ok && sub.SubCount() > 0 {
		return sub.SubCount() - 1
	}
	return 0
}

func (m *Model) moveCursor(delta int) {
	if len(m.items) == 0 {
		return
	}
	index := m.cursorIndex()
	if index == -1 {
		if delta > 0 {
			return
		}
		m.setCursor(len(m.items)-1, m.lastSub(m.items[len(m.items)-1]))
		return
	}

	// Fence-granular stepping inside the current item.
	if delta > 0 && m.cursorSub < m.lastSub(m.items[index]) {
		m.setCursor(index, m.cursorSub+1)
		return
	}
	if delta < 0 && m.cursorSub > 0 {
		m.setCursor(index, m.cursorSub-1)
		return
	}

	next := index + delta
	if next < 0 {
		return
	}
	if next >= len(m.items) {
		m.GotoBottom()
		return
	}
	if delta > 0 {
		m.setCursor(next, 0)
	} else {
		m.setCursor(next, m.lastSub(m.items[next]))
	}
}

func (m *Model) setCursor(index, sub int) {
	if index < 0 || index >= len(m.items) {
		return
	}
	m.cursorID = m.items[index].ID()
	m.cursorSub = sub
	m.rerender()
	m.scrollToCursor()
}

func (m *Model) toggleCollapse() {
	item := m.SelectedItem()
	if item == nil {
		return
	}
	if _, ok := item.(Collapsible); !ok {
		return
	}
	m.collapsed[item.ID()] = !m.IsCollapsed(item)
	m.cursorSub = 0
	m.rerender()
	m.scrollToCursor()
}

func (m *Model) scrollToCursor() {
	index := m.cursorIndex()
	if index < 0 || index >= len(m.offsets) {
		return
	}
	offset := m.offsets[index]
	if m.navMode == NavModeFence && m.cursorSub > 0 {
		if item, ok := m.items[index].(SubOffsetter); ok {
			subOffsets := item.SubOffsets(m.renderContext(m.items[index]))
			if m.cursorSub < len(subOffsets) {
				offset += subOffsets[m.cursorSub]
			}
		}
	}
	m.setYOffset(offset)
}

func (m *Model) renderContext(item Item) RenderContext {
	selected := m.focused && item.ID() == m.cursorID
	selectedSub := -1
	if selected && m.navMode == NavModeFence {
		if _, ok := item.(SubNavigable); ok {
			selectedSub = m.cursorSub
		}
	}
	return RenderContext{
		Width:       m.width,
		Selected:    selected,
		Focused:     m.focused,
		Collapsed:   m.IsCollapsed(item),
		SelectedSub: selectedSub,
		Renderer:    m.renderer,
	}
}

// itemLines returns the item's rendered lines, from cache when possible.
func (m *Model) itemLines(item Item) []string {
	key := ""
	if cacheable, ok := item.(Cacheable); ok && cacheable.CacheKey() != "" {
		ctx := m.renderContext(item)
		// Render state participates in the key so selection/collapse/focus
		// changes never serve stale output.
		key = fmt.Sprintf("%s|%t|%t|%t|%d",
			cacheable.CacheKey(), ctx.Selected, ctx.Focused, ctx.Collapsed, ctx.SelectedSub)
		if entry, ok := m.renderCache[key]; ok {
			// Touch so the entry survives the next eviction sweep.
			entry.usedGeneration = m.generation
			m.renderCache[key] = entry
			return entry.lines
		}
	} else if lines, ok := m.volatileLines[item.ID()]; ok {
		return lines
	}
	lines := strings.Split(item.Render(m.renderContext(item)), "\n")
	if key != "" {
		m.renderCache[key] = renderEntry{lines: lines, usedGeneration: m.generation}
	} else {
		m.volatileLines[item.ID()] = lines
	}
	return lines
}

// rerender measures item heights and offsets; cached items cost a map lookup,
// so a stream tick only renders the one volatile (streaming) item.
func (m *Model) rerender() {
	if !m.ready {
		return
	}
	m.generation++
	m.volatileLines = map[string][]string{}
	m.offsets = make([]int, len(m.items))
	m.heights = make([]int, len(m.items))
	line := 0
	for i, item := range m.items {
		m.offsets[i] = line
		lines := m.itemLines(item)
		m.heights[i] = len(lines)
		line += len(lines)
	}
	m.totalLines = line
	m.setYOffset(m.yOffset) // re-clamp if content shrank
	m.evictStaleRenders()
}

// evictStaleRenders drops entries not touched by the current pass once the
// cache exceeds its budget: the working set is the visible/measured items, so
// anything older is a superseded render state.
func (m *Model) evictStaleRenders() {
	if len(m.renderCache) <= renderCacheBudget {
		return
	}
	for key, entry := range m.renderCache {
		if entry.usedGeneration != m.generation {
			delete(m.renderCache, key)
		}
	}
}

// RenderItems renders items statically (no cursor, no cache) — used by
// read-only previews such as the menu detail pane.
func RenderItems(items []Item, renderer *markdown.Renderer, width int) string {
	var b strings.Builder
	for i, item := range items {
		if i > 0 {
			b.WriteString("\n")
		}
		ctx := RenderContext{Width: width, Renderer: renderer, Static: true, SelectedSub: -1}
		if collapsible, ok := item.(Collapsible); ok {
			ctx.Collapsed = collapsible.DefaultCollapsed()
		}
		b.WriteString(item.Render(ctx))
	}
	return b.String()
}
