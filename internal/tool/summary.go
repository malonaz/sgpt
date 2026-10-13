package tool

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	aipb "github.com/malonaz/core/genproto/ai/v1"
	"google.golang.org/protobuf/types/known/structpb"
)

// TitleArgument is the user-facing title a model writes first when the
// tool's schema was built with pbjson.WithTitle.
const TitleArgument = "tool_call_title"

// TitleDescription documents TitleArgument for the model: the title is the
// call's collapsed, user-facing description in the timeline.
const TitleDescription = "A short user-facing title for this call, shown to the user in place of its arguments: " +
	"what it does and why, at most 8 words, sentence case, no trailing period " +
	"(e.g. 'Find where pendingReview is used', 'Run the timeline tests')."

// Title returns the title the model gave the call, if any.
func Title(toolCall *aipb.ToolCall) string {
	return toolCall.GetArguments().GetFields()[TitleArgument].GetStringValue()
}

// workingDirectory is resolved once: summaries run on every render.
var workingDirectory = sync.OnceValue(func() string {
	directory, _ := os.Getwd()
	return directory
})

// DisplayPath compacts a path for display: relative to the working directory
// when under it, home-relative (~) when under the home directory.
func DisplayPath(path string) string {
	if !filepath.IsAbs(path) {
		return path
	}
	if relative, err := filepath.Rel(workingDirectory(), path); err == nil && !strings.HasPrefix(relative, "..") {
		return relative
	}
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(path, home+string(filepath.Separator)) {
		return "~" + path[len(home):]
	}
	return path
}

// PathsSubject names a set of paths in a few characters: the path itself, or
// the first basename and how many others ("review.go +2").
func PathsSubject(paths []string) string {
	switch len(paths) {
	case 0:
		return ""
	case 1:
		return DisplayPath(paths[0])
	}
	return fmt.Sprintf("%s +%d", filepath.Base(paths[0]), len(paths)-1)
}

// Count renders a quantity with its unit: "1 line", "3 lines".
func Count(n int, singular, plural string) string {
	if n == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %s", n, plural)
}

// Quote wraps a free-text subject (a query) in quotes.
func Quote(text string) string { return "“" + text + "”" }

// Argument returns a top-level argument without unmarshaling the whole
// request: cheap, and tolerant of partially streamed arguments.
func Argument(toolCall *aipb.ToolCall, key string) *structpb.Value {
	return toolCall.GetArguments().GetFields()[key]
}

// StringList flattens a list value's string elements.
func StringList(value *structpb.Value) []string {
	values := value.GetListValue().GetValues()
	strs := make([]string, 0, len(values))
	for _, value := range values {
		if s := value.GetStringValue(); s != "" {
			strs = append(strs, s)
		}
	}
	return strs
}

// ResultField returns a top-level field of a structured result without
// unmarshaling it: results can be large (file contents, command output).
func ResultField(toolResult *aipb.ToolResult, key string) *structpb.Value {
	return toolResult.GetStructuredContent().GetStructValue().GetFields()[key]
}

// Fence wraps text in a code fence long enough that backticks inside the
// text cannot close it.
func Fence(language, text string) string {
	fence := "```"
	for strings.Contains(text, fence) {
		fence += "`"
	}
	return fence + language + "\n" + strings.TrimSuffix(text, "\n") + "\n" + fence
}

// DiffStat counts a unified diff's added and removed lines as "+12 −4".
func DiffStat(diff string) string {
	added, removed, inHunk := 0, 0, false
	for line := range strings.Lines(diff) {
		switch {
		case strings.HasPrefix(line, "@@"):
			inHunk = true
		case !inHunk:
			// File headers: "---"/"+++" here are not changed lines.
		case strings.HasPrefix(line, "+"):
			added++
		case strings.HasPrefix(line, "-"):
			removed++
		}
	}
	return fmt.Sprintf("+%d −%d", added, removed)
}

// firstLine trims text to its first non-empty line.
func firstLine(text string) string {
	text = strings.TrimSpace(text)
	if index := strings.IndexByte(text, '\n'); index != -1 {
		return strings.TrimSpace(text[:index])
	}
	return text
}
