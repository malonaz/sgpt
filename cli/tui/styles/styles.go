package styles

import (
	"charm.land/lipgloss/v2"
)

const (
	DefaultTextareaWidth = 80

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
)

// Legacy color names, remapped onto the semantic palette (palette.go).
// Deprecated: use the Color* tokens; these are removed once unused.
var (
	PrimaryColor           = ColorBrand
	SecondaryColor         = ColorInfo
	AccentColor            = ColorWarning
	SuccessColor           = ColorSuccess
	ErrorColor             = ColorDanger
	MutedColor             = ColorMuted
	TextColor              = ColorText
	DimTextColor           = ColorMuted
	MessageColor           = ColorText
	ThoughtColor           = ColorMuted
	FileColor              = ColorFile
	AgentColor             = ColorAgent
	BorderColor            = ColorSubtle
	DividerColor           = ColorSubtle
	CodeBgColor            = ColorSurface
	MessageSelectedColor   = ColorBrand
	MessageUnselectedColor = ColorSubtle
	BlockIndicatorColor    = ColorSubtle
)

var (
	TitleStyle = lipgloss.NewStyle().
		Background(PrimaryColor).
		Foreground(TextColor).
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

	// AgentMessageStyle frames messages other agents sent into this chat.
	AgentMessageStyle = UserMessageStyle.BorderForeground(AgentColor)

	AIThoughtStyle = lipgloss.NewStyle().
			Inherit(AIMessageStyle).
			BorderForeground(ThoughtColor)

	ToolCallStyle = lipgloss.NewStyle()

	ToolResultStyle = lipgloss.NewStyle()

	UserLabelStyle = lipgloss.NewStyle().
			Foreground(SuccessColor).
			Bold(true)

	AgentLabelStyle = lipgloss.NewStyle().
			Foreground(AgentColor).
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
