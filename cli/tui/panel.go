package tui

import (
	"fmt"
	"slices"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/malonaz/sgpt/cli/tui/keymap"
	"github.com/malonaz/sgpt/cli/tui/screen"
	"github.com/malonaz/sgpt/cli/tui/widget"
	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/review"
	"github.com/malonaz/sgpt/internal/tool"
	"github.com/malonaz/sgpt/internal/tool/agent"
)

const (
	panelWidth = 40
	// panelMinWidth is the terminal width from which the panel docks beside
	// the chat; narrower terminals open it as a modal.
	panelMinWidth = 160
)

var (
	keyTogglePanel = keymap.New("alt+b", "Toggle the agents panel")
	panelKeyUp     = keymap.New("up", "Select the previous agent")
	panelKeyDown   = keymap.New("down", "Select the next agent")
	panelKeyOpen   = keymap.New("enter", "Go to the agent's tab")
	panelKeyAccept = keymap.New("a", "Accept the agent's tool call")
	panelKeyReject = keymap.New("r", "Reject the agent's tool call")
	panelKeyClose  = keymap.New("esc", "Leave the panel")

	panelKeymap = keymap.Map{Name: "Agents panel", Bindings: []keymap.Binding{
		panelKeyUp, panelKeyDown, panelKeyOpen, panelKeyAccept, panelKeyReject, panelKeyClose,
	}}
)

// tabStateToAgentState reads a user chat's tab state in the agents' vocabulary.
var tabStateToAgentState = map[widget.TabState]sgptpb.AgentOperation_State{
	widget.TabReviewing: sgptpb.AgentOperation_STATE_AWAITING_REVIEW,
	widget.TabBusy:      sgptpb.AgentOperation_STATE_RUNNING,
	widget.TabDone:      sgptpb.AgentOperation_STATE_SUCCEEDED,
	widget.TabFailed:    sgptpb.AgentOperation_STATE_FAILED,
}

// agentPanel is the agents panel's UI state; its content is derived on every
// render from the tabs and the agent manager.
type agentPanel struct {
	// hidden is the user dismissing the docked panel.
	hidden bool
	// modal is the overlay open on a narrow terminal.
	modal bool
	// focused routes keys to the docked panel.
	focused bool
	// selected names the selected agent, stable as rows reorder.
	selected string
}

// agentNode is one agent of the tree.
type agentNode struct {
	// name is the manager's; empty for the chat the user opened.
	name  string
	title string
	// agent is nil while its launch is queued.
	agent tool.Agent
	state sgptpb.AgentOperation_State
	depth int
}

func (a *App) panelDocked() bool {
	return a.width >= panelMinWidth && !a.panel.hidden
}

// panelHasKeys reports whether keys go to the panel rather than the chat.
func (a *App) panelHasKeys() bool {
	return a.panel.modal || a.panel.focused && a.panelDocked()
}

func (a *App) contentWidth() int {
	if a.panelDocked() {
		return max(0, a.width-panelWidth)
	}
	return a.width
}

// layout sizes every screen to what the chrome and the panel leave.
func (a *App) layout() {
	for _, t := range a.tabs {
		t.screen.SetSize(a.contentWidth(), a.contentHeight())
	}
}

// togglePanel docks or undocks the panel on wide terminals, and opens or
// closes the modal on narrow ones.
func (a *App) togglePanel() {
	if a.width < panelMinWidth {
		a.panel.modal = !a.panel.modal
		return
	}
	a.panel.hidden, a.panel.focused = !a.panel.hidden, false
	a.layout()
}

// cycleFocusIntoPanel extends the chat's input → timeline cycle with the
// docked panel; it reports whether it took the key.
func (a *App) cycleFocusIntoPanel() bool {
	chatScreen := a.activeChat()
	if !a.panelDocked() || a.panel.focused || chatScreen == nil || !chatScreen.TimelineFocused() {
		return false
	}
	a.panel.focused = true
	return true
}

// handlePanelKey drives the panel; unbound keys are swallowed so they never
// leak into the chat underneath.
func (a *App) handlePanelKey(msg tea.KeyPressMsg) tea.Cmd {
	nodes := a.agentTree()
	selected := a.selectedNode(nodes)
	switch {
	case key.Matches(msg, panelKeyClose.Key):
		a.panel.modal, a.panel.focused = false, false
	case key.Matches(msg, screen.KeyCycleFocus.Key) && !a.panel.modal:
		// The chat's cycle carries on from the timeline to the input.
		a.panel.focused = false
		return a.tabs[a.activeTab].screen.Update(msg)
	case key.Matches(msg, panelKeyUp.Key):
		a.selectNode(nodes, selected-1)
	case key.Matches(msg, panelKeyDown.Key):
		a.selectNode(nodes, selected+1)
	case selected >= 0:
		return a.actOnNode(msg, nodes[selected])
	}
	return nil
}

// actOnNode applies an action key to the selected agent.
func (a *App) actOnNode(msg tea.KeyPressMsg, node agentNode) tea.Cmd {
	switch {
	case key.Matches(msg, panelKeyOpen.Key):
		return a.openNode(node)
	case key.Matches(msg, panelKeyAccept.Key):
		return a.answerReview(node, review.Verdict{Approved: true})
	case key.Matches(msg, panelKeyReject.Key):
		return a.answerReview(node, review.Verdict{})
	}
	return nil
}

// openNode jumps to the agent's tab, handing the keys to its chat.
func (a *App) openNode(node agentNode) tea.Cmd {
	if node.agent == nil {
		return a.showAlert(screen.AlertMsg{Text: fmt.Sprintf("%s has not launched yet", node.title)})
	}
	a.panel.modal, a.panel.focused = false, false
	return a.focusAgent(screen.FocusAgentMsg{Agent: node.agent, Title: node.title})
}

func (a *App) selectNode(nodes []agentNode, index int) {
	if index >= 0 && index < len(nodes) {
		a.panel.selected = nodes[index].name
	}
}

// selectedNode resolves the selection, defaulting to the first agent; -1
// when there are none.
func (a *App) selectedNode(nodes []agentNode) int {
	if len(nodes) == 0 {
		return -1
	}
	return max(0, slices.IndexFunc(nodes, func(node agentNode) bool { return node.name == a.panel.selected }))
}

// answerReview gives the verdict to the agent's oldest tool call awaiting
// review.
func (a *App) answerReview(node agentNode, verdict review.Verdict) tea.Cmd {
	chatScreen := a.chatOf(node.agent)
	var pending []*review.Request
	if chatScreen != nil {
		pending = chatScreen.Session().PendingReviews()
	}
	if len(pending) == 0 {
		return a.showAlert(screen.AlertMsg{Text: fmt.Sprintf("%s has no tool call awaiting review", node.title)})
	}
	toolCallID := pending[0].ToolCall.GetId()
	if verdict.Approved {
		chatScreen.Session().ApproveToolCall(toolCallID)
	} else {
		chatScreen.Session().RejectToolCall(toolCallID, verdict.Reason)
	}
	return nil
}

// agentTree lists the active chat's whole agent tree depth first, from the
// chat the user opened down; nil when the chat is alone.
func (a *App) agentTree() []agentNode {
	chatScreen := a.activeChat()
	if chatScreen == nil {
		return nil
	}
	var root tool.Agent = chatScreen.Session()
	for parent := a.agents.Parent(root); parent != nil; parent = a.agents.Parent(parent) {
		root = parent
	}
	children := a.appendChildren(nil, root, 1)
	if len(children) == 0 {
		return nil
	}
	rootNode := agentNode{agent: root, title: root.Chat().GetTitle()}
	if rootScreen := a.chatOf(root); rootScreen != nil {
		rootNode.title, rootNode.state = rootScreen.Title(), tabStateToAgentState[rootScreen.TabState()]
	}
	return append([]agentNode{rootNode}, children...)
}

// appendChildren appends parent's subtree, those awaiting review first.
func (a *App) appendChildren(nodes []agentNode, parent tool.Agent, depth int) []agentNode {
	children := a.agents.Children(parent)
	slices.SortStableFunc(children, func(x, y agent.Status) int {
		return awaitingReviewRank(x.State) - awaitingReviewRank(y.State)
	})
	for _, child := range children {
		nodes = append(nodes, agentNode{name: child.Name, title: child.Title, agent: child.Agent, state: child.State, depth: depth})
		if child.Agent != nil {
			nodes = a.appendChildren(nodes, child.Agent, depth+1)
		}
	}
	return nodes
}

func awaitingReviewRank(state sgptpb.AgentOperation_State) int {
	if state == sgptpb.AgentOperation_STATE_AWAITING_REVIEW {
		return 0
	}
	return 1
}

// chatOf finds an agent's chat screen; nil once its tab is closed.
func (a *App) chatOf(target tool.Agent) *screen.ChatScreen {
	if index := a.agentTabIndex(target); index >= 0 {
		return a.tabs[index].screen.(*screen.ChatScreen)
	}
	return nil
}

// agentPanelModel derives what the panel shows from the live tree.
func (a *App) agentPanelModel() widget.AgentPanel {
	panel := widget.AgentPanel{
		Focused: a.panelHasKeys(),
		Spinner: a.busySpinner.View(),
		Hints: []string{
			panelKeyOpen.WithShort("open").Hint(), panelKeyAccept.WithShort("accept").Hint(),
			panelKeyReject.WithShort("reject").Hint(),
		},
	}
	nodes := a.agentTree()
	if len(nodes) == 0 {
		if chatScreen := a.activeChat(); chatScreen != nil {
			panel.Info = chatScreen.Session().Info()
		}
		return panel
	}
	panel.Selected = a.selectedNode(nodes)
	for i, node := range nodes {
		row := widget.AgentRow{Title: node.title, State: node.state, Depth: node.depth}
		if chatScreen := a.chatOf(node.agent); chatScreen != nil {
			row.Activity, row.Elapsed = chatScreen.Activity(), chatScreen.TurnElapsed()
			if i == panel.Selected {
				panel.Tail = chatScreen.Session().LatestText()
			}
		}
		panel.Rows = append(panel.Rows, row)
	}
	return panel
}
