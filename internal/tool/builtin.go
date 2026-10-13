package tool

import (
	"sort"

	aipb "github.com/malonaz/core/genproto/ai/v1"
)

// nameToBuiltinTools indexes built-in tools by the user-facing name they are
// enabled under (--tool, the tool picker), exactly like tool engines. Most
// names enable a single tool of the same name; a group enables several.
// Tool packages register themselves in their init().
var nameToBuiltinTools = map[string][]*aipb.Tool{}

// RegisterBuiltin registers built-in tools, each under its own name.
func RegisterBuiltin(tools ...*aipb.Tool) {
	for _, builtinTool := range tools {
		nameToBuiltinTools[builtinTool.GetName()] = []*aipb.Tool{builtinTool}
	}
}

// RegisterBuiltinGroup registers built-in tools enabled together under name.
func RegisterBuiltinGroup(name string, tools ...*aipb.Tool) {
	nameToBuiltinTools[name] = tools
}

// Builtin returns the built-in tools enabled under name, if any.
func Builtin(name string) ([]*aipb.Tool, bool) {
	builtinTools, ok := nameToBuiltinTools[name]
	return builtinTools, ok
}

// BuiltinNames returns the user-facing names of all built-in tools, sorted.
func BuiltinNames() []string {
	names := make([]string, 0, len(nameToBuiltinTools))
	for name := range nameToBuiltinTools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
