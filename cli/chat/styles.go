package chat

import "github.com/charmbracelet/lipgloss"

// Layout constants
const (
	// Padding and margins
	textAreaPaddingLeft      = 1
	confirmPaddingHorizontal = 2
	confirmPaddingVertical   = 1
	confirmMarginTop         = 1
	helpMarginTop            = 1
	messagePaddingLeft       = 2

	// Border adjustments
	inputBorderHeight = 2
	headerHeight      = 2
)

var (
	messageHorizontalFrameSize = aiMessageStyle.GetHorizontalFrameSize()

	// Color palette
	primaryColor   = lipgloss.Color("#7C3AED") // Purple
	secondaryColor = lipgloss.Color("#06B6D4") // Cyan
	accentColor    = lipgloss.Color("#F59E0B") // Amber
	successColor   = lipgloss.Color("#10B981") // Green
	errorColor     = lipgloss.Color("#EF4444") // Red
	mutedColor     = lipgloss.Color("#6B7280") // Gray
	textColor      = lipgloss.Color("#F9FAFB") // Light gray
	dimTextColor   = lipgloss.Color("#9CA3AF") // Dim gray
	messageColor   = lipgloss.Color("#E5E7EB")
	thoughtColor   = lipgloss.Color("#FCD34D")
	fileColor      = lipgloss.Color("#F472B6") // Pink
	borderColor    = lipgloss.Color("#4B5563")
	dividerColor   = lipgloss.Color("#374151")
	codeBgColor    = lipgloss.Color("#374151")

	// Title bar style
	titleStyle = lipgloss.NewStyle().
			Background(primaryColor).
			Foreground(textColor).
			Bold(true)

	// Status indicators in title
	statusStyle = lipgloss.NewStyle().
			Foreground(dimTextColor).
			Background(primaryColor)

	// User message styles
	userLabelStyle = lipgloss.NewStyle().
			Foreground(successColor).
			Bold(true)

	userMessageStyle = lipgloss.NewStyle().
				Foreground(textColor).
				Border(lipgloss.RoundedBorder()).
				BorderForeground(primaryColor).
				Padding(0, 1).
				MarginLeft(10)

	// AI message styles
	aiLabelStyle = lipgloss.NewStyle().
			Foreground(secondaryColor).
			Bold(true)

	aiMessageStyle = lipgloss.NewStyle().
			Foreground(messageColor).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(secondaryColor).
			Padding(0, 1).
			MarginRight(10)

	messageErrorStyle = lipgloss.NewStyle().
				Foreground(errorColor).
				Italic(true).
				PaddingLeft(messagePaddingLeft)

	// Message interrupt style (user cancelled)
	messageInterruptStyle = lipgloss.NewStyle().
				Foreground(accentColor).
				Italic(true).
				PaddingLeft(messagePaddingLeft)

	// Reasoning/thought styles
	thoughtLabelStyle = lipgloss.NewStyle().
				Foreground(accentColor).
				Italic(true)

	thoughtStyle = lipgloss.NewStyle().
			Foreground(thoughtColor).
			Italic(true).
			PaddingLeft(messagePaddingLeft)

	dimTextStyle = lipgloss.NewStyle().
			Foreground(dimTextColor)

	// Tool styles
	toolLabelStyle = lipgloss.NewStyle().
			Foreground(accentColor).
			Bold(true)

	toolCallStyle = lipgloss.NewStyle().
			Foreground(accentColor).
			PaddingLeft(messagePaddingLeft)

	toolResultStyle = lipgloss.NewStyle().
			Foreground(secondaryColor).
			PaddingLeft(messagePaddingLeft)

	// File injection style
	fileStyle = lipgloss.NewStyle().
			Foreground(fileColor).
			Italic(true)

	// System message style
	systemStyle = lipgloss.NewStyle().
			Foreground(mutedColor).
			Italic(true)

	// Error style
	errorStyle = lipgloss.NewStyle().
			Foreground(errorColor).
			Bold(true)

	// Input area styles
	textAreaStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(primaryColor).
			PaddingLeft(textAreaPaddingLeft)

	// Spinner style
	spinnerStyle = lipgloss.NewStyle().
			Foreground(secondaryColor)

	// Help text style
	helpStyle = lipgloss.NewStyle().
			Foreground(mutedColor).
			Italic(true).
			MarginTop(helpMarginTop)

	// Confirmation dialog styles
	confirmBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(accentColor).
			Padding(confirmPaddingVertical, confirmPaddingHorizontal).
			MarginTop(confirmMarginTop)

	confirmTitleStyle = lipgloss.NewStyle().
				Foreground(accentColor).
				Bold(true)

	confirmCommandStyle = lipgloss.NewStyle().
				Foreground(textColor).
				Background(codeBgColor)

	// Viewport border
	viewportStyle = lipgloss.NewStyle().Margin(0).Padding(0)

	// Divider
	dividerStyle = lipgloss.NewStyle().
			Foreground(dividerColor)
)

// Helper function to create a divider
func divider(width int) string {
	return dividerStyle.Render(lipgloss.NewStyle().Width(width).Render("─"))
}
