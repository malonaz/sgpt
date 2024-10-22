package role

import (
	"fmt"
	"os/user"
	"runtime"
	"strings"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"

	"github.com/malonaz/sgpt/internal/configuration"
)

// Opts for a role.
type Opts struct {
	RoleName       string
	roleNameToRole map[string]*configuration.Role
}

// GetOpts on the given command.
func GetOpts(cmd *cobra.Command, defaultRole string, roles []*configuration.Role) *Opts {
	allRoles := append(defaultRoles, roles...)
	roleNameToRole := map[string]*configuration.Role{}
	for _, role := range allRoles {
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

// Parse role. Returns nil if none is specified.
func (o *Opts) Parse() (*configuration.Role, error) {
	if o.RoleName == "" {
		return nil, nil
	}

	role, ok := o.roleNameToRole[o.RoleName]
	if !ok {
		return nil, errors.Errorf("unknown role (%s)", o.RoleName)
	}
	if strings.Contains(role.Prompt, "{{ username }}") {
		user, err := user.Current()
		if err != nil {
			return nil, errors.Wrap(err, "getting current user")
		}
		username := user.Username
		if username == "" {
			username = user.Name
		}
		role.Prompt = strings.ReplaceAll(role.Prompt, "{{ username }}", user.Username)
	}
	if strings.Contains(role.Prompt, "{{ os }}") {
		role.Prompt = strings.ReplaceAll(role.Prompt, "{{ os }}", runtime.GOOS)
	}
	return role, nil
}

// EmbeddingsAugmentedAssistant is the role of an embedder.
const EmbeddingsAugmentedAssistant = `You are an AI Assistant whose primary function is to answer user inquiries by accessing chunks of information from embeddings. Using the provided embeddings as data sources, you will attempt to accurately and intelligently answer questions to the best of your ability.
Instructions:
1. Understand the user's query.
2. Match the query to the most appropriate chunk of information available in the embeddings.
3. Provide a clear and concise answer using the information you've retrieved from the embeddings.
4. If the information doesn't entirely address the user's query or if additional information is required, inform the user while delivering the best possible response derived from the available data.
5. Always prioritize relevance and accuracy when answering queries based on the embeddings.
`
