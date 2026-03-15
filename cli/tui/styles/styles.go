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

	MessagePaddingLeft = 2

	BlockIndicatorChar  = "┃"
	BlockIndicatorWidth = 2

	ConfirmPaddingHorizontal = 2
	ConfirmPaddingVertical   = 1
	ConfirmMarginTop         = 1

	HelpMarginTop = 1

	TruncateLength       = 100
	TruncateSuffix       = "..."
	TruncateSuffixLength = 3

	TabBarHeight = 1
)

var (
	PrimaryColor           = lipgloss.Color("#7C3AED")
	SecondaryColor         = lipgloss.Color("#06B6D4")
	AccentColor            = lipgloss.Color("#F59E0B")
	SuccessColor           = lipgloss.Color("#10B981")
	ErrorColor             = lipgloss.Color("#EF4444")
	MutedColor             = lipgloss.Color("#6B7280")
	TextColor              = lipgloss.Color("#F9FAFB")
	DimTextColor           = lipgloss.Color("#9CA3AF")
	MessageColor           = lipgloss.Color("#E5E7EB")
	ThoughtColor           = lipgloss.Color("#FCD34D")
	FileColor              = lipgloss.Color("#F472B6")
	BorderColor            = lipgloss.Color("#4B5563")
	DividerColor           = lipgloss.Color("#374151")
	CodeBgColor            = lipgloss.Color("#374151")
	MessageSelectedColor   = PrimaryColor
	MessageUnselectedColor = lipgloss.Color("#9CA3AF")
	BlockIndicatorColor    = lipgloss.Color("#4B5563")

	TabActiveColor   = PrimaryColor
	TabInactiveColor = lipgloss.Color("#374151")
	TabTextColor     = TextColor
	TabDimTextColor  = DimTextColor
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
			Background(lipgloss.Color("#1F2937"))

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
			Inherit(AIMessageStyle).
			BorderForeground(ThoughtColor)

	UserLabelStyle = lipgloss.NewStyle().
			Foreground(SuccessColor).
			Bold(true)

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

var (
	BlockIndicatorStyle = lipgloss.NewStyle().
				Foreground(BlockIndicatorColor)

	BlockIndicatorSelectedStyle = lipgloss.NewStyle().
					Foreground(MessageSelectedColor)
)

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

var (
	FileStyle = lipgloss.NewStyle().
		Foreground(FileColor).
		Italic(true)
)

var (
	SystemStyle = lipgloss.NewStyle().
		Foreground(MutedColor).
		Italic(true)
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
	HelpStyle = lipgloss.NewStyle().
		Foreground(MutedColor).
		Italic(true).
		MarginTop(HelpMarginTop)
)

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

var (
	ViewportStyle = lipgloss.NewStyle().Margin(0).Padding(0)
)

var (
	DividerStyle = lipgloss.NewStyle().
		Foreground(DividerColor)
)

var (
	MenuSelectedStyle = lipgloss.NewStyle().
				Background(PrimaryColor).
				Foreground(TextColor).
				Bold(true).
				Padding(0, 1)

	MenuItemStyle = lipgloss.NewStyle().
			Foreground(TextColor).
			Padding(0, 1)

	MenuHeaderStyle = lipgloss.NewStyle().
			Foreground(DimTextColor).
			Bold(true).
			Padding(0, 1).
			BorderBottom(true).
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(DividerColor)

	MenuDimStyle = lipgloss.NewStyle().
			Foreground(DimTextColor)

	MenuTitleStyle = lipgloss.NewStyle().
			Foreground(SecondaryColor)

	MenuTagStyle = lipgloss.NewStyle().
			Foreground(AccentColor).
			Italic(true)
)

var (
	SearchInputStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(SecondaryColor).
				PaddingLeft(1)

	SearchPromptStyle = lipgloss.NewStyle().
				Foreground(SecondaryColor).
				Bold(true)
)

func MessageHorizontalFrameSize() int {
	return AIMessageStyle.GetHorizontalFrameSize()
}

func Divider(width int) string {
	return DividerStyle.Render(lipgloss.NewStyle().Width(width).Render("─"))
}

func Truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-TruncateSuffixLength] + TruncateSuffix
}
