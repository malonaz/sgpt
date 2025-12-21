package role

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"os/user"
	"runtime"
	"text/template"

	"github.com/Masterminds/sprig/v3"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"

	"github.com/malonaz/sgpt/internal/configuration"
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
}

// Opts for a role.
type Opts struct {
	RoleName       string
	roleNameToRole map[string]*configuration.Role
}

// GetOpts on the given command.
func GetOpts(cmd *cobra.Command, defaultRole string, roles []*configuration.Role) *Opts {
	roleNameToRole := map[string]*configuration.Role{}
	for _, role := range roles {
		if _, ok := roleNameToRole[role.Name]; ok {
			panic(fmt.Sprintf("Duplicate role name (%s)", role.Name))
		}
		roleNameToRole[role.Name] = role
		if role.Alias != "" {
			if _, ok := roleNameToRole[role.Alias]; ok {
				panic(fmt.Sprintf("Duplicate role alias (%s)", role.Alias))
			}
			roleNameToRole[role.Alias] = role
		}
	}
	opts := &Opts{roleNameToRole: roleNameToRole}
	cmd.Flags().StringVarP(&opts.RoleName, "role", "r", defaultRole, "specify a role")
	return opts
}

// Parse role. Returns a role with the system prompt wrapper applied.
func (o *Opts) Parse() (*configuration.Role, error) {
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
		Username: username,
		OS:       runtime.GOOS,
		Arch:     runtime.GOARCH,
		Shell:    os.Getenv("SHELL"),
		Home:     home,
		CWD:      cwd,
		Term:     os.Getenv("TERM"),
	}

	// Build the result role.
	result := &configuration.Role{}

	// If a role is specified, inject its prompt and copy other fields.
	if o.RoleName != "" {
		role, ok := o.roleNameToRole[o.RoleName]
		if !ok {
			return nil, errors.Errorf("unknown role (%s)", o.RoleName)
		}
		result.Name = role.Name
		result.Alias = role.Alias
		result.Model = role.Model
		result.Files = role.Files
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
