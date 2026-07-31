package tool

import (
	"sort"

	aipb "github.com/malonaz/core/genproto/ai/v1"
)

// nameToBuiltinTool indexes built-in tools by their advertised name, so they
// are enabled via --tool exactly like tool engines. Tool packages register
// themselves via RegisterBuiltin in their init().
var nameToBuiltinTool = map[string]*aipb.Tool{}

// RegisterBuiltin registers a built-in tool definition under its name.
func RegisterBuiltin(tools ...*aipb.Tool) {
	for _, builtinTool := range tools {
		nameToBuiltinTool[builtinTool.GetName()] = builtinTool
	}
}

// Builtin returns the built-in tool definition for name, if any.
func Builtin(name string) (*aipb.Tool, bool) {
	builtinTool, ok := nameToBuiltinTool[name]
	return builtinTool, ok
}

// BuiltinNames returns the names of all built-in tools, sorted.
func BuiltinNames() []string {
	names := make([]string, 0, len(nameToBuiltinTool))
	for name := range nameToBuiltinTool {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
