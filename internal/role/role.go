package role

import (
	"bytes"
	_ "embed"
	"os"
	"os/user"
	"runtime"
	"strings"
	"text/template"
	"time"

	"github.com/Masterminds/sprig/v3"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
)

//go:embed system_prompt.tmpl
var systemPromptTemplate string

// TemplateData for rendering the system prompt.
type TemplateData struct {
	Username   string
	OS         string
	Arch       string
	Shell      string
	Home       string
	CWD        string
	Term       string
	RolePrompt string
	Time       string
	// ToolDiscovery gates the tool-discovery guidance: only rendered when
	// discoverable tool engines exist in the forest.
	ToolDiscovery bool
}

// Opts for a role.
type Opts struct {
	RoleName string
	// ToolDiscovery indicates discoverable tool engines are present; the
	// system prompt then explains the discovery protocol.
	ToolDiscovery  bool
	roleNameToRole map[string]*sgptpb.Role
}

// GetOpts on the given command.
func GetOpts(cmd *cobra.Command, defaultRole string, roles []*sgptpb.Role) *Opts {
	// Names are selectors (unique by construction); aliases are a
	// convenience and may collide across directories — first one wins.
	roleNameToRole := map[string]*sgptpb.Role{}
	for _, role := range roles {
		if _, ok := roleNameToRole[role.Name]; !ok {
			roleNameToRole[role.Name] = role
		}
		if role.Alias != "" {
			if _, ok := roleNameToRole[role.Alias]; !ok {
				roleNameToRole[role.Alias] = role
			}
		}
	}
	opts := &Opts{roleNameToRole: roleNameToRole}
	cmd.Flags().StringVarP(&opts.RoleName, "role", "r", defaultRole, "specify a role")
	return opts
}

// Parse role. Returns a role with the system prompt wrapper applied.
func (o *Opts) Parse() (*sgptpb.Role, error) {
	// Gather template data.
	u, err := user.Current()
	if err != nil {
		return nil, errors.Wrap(err, "getting current user")
	}
	username := u.Username
	if username == "" {
		username = u.Name
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return nil, errors.Wrap(err, "getting home directory")
	}

	cwd, err := os.Getwd()
	if err != nil {
		return nil, errors.Wrap(err, "getting current working directory")
	}

	data := TemplateData{
		Username:      username,
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
		Shell:         os.Getenv("SHELL"),
		Home:          home,
		CWD:           cwd,
		Term:          os.Getenv("TERM"),
		Time:          time.Now().Format("Mon Jan 2 3PM MST 2006"), // Hour resolution to allow for prompt caching.
		ToolDiscovery: o.ToolDiscovery,
	}

	// Build the result role.
	result := &sgptpb.Role{}

	// If a role is specified, inject its prompt and copy other fields.
	if o.RoleName != "" {
		role, err := o.expand(o.RoleName, map[string]bool{})
		if err != nil {
			return nil, err
		}
		result.Name = role.Name
		result.Alias = role.Alias
		result.Model = role.Model
		result.Files = role.Files
		result.Tools = role.Tools
		result.GraphNodes = role.GraphNodes
		data.RolePrompt = role.Prompt
	}

	// Render template.
	tmpl, err := template.New("system_prompt").Funcs(sprig.FuncMap()).Parse(systemPromptTemplate)
	if err != nil {
		return nil, errors.Wrap(err, "parsing system prompt template")
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, errors.Wrap(err, "executing system prompt template")
	}

	result.Prompt = buf.String()
	return result, nil
}

// expand resolves a role and merges its included roles depth-first. Included
// prompts are prepended so the outermost role's prompt has the last word.
// visitedNameSet both breaks inclusion cycles and dedupes diamond includes.
func (o *Opts) expand(name string, visitedNameSet map[string]bool) (*sgptpb.Role, error) {
	role, ok := o.roleNameToRole[name]
	if !ok {
		return nil, errors.Errorf("unknown role (%s)", name)
	}
	if visitedNameSet[role.Name] {
		return &sgptpb.Role{Name: role.Name, Alias: role.Alias}, nil
	}
	visitedNameSet[role.Name] = true

	result := &sgptpb.Role{
		Name:  role.Name,
		Alias: role.Alias,
		Model: role.Model,
	}
	var prompts []string
	for _, includedName := range role.GetRoles() {
		includedRole, err := o.expand(includedName, visitedNameSet)
		if err != nil {
			return nil, errors.Wrapf(err, "expanding role (%s)", name)
		}
		if includedRole.Prompt != "" {
			prompts = append(prompts, includedRole.Prompt)
		}
		result.Files = append(result.Files, includedRole.Files...)
		result.Tools = append(result.Tools, includedRole.Tools...)
		result.GraphNodes = append(result.GraphNodes, includedRole.GraphNodes...)
		// The outermost model wins; fall back to included roles' models.
		if result.Model == "" {
			result.Model = includedRole.Model
		}
	}
	if role.Prompt != "" {
		prompts = append(prompts, role.Prompt)
	}
	result.Prompt = strings.Join(prompts, "\n\n")
	result.Files = append(result.Files, role.Files...)
	result.Tools = append(result.Tools, role.Tools...)
	result.GraphNodes = append(result.GraphNodes, role.GraphNodes...)
	result.Files = dedupe(result.Files)
	result.Tools = dedupe(result.Tools)
	result.GraphNodes = dedupe(result.GraphNodes)
	return result, nil
}

func dedupe(values []string) []string {
	valueSet := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if valueSet[value] {
			continue
		}
		valueSet[value] = true
		result = append(result, value)
	}
	return result
}
