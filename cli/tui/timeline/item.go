package timeline

import (
	tea "charm.land/bubbletea/v2"

	"github.com/malonaz/sgpt/internal/markdown"
)

// RenderContext carries per-render state so items themselves stay stateless;
// selection and collapse state live in the Model, keyed by item ID.
type RenderContext struct {
	Width     int
	Selected  bool
	Focused   bool
	Collapsed bool
	// SelectedSub is the selected fence index within the item while in fence
	// navigation mode; -1 means no fence-level selection.
	SelectedSub int
	// Static disables markdown render-caching — used for read-only previews
	// (menu detail pane) where cache keys would collide across chats.
	Static   bool
	Renderer *markdown.Renderer
}

// Item is a single navigable unit in the timeline (an API block).
type Item interface {
	ID() string
	Render(ctx RenderContext) string
	// Content returns raw text and a file extension, for copy / open-in-editor.
	Content() (string, string)
}

// Cacheable items expose a stable fingerprint of their content; the timeline
// caches their fully rendered output keyed by fingerprint + render state.
// Return "" to opt out (content still mutating, e.g. streaming).
type Cacheable interface {
	Item
	CacheKey() string
}

// Collapsible items can fold to a one-line summary.
type Collapsible interface {
	Item
	DefaultCollapsed() bool
}

// SubNavigable items contain sub-blocks (code fences) navigable individually.
type SubNavigable interface {
	Item
	SubCount() int
	SubContent(index int) (string, string)
}

// SubOffsetter exposes line offsets of sub-blocks (relative to the item's
// first line) so the viewport can scroll to the selected fence.
type SubOffsetter interface {
	SubNavigable
	SubOffsets(ctx RenderContext) []int
}

// Interactive items receive unhandled keys while selected.
type Interactive interface {
	Item
	HandleKey(msg tea.KeyPressMsg) tea.Cmd
}

// MessageOwned items know the chat message they were built from, so the UI can
// act on the underlying resource (e.g. delete it). Returns "" for items with
// no persisted message behind them (stream errors, optimistic context).
type MessageOwned interface {
	Item
	MessageName() string
}
