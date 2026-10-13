package styles

import (
	"charm.land/lipgloss/v2"
)

const (
	DefaultTextareaWidth = 80

	TruncateLength       = 100
	TruncateSuffix       = "..."
	TruncateSuffixLength = 3
)

// Legacy color names, remapped onto the semantic palette (palette.go).
// Deprecated: use the Color* tokens; these are removed once unused.
var (
	PrimaryColor   = ColorBrand
	SecondaryColor = ColorInfo
	AccentColor    = ColorWarning
	SuccessColor   = ColorSuccess
	ErrorColor     = ColorDanger
	MutedColor     = ColorMuted
	TextColor      = ColorText
	DimTextColor   = ColorMuted
	FileColor      = ColorFile
	AgentColor     = ColorAgent
	BorderColor    = ColorSubtle
	DividerColor   = ColorSubtle
	CodeBgColor    = ColorSurface
)

var (
	TitleStyle = lipgloss.NewStyle().
		Background(PrimaryColor).
		Foreground(TextColor).
		Bold(true)
)

var (
	// DimTextStyle is muted ink.
	// Deprecated: use MutedStyle.
	DimTextStyle = lipgloss.NewStyle().
		Foreground(DimTextColor)
)

var (
	ErrorStyle = lipgloss.NewStyle().
		Foreground(ErrorColor).
		Bold(true)
)

var (
	ViewportStyle = lipgloss.NewStyle().Margin(0).Padding(0)
)

func Truncate(s string, maxLen int) string {
	if lipgloss.Width(s) <= maxLen {
		return s
	}
	truncated := ""
	for _, r := range s {
		candidate := truncated + string(r)
		if lipgloss.Width(candidate)+TruncateSuffixLength > maxLen {
			break
		}
		truncated = candidate
	}
	return truncated + TruncateSuffix
}
