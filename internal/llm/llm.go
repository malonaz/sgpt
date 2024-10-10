package llm

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/malonaz/sgpt/internal/configuration"
)

// Opts for model.
type Opts struct {
	Model string
}

// GetOpts on the given command.
func GetOpts(cmd *cobra.Command, defaultModel string) *Opts {
	opts := &Opts{}
	cmd.Flags().StringVarP(&opts.Model, "model", "m", defaultModel, "specify a model")
	return opts
}

// Instantiates and returns a new client.
func NewClient(config *configuration.Config, opts *Opts) (Client, *configuration.Model, *configuration.Provider, error) {
	var model *configuration.Model
	var provider *configuration.Provider
	for _, p := range config.Providers {
		for _, m := range p.Models {
			if m.Name == opts.Model || m.Alias == opts.Model {
				model = m
				provider = p
				break
			}
		}
	}
	if model == nil {
		return nil, nil, nil, fmt.Errorf("unknown model (%s)", opts.Model)
	}

	return NewOpenAIClient(provider.APIKey, provider.APIHost), model, provider, nil
}

type Message struct {
	Role    string
	Content string
}

type CreateTextGenerationRequest struct {
	Model            string
	Messages         []*Message
	StopWords        []string
	MaxTokens        int
	Temperature      float32
	TopP             float32
	PresencePenalty  float32
	FrequencyPenalty float32

	// If set, uses v1/chat/completion.
	UseChatCompletion bool
}

type StreamEvent struct {
	Token        string
	FinishReason string
}

type Stream interface {
	Recv() (*StreamEvent, error)
	Close()
}

type CreateEmbeddingRequest struct {
	Model string
	Input string
}

type Client interface {
	CreateEmbedding(context.Context, *CreateEmbeddingRequest) ([]float32, error)
	CreateTextGeneration(context.Context, *CreateTextGenerationRequest) (Stream, error)
}
