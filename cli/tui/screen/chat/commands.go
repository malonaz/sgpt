package chat

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/malonaz/core/go/pbutil/pbfieldmask"
	spb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc/status"

	"github.com/malonaz/sgpt/cli/tui/screen"
	chatservicepb "github.com/malonaz/sgpt/genproto/chat/chat_service/v1"
	chatpb "github.com/malonaz/sgpt/genproto/chat/v1"
)

type editorClosedMsg struct{}

func statusToProto(err error) *spb.Status {
	if err == nil {
		return nil
	}
	return status.Convert(err).Proto()
}

func (m *Model) saveChat() tea.Cmd {
	return func() tea.Msg {
		updateChatRequest := &chatservicepb.UpdateChatRequest{
			Chat:       m.chat,
			UpdateMask: pbfieldmask.FromPaths("tags", "files", "metadata").MustValidate(&chatpb.Chat{}).Proto(),
		}
		chat, err := m.chatClient.UpdateChat(m.ctx, updateChatRequest)
		if err != nil {
			return screen.AlertMsg{Text: fmt.Sprintf("Save failed: %v", err)}
		}
		m.chat = chat
		return chatSavedMsg{}
	}
}

func (m *Model) generateSummary() tea.Cmd {
	return func() tea.Msg {
		if err := GenerateChatSummary(m.ctx, m.config, m.aiClient, m.chatClient, m.chat); err != nil {
			m.log.Error("summary generation failed", "error", err)
		}
		return chatSavedMsg{}
	}
}

func (m *Model) forkChat() tea.Cmd {
	return func() tea.Msg {
		return screen.OpenChatMsg{Chat: m.chat, Fork: true}
	}
}

func (m *Model) openInEditor(content, ext string) tea.Cmd {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		editor = "vim"
	}

	tmpFile, err := os.CreateTemp("", "sgpt-message-*."+ext)
	if err != nil {
		return func() tea.Msg {
			return screen.AlertMsg{Text: fmt.Sprintf("Failed to create temp file: %v", err)}
		}
	}
	tmpPath := tmpFile.Name()

	if _, err := tmpFile.WriteString(content); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return func() tea.Msg {
			return screen.AlertMsg{Text: fmt.Sprintf("Failed to write temp file: %v", err)}
		}
	}
	tmpFile.Close()

	parts := strings.Fields(editor)
	if len(parts) == 0 {
		os.Remove(tmpPath)
		return nil
	}

	args := append(parts[1:], tmpPath)
	cmd := exec.Command(parts[0], args...)
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		os.Remove(tmpPath)
		if err != nil {
			return screen.AlertMsg{Text: fmt.Sprintf("Editor failed: %v", err)}
		}
		return editorClosedMsg{}
	})
}
