package chat

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/malonaz/core/go/pbutil/pbfieldmask"
	"github.com/malonaz/core/go/uuid"
	spb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc/status"

	"github.com/malonaz/sgpt/cli/tui/screen"
	chatservicepb "github.com/malonaz/sgpt/genproto/chat/chat_service/v1"
	chatpb "github.com/malonaz/sgpt/genproto/chat/v1"
)

type editorClosedMsg struct {
	Modified bool
	Content  string
}

func statusToProto(err error) *spb.Status {
	if err == nil {
		return nil
	}
	return status.Convert(err).Proto()
}

func (m *Model) saveChat() tea.Cmd {
	return func() tea.Msg {
		if m.chat.GetName() == "" {
			createChatRequest := &chatservicepb.CreateChatRequest{
				RequestId: uuid.MustNewV7().String(),
				ChatId:    uuid.MustNewV7().String()[:8],
				Chat:      m.chat,
			}
			chat, err := m.chatClient.CreateChat(m.ctx, createChatRequest)
			if err != nil {
				return screen.AlertMsg{Text: fmt.Sprintf("Failed to create chat: %v", err)}
			}
			m.chat = chat
			if err := GenerateChatSummary(m.ctx, m.config, m.aiClient, m.chatClient, m.chat); err != nil {
				return screen.AlertMsg{Text: fmt.Sprintf("Failed to generate chat summary: %v", err)}
			}
		} else {
			updateChatRequest := &chatservicepb.UpdateChatRequest{
				Chat:       m.chat,
				UpdateMask: pbfieldmask.FromPaths("tags", "files", "metadata").MustValidate(&chatpb.Chat{}).Proto(),
			}
			chat, err := m.chatClient.UpdateChat(m.ctx, updateChatRequest)
			if err != nil {
				return screen.AlertMsg{Text: fmt.Sprintf("Failed to update chat: %v", err)}
			}
			m.chat = chat
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
	// Find the editor.
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		editor = "vim"
	}
	editorArgs := strings.Fields(editor)
	if len(editorArgs) == 0 {
		return func() tea.Msg {
			return screen.AlertMsg{Text: "Failed to find editor"}
		}
	}

	// Create the temporary file.
	tmpFile, err := os.CreateTemp("", "sgpt-*."+ext)
	if err != nil {
		return func() tea.Msg {
			return screen.AlertMsg{Text: fmt.Sprintf("Failed to create temp file: %v", err)}
		}
	}
	tmpPath := tmpFile.Name()

	// If content is set => write to it.
	if content != "" {
		if _, err := tmpFile.WriteString(content); err != nil {
			tmpFile.Close()
			os.Remove(tmpPath)
			return func() tea.Msg {
				return screen.AlertMsg{Text: fmt.Sprintf("Failed to write temp file: %v", err)}
			}
		}
		tmpFile.Close()
	}

	// Capture the info, so we can check if was modified.
	info, err := os.Stat(tmpPath)
	if err != nil {
		os.Remove(tmpPath)
		return func() tea.Msg {
			return screen.AlertMsg{Text: fmt.Sprintf("Failed to stat temp file: %v", err)}
		}
	}
	modTimeBefore := info.ModTime()

	args := append(editorArgs[1:], tmpPath)
	cmd := exec.Command(editorArgs[0], args...)
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		info, err := os.Stat(tmpPath)
		if err != nil {
			return screen.AlertMsg{Text: fmt.Sprintf("Editor failed: %v", err)}
		}
		content, err := os.ReadFile(tmpPath)
		if err != nil {
			return screen.AlertMsg{Text: fmt.Sprintf("Failed to read file: %v", err)}
		}
		os.Remove(tmpPath)
		if err != nil {
			return screen.AlertMsg{Text: fmt.Sprintf("Editor failed: %v", err)}
		}
		return editorClosedMsg{
			Modified: info.ModTime().After(modTimeBefore),
			Content:  string(content),
		}
	})
}
