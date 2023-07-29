package cli

import (
	"fmt"
	"strings"

	"github.com/buger/goterm"
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
	userColor.Printf("-> %s", fmt.Sprintf(text, args...))
}

// AIInput printed to cli.
func AIInput(text string, args ...any) {
	aiColor.Printf(text, args...)
}

// CostInput printed to cli.
func CostInput(text string, args ...any) {
	costColor.Printf(text, args...)
}
