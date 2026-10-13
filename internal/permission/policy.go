// Package permission decides how tool calls are handled — run, ask the user,
// or refuse — from the user's configured rules and the grants they make
// mid-session ("always accept"). A sub-agent's policy is a child of its
// launcher's: it inherits every decision, so grants cascade down, never up.
package permission

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	aipb "github.com/malonaz/core/genproto/ai/v1"
	"google.golang.org/protobuf/types/known/structpb"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
)

const (
	ModeAllow  = sgptpb.PermissionMode_PERMISSION_MODE_ALLOW
	ModeReview = sgptpb.PermissionMode_PERMISSION_MODE_REVIEW
	ModeDeny   = sgptpb.PermissionMode_PERMISSION_MODE_DENY
)

// Decision is the verdict for one tool call: the mode, and the rule that
// produced it when one did (so a denial can be explained to the model).
type Decision struct {
	Mode sgptpb.PermissionMode
	Rule *sgptpb.PermissionRule
}

// Reason explains a decision in one line.
func (d Decision) Reason() string {
	if d.Rule == nil {
		return "tool policy"
	}
	return "rule " + Describe(d.Rule)
}

// Describe renders a rule as `tool[.argument ~ /pattern/] → mode`.
func Describe(rule *sgptpb.PermissionRule) string {
	var b strings.Builder
	b.WriteString(rule.GetTool())
	if rule.GetPattern() != "" {
		fmt.Fprintf(&b, ".%s ~ /%s/", rule.GetArgument(), rule.GetPattern())
	}
	b.WriteString(" → ")
	b.WriteString(ModeName(rule.GetMode()))
	return b.String()
}

// ModeName is the lowercase, prefix-less name of a mode ("allow").
func ModeName(mode sgptpb.PermissionMode) string {
	return strings.ToLower(strings.TrimPrefix(mode.String(), "PERMISSION_MODE_"))
}

type rule struct {
	proto   *sgptpb.PermissionRule
	pattern *regexp.Regexp
}

// Policy decides how tool calls are handled. The zero value is a root policy
// with no rules. Safe for concurrent use: sessions decide from turn
// goroutines while the user grants from the UI.
type Policy struct {
	parent *Policy
	rules  []rule

	mu     sync.RWMutex
	grants map[string]bool
}

// New compiles a root policy from the user's configured rules.
func New(protoRules []*sgptpb.PermissionRule) (*Policy, error) {
	policy := &Policy{rules: make([]rule, 0, len(protoRules))}
	for i, protoRule := range protoRules {
		if protoRule.GetTool() == "" {
			return nil, fmt.Errorf("permission rule %d: tool is required", i+1)
		}
		if protoRule.GetMode() == sgptpb.PermissionMode_PERMISSION_MODE_UNSPECIFIED {
			return nil, fmt.Errorf("permission rule %d (%s): mode is required", i+1, protoRule.GetTool())
		}
		if (protoRule.GetArgument() == "") != (protoRule.GetPattern() == "") {
			return nil, fmt.Errorf("permission rule %d (%s): argument and pattern go together", i+1, protoRule.GetTool())
		}
		compiled := rule{proto: protoRule}
		if protoRule.GetPattern() != "" {
			pattern, err := regexp.Compile(protoRule.GetPattern())
			if err != nil {
				return nil, fmt.Errorf("permission rule %d (%s): %w", i+1, protoRule.GetTool(), err)
			}
			compiled.pattern = pattern
		}
		policy.rules = append(policy.rules, compiled)
	}
	return policy, nil
}

// Child derives a sub-agent's policy: it decides exactly as this one does,
// live — so "always accept" in a chat also covers the sub-agents it launched
// — while its own grants stay with it.
func (p *Policy) Child() *Policy {
	return &Policy{parent: p}
}

// Grant lets a tool run without review for the rest of this policy's life:
// the user's word, so it overrides rules and inherited decisions alike.
func (p *Policy) Grant(tool string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.grants == nil {
		p.grants = map[string]bool{}
	}
	p.grants[tool] = true
}

// Grants lists the tools granted on this policy (not inherited ones), sorted.
func (p *Policy) Grants() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	grants := make([]string, 0, len(p.grants))
	for tool := range p.grants {
		grants = append(grants, tool)
	}
	sort.Strings(grants)
	return grants
}

func (p *Policy) granted(tool string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.grants[tool]
}

// Decide returns the verdict for a tool call. autoExecute is the tool's own
// judgment (side-effect-free calls need no review); it is the fallback when
// no rule matches.
func (p *Policy) Decide(toolCall *aipb.ToolCall, autoExecute bool) Decision {
	if p.granted(toolCall.GetName()) {
		return Decision{Mode: ModeAllow}
	}
	if p.parent != nil {
		return p.parent.Decide(toolCall, autoExecute)
	}
	if decision, ok := p.match(toolCall); ok {
		return decision
	}
	if autoExecute {
		return Decision{Mode: ModeAllow}
	}
	return Decision{Mode: ModeReview}
}

func (p *Policy) match(toolCall *aipb.ToolCall) (Decision, bool) {
	for _, candidate := range p.rules {
		if candidate.matches(toolCall) {
			return Decision{Mode: candidate.proto.GetMode(), Rule: candidate.proto}, true
		}
	}
	return Decision{}, false
}

func (r rule) matches(toolCall *aipb.ToolCall) bool {
	if r.proto.GetTool() != toolCall.GetName() {
		return false
	}
	if r.pattern == nil {
		return true
	}
	for _, value := range argumentValues(toolCall.GetArguments(), strings.Split(r.proto.GetArgument(), ".")) {
		if r.pattern.MatchString(value) {
			return true
		}
	}
	return false
}

// argumentValues collects the scalar values reachable through path, fanning
// out across list elements so "patches.search" yields every patch's search.
func argumentValues(arguments *structpb.Struct, path []string) []string {
	if arguments == nil {
		return nil
	}
	return leafValues(structpb.NewStructValue(arguments), path)
}

func leafValues(value *structpb.Value, path []string) []string {
	switch kind := value.GetKind().(type) {
	case *structpb.Value_ListValue:
		var values []string
		for _, element := range kind.ListValue.GetValues() {
			values = append(values, leafValues(element, path)...)
		}
		return values
	case *structpb.Value_StructValue:
		if len(path) == 0 {
			return nil
		}
		field, ok := kind.StructValue.GetFields()[path[0]]
		if !ok {
			return nil
		}
		return leafValues(field, path[1:])
	case *structpb.Value_StringValue:
		if len(path) == 0 {
			return []string{kind.StringValue}
		}
	case *structpb.Value_NumberValue, *structpb.Value_BoolValue:
		if len(path) == 0 {
			return []string{fmt.Sprint(value.AsInterface())}
		}
	}
	return nil
}
