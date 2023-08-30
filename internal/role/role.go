package role

import (
	"os/user"
	"runtime"
	"strings"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

// Opts for a role.
type Opts struct {
	Role  string
	roles map[string]string
}

// Role represents a role ChatGPT will play.
type Role struct {
	Name        string
	Description string
}

// GetOpts on the given command.
func GetOpts(cmd *cobra.Command, defaultRole string, roles map[string]string) *Opts {
	if roles == nil {
		roles = map[string]string{}
	}
	roles["code"] = roleCodeDescription
	roles["shell"] = roleShellDescription
	opts := &Opts{roles: roles}
	cmd.Flags().StringVarP(&opts.Role, "role", "r", defaultRole, "specify a role")
	return opts
}

// Parse role. Returns nil if none is specified.
func (o *Opts) Parse() (*Role, error) {
	if o.Role == "" {
		return nil, nil
	}

	description, ok := o.roles[o.Role]
	if !ok {
		return nil, errors.Errorf("unknown role (%s)", o.Role)
	}
	if strings.Contains(description, "{{ username }}") {
		user, err := user.Current()
		if err != nil {
			return nil, errors.Wrap(err, "getting current user")
		}
		username := user.Username
		if username == "" {
			username = user.Name
		}
		description = strings.ReplaceAll(description, "{{ username }}", user.Username)
	}
	if strings.Contains(description, "{{ os }}") {
		description = strings.ReplaceAll(description, "{{ os }}", runtime.GOOS)
	}
	return &Role{
		Name:        o.Role,
		Description: description,
	}, nil
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
