package tools

import (
	"sort"

	aipb "github.com/malonaz/core/genproto/ai/v1"
)

// nameToInternalTool indexes built-in tools by their advertised name, so they
// are enabled via --tool exactly like tool engines.
var nameToInternalTool = map[string]*aipb.Tool{
	ShellCommand.GetName(): ShellCommand,
	ReadFiles.GetName():    ReadFiles,
	EditFile.GetName():     EditFile,
}

// InternalTool returns the built-in tool definition for name, if any.
func InternalTool(name string) (*aipb.Tool, bool) {
	tool, ok := nameToInternalTool[name]
	return tool, ok
}

// InternalToolNames returns the names of all built-in tools, sorted.
func InternalToolNames() []string {
	names := make([]string, 0, len(nameToInternalTool))
	for name := range nameToInternalTool {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
