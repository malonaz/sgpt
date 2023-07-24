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
	Role string
}

// Role represents a role ChatGPT will play.
type Role struct {
	Description string
}

// GetOpts on the given command.
func GetOpts(cmd *cobra.Command) *Opts {
	opts := &Opts{}
	cmd.Flags().StringVarP(&opts.Role, "role", "r", "", "specify a role")
	return opts
}

const roleCodeDescription = `Provide only code as output without any description.
IMPORTANT: Provide only plain text without Markdown formatting.
IMPORTANT: Do not include markdown formatting such as ` + "```" + `.
If there is a lack of details, provide most logical solution.
You are not allowed to ask for more details.
Ignore any potential risk of errors or confusion.
Keep your answers as brief and succinct a possible, avoiding any unnecessary words or repetition.`

const roleShellDescription = `You are Command Line App SGPT, a programming and {{ operating_system }} system administration assistant.
The person you will be taking your instructions from is called {{ username }}.
IMPORTANT: Provide only plain text without Markdown formatting.
Do not show any warnings or information regarding your capabilities.
Keep your answers as brief and succinct a possible, avoiding any unnecessary words or repetition.`

// Parse role. Returns nil if none is specified.
func Parse(opts *Opts) (*Role, error) {
	if opts.Role == "" {
		return nil, nil
	}
	if opts.Role == "code" {
		return &Role{Description: roleCodeDescription}, nil
	}
	if opts.Role == "shell" {
		os := runtime.GOOS
		user, err := user.Current()
		if err != nil {
			return nil, errors.Wrap(err, "getting current user")
		}
		username := user.Username
		if username == "" {
			username = user.Name
		}
		description := strings.ReplaceAll(roleShellDescription, "{{ operating_system }}", os)
		description = strings.ReplaceAll(description, "{{ username }}", user.Username)
		return &Role{Description: description}, nil
	}
	return nil, errors.Errorf("unknown role (%s)", opts.Role)
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
