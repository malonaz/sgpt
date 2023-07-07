package model

import (
	"github.com/pkg/errors"
	"github.com/sashabaranov/go-openai"
	"github.com/spf13/cobra"

	"github.com/malonaz/sgpt/configuration"
)

// Opts for model.
type Opts struct {
	Model string
}

// GetOpts on the given command.
func GetOpts(cmd *cobra.Command, config *configuration.Config) *Opts {
	opts := &Opts{}
	cmd.Flags().StringVar(&opts.Model, "model", config.DefaultModel, "specify a model")
	return opts
}

var modelSet = map[string]struct{}{
	openai.GPT432K0613:             {},
	openai.GPT432K0314:             {},
	openai.GPT432K:                 {},
	openai.GPT40613:                {},
	openai.GPT40314:                {},
	openai.GPT4:                    {},
	openai.GPT3Dot5Turbo0613:       {},
	openai.GPT3Dot5Turbo0301:       {},
	openai.GPT3Dot5Turbo16K:        {},
	openai.GPT3Dot5Turbo16K0613:    {},
	openai.GPT3Dot5Turbo:           {},
	openai.GPT3TextDavinci003:      {},
	openai.GPT3TextDavinci002:      {},
	openai.GPT3TextCurie001:        {},
	openai.GPT3TextBabbage001:      {},
	openai.GPT3TextAda001:          {},
	openai.GPT3TextDavinci001:      {},
	openai.GPT3DavinciInstructBeta: {},
	openai.GPT3Davinci:             {},
	openai.GPT3CurieInstructBeta:   {},
	openai.GPT3Curie:               {},
	openai.GPT3Ada:                 {},
	openai.GPT3Babbage:             {},
}

// Parse the model.
func Parse(opts *Opts, config *configuration.Config) (string, error) {
	if _, ok := modelSet[opts.Model]; !ok {
		return "", errors.Errorf("unknown model (%s)", opts.Model)
	}
	return opts.Model, nil
}
