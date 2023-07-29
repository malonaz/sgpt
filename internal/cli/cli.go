package cli

import (
	"fmt"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/buger/goterm"
	"github.com/chzyer/readline"
	"github.com/fatih/color"
)

var (
	// Colors.
	userColor   = color.New(color.Bold)
	aiColor     = color.New(color.FgCyan)
	formatColor = color.New(color.FgGreen)
	fileColor   = color.New(color.FgRed)
	costColor   = color.New(color.FgYellow)

	width = goterm.Width()
)

// Separator printed to cli.
func Separator() {
	separator := strings.Repeat("-", width)
	formatColor.Println(separator)
}

// Title printed to cli.
func Title(text string, args ...any) {
	title := "      " + fmt.Sprintf(text, args...) + "      "
	leftWidth := (width - len(title)) / 2
	separator1 := strings.Repeat("-", leftWidth)
	separator2 := strings.Repeat("-", width-len(title)-len(separator1))
	output := fmt.Sprintf("%s%s%s", separator1, title, separator2)
	formatColor.Println(output)
}

// UserInput printed to cli.
func UserInput(text string, args ...any) {
	userColor.Printf(text, args...)
}

// AIInput printed to cli.
func AIInput(text string, args ...any) {
	aiColor.Printf(text, args...)
}

// CostInfo printed to cli.
func CostInfo(text string, args ...any) {
	costColor.Printf(text, args...)
}

// FileInfo printed to cli.
func FileInfo(text string, args ...any) {
	fileColor.Printf(text, args...)
}

// PromptUser for input.
func PromptUser() (string, error) {
	exit := false
	config := &readline.Config{
		Prompt:          "> ",
		InterruptPrompt: "^C",
		FuncFilterInputRune: func(r rune) (rune, bool) {
			if r == '\x0F' { // Ctrl+o.a
				exit = true
			}
			return r, true
		},
	}

	rl, err := readline.NewEx(config)
	if err != nil {
		return "", err
	}
	defer rl.Close()
	var lines []string
	for {
		line, err := rl.Readline()
		if err != nil {
			return "", err
		}
		rl.SetPrompt("")
		lines = append(lines, line)
		if err == readline.ErrInterrupt || exit {
			break
		}
	}
	return strings.Join(lines, "\n"), nil
}

// QueryUser a yes/no question.
func QueryUser(question string) bool {
	// Check if user wants to commit the message.
	surveyQuestion := &survey.Confirm{
		Message: "Apply commit",
	}
	confirm := false
	survey.AskOne(surveyQuestion, &confirm)
	return confirm
}
