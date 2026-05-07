package widget

import (
	"os"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
)

type EditorClosedMsg struct {
	Content  string
	Modified bool
}

func OpenEditor(content, ext string) tea.Cmd {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		editor = "vim"
	}
	editorArgs := strings.Fields(editor)
	if len(editorArgs) == 0 {
		return nil
	}

	tmpFile, err := os.CreateTemp("", "sgpt-*."+ext)
	if err != nil {
		return nil
	}
	tmpPath := tmpFile.Name()
	if content != "" {
		tmpFile.WriteString(content)
	}
	tmpFile.Close()

	info, _ := os.Stat(tmpPath)
	modTimeBefore := info.ModTime()

	args := append(editorArgs[1:], tmpPath)
	c := exec.Command(editorArgs[0], args...)
	return tea.ExecProcess(c, func(err error) tea.Msg {
		defer os.Remove(tmpPath)
		info, statErr := os.Stat(tmpPath)
		if statErr != nil {
			return EditorClosedMsg{}
		}
		bytes, readErr := os.ReadFile(tmpPath)
		if readErr != nil {
			return EditorClosedMsg{}
		}
		return EditorClosedMsg{
			Modified: info.ModTime().After(modTimeBefore),
			Content:  string(bytes),
		}
	})
}
