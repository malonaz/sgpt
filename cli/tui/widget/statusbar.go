package widget

import (
	"fmt"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/malonaz/sgpt/cli/tui/styles"
)

// ToastLevel grades a toast: its color and glyph.
type ToastLevel int

const (
	ToastInfo ToastLevel = iota
	ToastSuccess
	ToastError
)

// Toast is a transient message shown in place of the status hints.
type Toast struct {
	Text  string
	Level ToastLevel
}

var (
	toastStyles = map[ToastLevel]lipgloss.Style{
		ToastInfo:    styles.TextStyle,
		ToastSuccess: lipgloss.NewStyle().Foreground(styles.ColorSuccess),
		ToastError:   lipgloss.NewStyle().Foreground(styles.ColorDanger),
	}
	toastGlyphs = map[ToastLevel]string{
		ToastSuccess: styles.GlyphSuccess + " ",
		ToastError:   styles.GlyphFailure + " ",
	}
	statusFactSeparator = styles.SubtleStyle.Render(" " + styles.GlyphSeparator + " ")
	statusBelowStyle    = styles.TextStyle
)

const (
	statusPadding = 1
	statusGap     = 3
)

// Status is what the bottom bar shows; each screen supplies its own.
type Status struct {
	// State is the rendered turn state, always shown.
	State string
	// Below counts the timeline lines hidden under the viewport.
	Below int
	// Hints are rendered key hints, most useful first.
	Hints []string
	// Facts are the right side's session facts, in display order.
	Facts []Fact
}

// Fact is one rendered session fact; the lowest Priority drops first.
type Fact struct {
	Text     string
	Priority int
}

// RenderStatusBar renders the status on one line of width. A toast replaces
// the hints. Hints and facts are dropped from the least important end, in
// turns, until the line fits.
func RenderStatusBar(status Status, toast *Toast, width int) string {
	hints, facts := status.Hints, slices.Clone(status.Facts)
	if toast != nil {
		hints = []string{renderToast(*toast)}
	}
	for {
		left, right := statusLeft(status, hints), joinFacts(facts)
		inner := width - 2*statusPadding
		used := lipgloss.Width(left) + lipgloss.Width(right)
		if right != "" {
			used += statusGap
		}
		switch {
		case used <= inner:
			gap := inner - lipgloss.Width(left) - lipgloss.Width(right)
			return pad(left + strings.Repeat(" ", gap) + right)
		case toast == nil && len(hints) > 1:
			hints = hints[:len(hints)-1]
		case len(facts) > 1:
			facts = dropLeastFact(facts)
		case toast == nil && len(hints) > 0:
			hints = nil
		case len(facts) > 0:
			facts = nil
		default:
			// Only the state and a toast remain: the toast is cut.
			return pad(ansi.Truncate(left, max(0, inner), styles.GlyphEllipsis))
		}
	}
}

func joinFacts(facts []Fact) string {
	texts := make([]string, len(facts))
	for i, fact := range facts {
		texts[i] = fact.Text
	}
	return strings.Join(texts, statusFactSeparator)
}

func dropLeastFact(facts []Fact) []Fact {
	least := 0
	for i, fact := range facts {
		if fact.Priority < facts[least].Priority {
			least = i
		}
	}
	return slices.Delete(facts, least, least+1)
}

func statusLeft(status Status, hints []string) string {
	var parts []string
	if status.State != "" {
		parts = append(parts, status.State)
	}
	if status.Below > 0 {
		parts = append(parts, statusBelowStyle.Render(fmt.Sprintf("↓ %d more", status.Below)))
	}
	if len(hints) > 0 {
		parts = append(parts, styles.Hints(hints...))
	}
	return strings.Join(parts, "   ")
}

// renderToast keeps a toast on one line: only its first line shows.
func renderToast(toast Toast) string {
	text, _, _ := strings.Cut(strings.TrimSpace(toast.Text), "\n")
	return toastStyles[toast.Level].Render(toastGlyphs[toast.Level] + text)
}

func pad(line string) string {
	padding := strings.Repeat(" ", statusPadding)
	return padding + line + padding
}
