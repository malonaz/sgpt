package styles

import (
	"charm.land/lipgloss/v2"
)

const (
	MinTextareaHeight    = 3
	MaxTextareaHeight    = 20
	DefaultTextareaWidth = 80
	TextAreaPaddingLeft  = 1

	MinViewportHeight = 1

	TruncateLength       = 100
	TruncateSuffix       = "..."
	TruncateSuffixLength = 3

	TabBarHeight = 1
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

	TabActiveColor   = ColorBrand
	TabInactiveColor = ColorSurface
	TabTextColor     = ColorText
	TabDimTextColor  = ColorMuted
)

var (
	TitleStyle = lipgloss.NewStyle().
			Background(PrimaryColor).
			Foreground(TextColor).
			Bold(true)

	StatusStyle = lipgloss.NewStyle().
			Foreground(DimTextColor).
			Background(PrimaryColor)
)

var (
	TabBarStyle = lipgloss.NewStyle().
			Background(ColorSurface)

	TabActiveStyle = lipgloss.NewStyle().
			Background(TabActiveColor).
			Foreground(TabTextColor).
			Bold(true).
			Padding(0, 1)

	TabInactiveStyle = lipgloss.NewStyle().
				Background(TabInactiveColor).
				Foreground(TabDimTextColor).
				Padding(0, 1)

	TabStreamingIndicator = lipgloss.NewStyle().
				Foreground(AccentColor).
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
	TextAreaStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(PrimaryColor).
		PaddingLeft(TextAreaPaddingLeft)
)

var (
	SpinnerStyle = lipgloss.NewStyle().
		Foreground(SecondaryColor)
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
