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
	// Colors for different types of output
	userInputColor   = color.New(color.FgWhite)               // Bold blue for user input
	userCommandColor = color.New(color.FgGreen)               // Green for user commands
	aiOutputColor    = color.New(color.FgCyan)                // Cyan for AI responses
	aiThoughtColor   = color.New(color.FgHiYellow)            // Yellow for AI thoughts
	titleColor       = color.New(color.FgMagenta, color.Bold) // Bold magenta for titles
	separatorColor   = color.New(color.FgHiBlack)             // Dark grey for separators
	fileColor        = color.New(color.FgRed)                 // Red for file operations
	costColor        = color.New(color.FgYellow)              // Bright yellow for cost info
	promptColor      = color.New(color.FgHiBlue)              // Bright blue for prompts

	width = goterm.Width()
)

// Separator printed to cli.
func Separator() {
	separator := strings.Repeat("-", width)
	separatorColor.Println(separator)
}

// Title printed to cli.
func Title(text string, args ...any) {
	title := "      " + fmt.Sprintf(text, args...) + "      "
	leftWidth := (width - len(title)) / 2
	separator1 := strings.Repeat("-", leftWidth)
	separator2 := strings.Repeat("-", width-len(title)-len(separator1))
	output := fmt.Sprintf("%s%s%s", separator1, title, separator2)
	titleColor.Println(output)
}

// UserInput printed to cli.
func UserInput(text string, args ...any) {
	userInputColor.Printf(text, args...)
}

// UserCommand printed to cli.
func UserCommand(text string, args ...any) {
	if len(args) == 0 {
		userCommandColor.Print(text)
		return
	}
	userCommandColor.Printf(text, args...)
}

// AIOutput printed to cli.
func AIOutput(text string, args ...any) {
	text = strings.ReplaceAll(text, "%", "%%")
	aiOutputColor.Printf(text, args...)
}

// AIThought printed to cli.
func AIThought(text string, args ...any) {
	text = strings.ReplaceAll(text, "%", "%%")
	aiThoughtColor.Printf(text, args...)
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
		Prompt:            promptColor.Sprint("> "),
		InterruptPrompt:   "^C",
		HistoryFile:       "/tmp/sgpt.history",
		HistorySearchFold: true,
		FuncFilterInputRune: func(r rune) (rune, bool) {
			if r == '\x0A' { // Ctrl + J
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
		lines = append(lines, line)
		if err == readline.ErrInterrupt || exit {
			break
		}
		rl.SetPrompt("")
	}
	return strings.Join(lines, "\n"), nil
}

// QueryUser a yes/no question.
func QueryUser(question string) bool {
	surveyQuestion := &survey.Confirm{
		Message: question,
	}
	confirm := false
	survey.AskOne(surveyQuestion, &confirm)
	return confirm
}
