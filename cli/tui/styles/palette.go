package styles

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/compat"
)

// adaptive picks its shade from the terminal background, resolved lazily at
// render time, so package-level styles stay valid on light and dark themes.
func adaptive(light, dark string) color.Color {
	return compat.AdaptiveColor{Light: lipgloss.Color(light), Dark: lipgloss.Color(dark)}
}

// The palette is semantic: pick a token by meaning, never by hue. Ink carries
// hierarchy (Text > Muted > Subtle); hue is reserved for state and identity.
var (
	// ColorText is primary content: messages, values, the active tab.
	ColorText = adaptive("#1F2328", "#E6E6E6")
	// ColorMuted is secondary content: labels, metadata, hints, thoughts.
	// It stays ≥4.5:1 on white and Dracula, and ≥4:1 on the selection.
	ColorMuted = adaptive("#59636E", "#A0A4AD")
	// ColorSubtle is for non-text only: rules, separators, inactive gutters.
	ColorSubtle = adaptive("#AFB8C1", "#5C6370")

	// ColorBrand is sgpt's single accent: focus, selection, the user's voice.
	ColorBrand = adaptive("#7C3AED", "#A78BFA")
	// ColorInfo marks the assistant and links.
	ColorInfo = adaptive("#0E7490", "#67E8F9")
	// ColorSuccess marks resolved-ok state.
	ColorSuccess = adaptive("#1A7F37", "#4ADE80")
	// ColorWarning marks anything waiting on the user (reviews).
	ColorWarning = adaptive("#9A6700", "#FBBF24")
	// ColorDanger marks failures and destructive actions.
	ColorDanger = adaptive("#CF222E", "#F87171")
	// ColorAgent marks other agents (sub-agents, launchers).
	ColorAgent = adaptive("#4D7C0F", "#A3E635")
	// ColorFile marks injected files.
	ColorFile = adaptive("#BF3989", "#F472B6")
)

// Glyphs form one small, width-1 vocabulary; emoji are avoided in chrome
// because their cell width varies across terminals and breaks alignment.
const (
	GlyphSuccess   = "✓"
	GlyphFailure   = "✗"
	GlyphReview    = "▶"
	GlyphQueued    = "◌"
	GlyphCancelled = "⊘"
	GlyphPrompt    = "›"
	GlyphSelected  = "▌"
	GlyphChild     = "↳"
	GlyphEllipsis  = "…"
	GlyphSeparator = "·"
)

var (
	// TextStyle, MutedStyle and SubtleStyle are the three inks.
	TextStyle   = lipgloss.NewStyle().Foreground(ColorText)
	MutedStyle  = lipgloss.NewStyle().Foreground(ColorMuted)
	SubtleStyle = lipgloss.NewStyle().Foreground(ColorSubtle)

	hintKeyStyle   = lipgloss.NewStyle().Foreground(ColorText)
	hintLabelStyle = lipgloss.NewStyle().Foreground(ColorMuted)
	hintSeparator  = SubtleStyle.Render(" " + GlyphSeparator + " ")
)

// Hint renders one key hint ("ctrl+j send"): the key in text ink, the action
// muted.
func Hint(key, action string) string {
	return hintKeyStyle.Render(key) + " " + hintLabelStyle.Render(action)
}

// Hints joins key hints with a subtle separator.
func Hints(hints ...string) string {
	return strings.Join(hints, hintSeparator)
}
