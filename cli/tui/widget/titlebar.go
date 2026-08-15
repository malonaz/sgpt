package widget

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	aipb "github.com/malonaz/core/genproto/ai/v1"

	"github.com/malonaz/sgpt/cli/tui/styles"
	"github.com/malonaz/sgpt/internal/session"
)

type TitleBar struct {
	width int
	title string
	// rendered is the memoized view; height derives from it so layout is
	// correct before the first View() call.
	rendered string
	height   int
}

func NewTitleBar() *TitleBar {
	return &TitleBar{}
}

func (t *TitleBar) SetWidth(width int) {
	if width == t.width {
		return
	}
	t.width = width
	t.render()
}

func (t *TitleBar) Height() int {
	return t.height
}

// render memoizes the styled title and its height; called on every width or
// title change so Height() is always valid — recalculateLayout needs it
// before the first View().
func (t *TitleBar) render() {
	t.rendered = styles.TitleStyle.Width(t.width).Render(t.title)
	t.height = lipgloss.Height(t.rendered)
}

// Refresh rebuilds the title string from session state.
// price is the sum of the chat's server-priced messages.
func (t *TitleBar) Refresh(params session.Params, totalUsage, lastUsage *aipb.ModelUsage, price float64) {
	roleName := "anon"
	if params.Role != nil {
		roleName = params.Role.Name
	}

	reasoningStr := "none"
	switch params.ReasoningEffort {
	case aipb.ReasoningEffort_REASONING_EFFORT_LOW:
		reasoningStr = "low"
	case aipb.ReasoningEffort_REASONING_EFFORT_MEDIUM:
		reasoningStr = "medium"
	case aipb.ReasoningEffort_REASONING_EFFORT_HIGH:
		reasoningStr = "high"
	}

	toolsStr := strings.Join(params.Tools, " + ")
	if toolsStr != "" {
		toolsStr = " | 🔧 " + toolsStr
	}

	totalInputTokens := totalUsage.GetInputToken().GetQuantity() + totalUsage.GetInputTokenCacheRead().GetQuantity()
	totalOutputTokens := totalUsage.GetOutputToken().GetQuantity() + totalUsage.GetOutputReasoningToken().GetQuantity()
	tokenStr := fmt.Sprintf("↑%s ↓%s $%.4f", formatTokenCount(totalInputTokens), formatTokenCount(totalOutputTokens), price)

	contextStr := ""
	if contextLimit := params.Model.GetTtt().GetContextTokenLimit(); contextLimit > 0 {
		lastInputTokens := lastUsage.GetInputToken().GetQuantity() + lastUsage.GetInputTokenCacheRead().GetQuantity()
		usagePercent := float64(lastInputTokens) / float64(contextLimit) * 100
		contextStr = fmt.Sprintf(" │ 📦 %.0f%% (%s/%s)", usagePercent, formatTokenCount(lastInputTokens), formatTokenCount(contextLimit))
	}

	modelResourceName := &aipb.ModelResourceName{}
	modelResourceName.UnmarshalString(params.Model.Name)
	modelStr := fmt.Sprintf("%s/%s", modelResourceName.Provider, modelResourceName.Model)

	t.title = fmt.Sprintf(
		" 🤖 %s │ 👤 %s │ 🧠 %s │ 📊 %s%s%s ",
		modelStr, roleName, reasoningStr, tokenStr, contextStr, toolsStr,
	)
	t.render()
}

func (t *TitleBar) View() string {
	return t.rendered
}

func formatTokenCount(count int32) string {
	if count < 1000 {
		return fmt.Sprintf("%d", count)
	}
	if count < 1000000 {
		return fmt.Sprintf("%.1fk", float64(count)/1000)
	}
	return fmt.Sprintf("%.1fm", float64(count)/1000000)
}
