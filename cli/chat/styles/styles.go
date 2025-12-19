package styles

import (
	"github.com/charmbracelet/lipgloss"
)

// Layout constants
const (
	// Textarea
	MinTextareaHeight    = 3
	MaxTextareaHeight    = 20
	DefaultTextareaWidth = 80
	TextAreaPaddingLeft  = 1

	// Viewport
	MinViewportHeight = 1

	// Layout
	InputBorderHeight  = 2
	HeaderHeight       = 2
	MessagePaddingLeft = 2

	// Confirmation dialog
	ConfirmPaddingHorizontal = 2
	ConfirmPaddingVertical   = 1
	ConfirmMarginTop         = 1

	// Help
	HelpMarginTop = 1

	// Truncation
	TruncateLength       = 100
	TruncateSuffix       = "..."
	TruncateSuffixLength = 3
)

// Color palette
var (
	PrimaryColor           = lipgloss.Color("#7C3AED") // Purple
	SecondaryColor         = lipgloss.Color("#06B6D4") // Cyan
	AccentColor            = lipgloss.Color("#F59E0B") // Amber
	SuccessColor           = lipgloss.Color("#10B981") // Green
	ErrorColor             = lipgloss.Color("#EF4444") // Red
	MutedColor             = lipgloss.Color("#6B7280") // Gray
	TextColor              = lipgloss.Color("#F9FAFB") // Light gray
	DimTextColor           = lipgloss.Color("#9CA3AF") // Dim gray
	MessageColor           = lipgloss.Color("#E5E7EB")
	ThoughtColor           = lipgloss.Color("#FCD34D")
	FileColor              = lipgloss.Color("#F472B6") // Pink
	BorderColor            = lipgloss.Color("#4B5563")
	DividerColor           = lipgloss.Color("#374151")
	CodeBgColor            = lipgloss.Color("#374151")
	MessageSelectedColor   = lipgloss.Color("#10B981")
	MessageUnselectedColor = lipgloss.Color("#9CA3AF") // Dim gray
)

// Title bar
var (
	TitleStyle = lipgloss.NewStyle().
			Background(PrimaryColor).
			Foreground(TextColor).
			Bold(true)

	StatusStyle = lipgloss.NewStyle().
			Foreground(DimTextColor).
			Background(PrimaryColor)
)

// Messages.
var (
	messageStyle = lipgloss.NewStyle().
			Foreground(TextColor).
			Padding(0, 1).
			Border(lipgloss.RoundedBorder())

	UserMessageStyle = lipgloss.NewStyle().
				Inherit(messageStyle).
				BorderForeground(PrimaryColor).
				MarginLeft(10)

	AIMessageStyle = lipgloss.NewStyle().
			Inherit(messageStyle).
			BorderForeground(SecondaryColor).
			MarginRight(10)

	AIThoughtStyle = lipgloss.NewStyle().
			Inherit(AIMessageStyle).BorderForeground(ThoughtColor)

	// User message
	UserLabelStyle = lipgloss.NewStyle().
			Foreground(SuccessColor).
			Bold(true)

	// Ai message
	AILabelStyle = lipgloss.NewStyle().
			Foreground(SecondaryColor).
			Bold(true)

	MessageErrorStyle = lipgloss.NewStyle().
				Foreground(ErrorColor).
				Italic(true).
				PaddingLeft(MessagePaddingLeft)

	MessageInterruptStyle = lipgloss.NewStyle().
				Foreground(AccentColor).
				Italic(true).
				PaddingLeft(MessagePaddingLeft)
)

// Reasoning/thought
var (
	ThoughtLabelStyle = lipgloss.NewStyle().
				Foreground(AccentColor).
				Italic(true)

	ThoughtStyle = lipgloss.NewStyle().
			Foreground(ThoughtColor).
			Italic(true).
			PaddingLeft(MessagePaddingLeft)

	DimTextStyle = lipgloss.NewStyle().
			Foreground(DimTextColor)
)

// Tools
var (
	ToolLabelStyle = lipgloss.NewStyle().
			Foreground(AccentColor).
			Bold(true)

	ToolCallStyle = lipgloss.NewStyle().
			Foreground(AccentColor).
			PaddingLeft(MessagePaddingLeft)

	ToolResultStyle = lipgloss.NewStyle().
			Foreground(SecondaryColor).
			PaddingLeft(MessagePaddingLeft)
)

// File injection
var (
	FileStyle = lipgloss.NewStyle().
		Foreground(FileColor).
		Italic(true)
)

// System message
var (
	SystemStyle = lipgloss.NewStyle().
		Foreground(MutedColor).
		Italic(true)
)

// Error
var (
	ErrorStyle = lipgloss.NewStyle().
		Foreground(ErrorColor).
		Bold(true)
)

// Input area
var (
	TextAreaStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(PrimaryColor).
		PaddingLeft(TextAreaPaddingLeft)
)

// Spinner
var (
	SpinnerStyle = lipgloss.NewStyle().
		Foreground(SecondaryColor)
)

// Help text
var (
	HelpStyle = lipgloss.NewStyle().
		Foreground(MutedColor).
		Italic(true).
		MarginTop(HelpMarginTop)
)

// Confirmation dialog
var (
	ConfirmBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(AccentColor).
			Padding(ConfirmPaddingVertical, ConfirmPaddingHorizontal).
			MarginTop(ConfirmMarginTop)

	ConfirmTitleStyle = lipgloss.NewStyle().
				Foreground(AccentColor).
				Bold(true)

	ConfirmCommandStyle = lipgloss.NewStyle().
				Foreground(TextColor).
				Background(CodeBgColor)
)

// Viewport
var (
	ViewportStyle = lipgloss.NewStyle().Margin(0).Padding(0)
)

// Divider
var (
	DividerStyle = lipgloss.NewStyle().
		Foreground(DividerColor)
)

// MessageHorizontalFrameSize returns the horizontal frame size of AI messages.
func MessageHorizontalFrameSize() int {
	return AIMessageStyle.GetHorizontalFrameSize()
}

// Divider creates a horizontal divider of the specified width.
func Divider(width int) string {
	return DividerStyle.Render(lipgloss.NewStyle().Width(width).Render("─"))
}

// Truncate truncates a string to the specified length with a suffix.
func Truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-TruncateSuffixLength] + TruncateSuffix
}
