package widget

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	aipb "github.com/malonaz/core/genproto/ai/v1"

	cliservice "github.com/malonaz/sgpt/cli/cli_service"
	"github.com/malonaz/sgpt/cli/tui/styles"
	"github.com/malonaz/sgpt/internal/toolengine"
)

type TitleBar struct {
	width  int
	title  string
	height int
}

func NewTitleBar() *TitleBar {
	return &TitleBar{}
}

func (t *TitleBar) SetWidth(width int) {
	t.width = width
}

func (t *TitleBar) Height() int {
	return t.height
}

// Refresh rebuilds the title string from session state.
func (t *TitleBar) Refresh(params cliservice.SessionParams, totalUsage, lastUsage *aipb.ModelUsage) {
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
	totalPrice := totalUsage.GetInputToken().GetPrice() +
		totalUsage.GetOutputToken().GetPrice() +
		totalUsage.GetOutputReasoningToken().GetPrice() +
		totalUsage.GetInputTokenCacheRead().GetPrice() +
		totalUsage.GetInputTokenCacheWrite().GetPrice()

	tokenStr := fmt.Sprintf("↑%s ↓%s $%.4f", formatTokenCount(totalInputTokens), formatTokenCount(totalOutputTokens), totalPrice)

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
}

func (t *TitleBar) View() string {
	rendered := styles.TitleStyle.Width(t.width).Render(t.title)
	t.height = lipgloss.Height(rendered)
	return rendered
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

// Ensure toolengine import is used for the interface check.
var _ = (*toolengine.Manager)(nil)
