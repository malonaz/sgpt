package graph

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/malonaz/core/go/pbutil"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
)

// Artifact markdown format (.node.md, .role.md): lines starting with
// `@directive(...)` carry the structured fields; everything else is the
// body (a node's content, a role's prompt).
//
// Two directive shapes:
//   - argument form, inline, all arguments double-quoted:
//     @label("key", "value")
//   - text form, inline or multiline (terminated by a lone `)` line):
//     @summary(one line)
//     @summary(
//     free text...
//     )
//
// Anything that looks like a directive but isn't valid is an error — never
// silently folded into the body.

// directiveStartPattern matches a directive opener at line start.
var directiveStartPattern = regexp.MustCompile(`^@([a-z_]+)\(`)

// directive is one parsed `@name(...)`.
type directive struct {
	name string
	// args is set for the argument form (all-quoted interior).
	args []string
	// text is set for the text form.
	text string
}

// parseArtifactMarkdown splits a markdown artifact into its directives and
// body.
func parseArtifactMarkdown(data string) ([]directive, string, error) {
	var directives []directive
	var bodyLines []string
	lines := strings.Split(data, "\n")
	for lineIndex := 0; lineIndex < len(lines); lineIndex++ {
		line := lines[lineIndex]
		match := directiveStartPattern.FindStringSubmatch(line)
		if match == nil {
			bodyLines = append(bodyLines, line)
			continue
		}
		name := match[1]
		rest := line[len(match[0]):]

		// Inline form: the directive closes on the same line.
		if strings.HasSuffix(rest, ")") {
			interior := strings.TrimSuffix(rest, ")")
			parsed := directive{name: name}
			if args, ok := parseQuotedArguments(interior); ok {
				parsed.args = args
			} else {
				parsed.text = strings.TrimSpace(interior)
			}
			directives = append(directives, parsed)
			continue
		}

		// Multiline text form: capture until a lone ")" line.
		var textLines []string
		if strings.TrimSpace(rest) != "" {
			textLines = append(textLines, rest)
		}
		terminated := false
		for lineIndex++; lineIndex < len(lines); lineIndex++ {
			if strings.TrimSpace(lines[lineIndex]) == ")" {
				terminated = true
				break
			}
			textLines = append(textLines, lines[lineIndex])
		}
		if !terminated {
			return nil, "", fmt.Errorf("@%s( is never closed by a lone \")\" line", name)
		}
		directives = append(directives, directive{name: name, text: strings.TrimSpace(strings.Join(textLines, "\n"))})
	}
	return directives, strings.TrimSpace(strings.Join(bodyLines, "\n")), nil
}

// parseQuotedArguments parses `"a", "b"` interiors; ok is false when the
// interior is not a pure quoted-argument list (text form).
func parseQuotedArguments(interior string) ([]string, bool) {
	trimmed := strings.TrimSpace(interior)
	if trimmed == "" || !strings.HasPrefix(trimmed, `"`) {
		return nil, false
	}
	var args []string
	rest := trimmed
	for {
		if !strings.HasPrefix(rest, `"`) {
			return nil, false
		}
		closing := strings.Index(rest[1:], `"`)
		if closing < 0 {
			return nil, false
		}
		args = append(args, rest[1:1+closing])
		rest = strings.TrimSpace(rest[closing+2:])
		if rest == "" {
			return args, true
		}
		if !strings.HasPrefix(rest, ",") {
			return nil, false
		}
		rest = strings.TrimSpace(rest[1:])
	}
}

// argumentCount enforces a directive's argument form and arity.
func (d *directive) arguments(count int) ([]string, error) {
	if d.args == nil {
		return nil, fmt.Errorf(`@%s expects %d quoted argument(s), e.g. @%s("...")`, d.name, count, d.name)
	}
	if len(d.args) != count {
		return nil, fmt.Errorf("@%s expects %d quoted argument(s), got %d", d.name, count, len(d.args))
	}
	return d.args, nil
}

// textContent enforces a directive's text form.
func (d *directive) textContent() (string, error) {
	if d.args != nil {
		return "", fmt.Errorf("@%s expects free text, not quoted arguments", d.name)
	}
	return d.text, nil
}

// parseNodeMarkdown builds a Node from a .node.md file: body = content;
// directives: @summary (text), @label("k","v"), @file("path").
func parseNodeMarkdown(data []byte) (*sgptpb.Node, error) {
	directives, body, err := parseArtifactMarkdown(string(data))
	if err != nil {
		return nil, err
	}
	node := &sgptpb.Node{}
	node.SetContent(body)
	for _, parsed := range directives {
		switch parsed.name {
		case "summary":
			text, err := parsed.textContent()
			if err != nil {
				return nil, err
			}
			if node.GetSummary() != "" {
				return nil, fmt.Errorf("@summary declared twice")
			}
			node.SetSummary(text)
		case "label":
			args, err := parsed.arguments(2)
			if err != nil {
				return nil, err
			}
			if node.GetLabels() == nil {
				node.SetLabels(map[string]string{})
			}
			node.GetLabels()[args[0]] = args[1]
		case "file":
			args, err := parsed.arguments(1)
			if err != nil {
				return nil, err
			}
			node.SetFiles(append(node.GetFiles(), args[0]))
		default:
			return nil, fmt.Errorf("unknown node directive @%s (want @summary, @label, @file)", parsed.name)
		}
	}
	return node, nil
}

// parseToolSetJSON parses a .toolset file (strict JSON).
func parseToolSetJSON(data []byte) (*sgptpb.ToolSet, error) {
	toolSet := &sgptpb.ToolSet{}
	if err := pbutil.JSONUnmarshalStrict(data, toolSet); err != nil {
		return nil, err
	}
	return toolSet, nil
}

// parseRoleMarkdown builds a Role from a .role.md file: body = prompt;
// directives: @alias, @model, @tool, @role, @node, @file (all quoted-arg).
func parseRoleMarkdown(data []byte) (*sgptpb.Role, error) {
	directives, body, err := parseArtifactMarkdown(string(data))
	if err != nil {
		return nil, err
	}
	role := &sgptpb.Role{Prompt: body}
	for _, parsed := range directives {
		switch parsed.name {
		case "alias":
			args, err := parsed.arguments(1)
			if err != nil {
				return nil, err
			}
			if role.Alias != "" {
				return nil, fmt.Errorf("@alias declared twice")
			}
			role.Alias = args[0]
		case "model":
			args, err := parsed.arguments(1)
			if err != nil {
				return nil, err
			}
			if role.Model != "" {
				return nil, fmt.Errorf("@model declared twice")
			}
			role.Model = args[0]
		case "tool":
			args, err := parsed.arguments(1)
			if err != nil {
				return nil, err
			}
			role.Tools = append(role.Tools, args[0])
		case "role":
			args, err := parsed.arguments(1)
			if err != nil {
				return nil, err
			}
			role.Roles = append(role.Roles, args[0])
		case "node":
			args, err := parsed.arguments(1)
			if err != nil {
				return nil, err
			}
			role.GraphNodes = append(role.GraphNodes, args[0])
		case "file":
			args, err := parsed.arguments(1)
			if err != nil {
				return nil, err
			}
			role.Files = append(role.Files, args[0])
		default:
			return nil, fmt.Errorf("unknown role directive @%s (want @alias, @model, @tool, @role, @node, @file)", parsed.name)
		}
	}
	return role, nil
}
