package model

import (
	"strings"

	"github.com/pkg/errors"
	"github.com/pkoukk/tiktoken-go"
	"github.com/sashabaranov/go-openai"
	"github.com/spf13/cobra"

	"github.com/malonaz/sgpt/configuration"
	"github.com/shopspring/decimal"
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

// Model represents a gpt model.
type Model struct {
	ID            string
	InputPricing  decimal.Decimal
	OutputPricing decimal.Decimal
}

var models = []*Model{
	// GPT-4 @ 32k.
	{ID: openai.GPT432K0613, InputPricing: decimal.RequireFromString("0.06"), OutputPricing: decimal.RequireFromString("0.12")},
	{ID: openai.GPT432K0314, InputPricing: decimal.RequireFromString("0.06"), OutputPricing: decimal.RequireFromString("0.12")},
	{ID: openai.GPT432K, InputPricing: decimal.RequireFromString("0.06"), OutputPricing: decimal.RequireFromString("0.12")},
	// GPT-4 @ 8k
	{ID: openai.GPT40613, InputPricing: decimal.RequireFromString("0.03"), OutputPricing: decimal.RequireFromString("0.06")},
	{ID: openai.GPT40314, InputPricing: decimal.RequireFromString("0.03"), OutputPricing: decimal.RequireFromString("0.06")},
	{ID: openai.GPT4, InputPricing: decimal.RequireFromString("0.03"), OutputPricing: decimal.RequireFromString("0.06")},

	// GPT-3.5-turbo @ 16k
	{ID: openai.GPT3Dot5Turbo16K0613, InputPricing: decimal.RequireFromString("0.003"), OutputPricing: decimal.RequireFromString("0.004")},
	{ID: openai.GPT3Dot5Turbo16K, InputPricing: decimal.RequireFromString("0.003"), OutputPricing: decimal.RequireFromString("0.004")},
	// GPT-3.5-turbo @ 4k
	{ID: openai.GPT3Dot5Turbo0613, InputPricing: decimal.RequireFromString("0.0015"), OutputPricing: decimal.RequireFromString("0.002")},
	{ID: openai.GPT3Dot5Turbo0301, InputPricing: decimal.RequireFromString("0.0015"), OutputPricing: decimal.RequireFromString("0.002")},
	{ID: openai.GPT3Dot5Turbo, InputPricing: decimal.RequireFromString("0.0015"), OutputPricing: decimal.RequireFromString("0.002")},

	// Embeddings.
	{ID: "text-embedding-ada-002", InputPricing: decimal.RequireFromString("0.0001")},

	// We do not have pricing informnation for this.
	{ID: openai.GPT3TextDavinci003},
	{ID: openai.GPT3TextDavinci002},
	{ID: openai.GPT3TextCurie001},
	{ID: openai.GPT3TextBabbage001},
	{ID: openai.GPT3TextAda001},
	{ID: openai.GPT3TextDavinci001},
	{ID: openai.GPT3DavinciInstructBeta},
	{ID: openai.GPT3Davinci},
	{ID: openai.GPT3CurieInstructBeta},
	{ID: openai.GPT3Curie},
	{ID: openai.GPT3Ada},
	{ID: openai.GPT3Babbage},
}

// Parse the model.
func Parse(opts *Opts, config *configuration.Config) (*Model, error) {
	for _, model := range models {
		if model.ID == opts.Model {
			return model, nil
		}
	}
	return nil, errors.Errorf("unknown model (%s)", opts.Model)
}

// CalculateEmbeddingCost for the given input.
func (m *Model) CalculateEmbeddingCost(input string) (int64, decimal.Decimal, error) {
	tkm, err := tiktoken.EncodingForModel(m.ID)
	if err != nil {
		return 0, decimal.Zero, errors.Wrap(err, "encoding for model")
	}
	tokens := int64(len(tkm.Encode(input, nil, nil)))
	pricing := m.InputPricing
	cost := pricing.Mul(decimal.NewFromInt(tokens)).Div(decimal.NewFromInt(1000))
	return tokens, cost, nil
}

// CalculateRequestCost of these messages.
func (m *Model) CalculateRequestCost(messages ...openai.ChatCompletionMessage) (int64, decimal.Decimal, error) {
	return m.calculateCost(messages, true)
}

// CalculateResponseCost of these messages.
func (m *Model) CalculateResponseCost(messages ...openai.ChatCompletionMessage) (int64, decimal.Decimal, error) {
	return m.calculateCost(messages, false)
}

func (m *Model) calculateCost(messages []openai.ChatCompletionMessage, input bool) (int64, decimal.Decimal, error) {
	tokens, err := numTokensFromMessages(messages, m.ID)
	if err != nil {
		return 0, decimal.Zero, errors.Wrap(err, "counting tokens in messages")
	}
	pricing := m.OutputPricing
	if input {
		pricing = m.InputPricing
	}
	cost := pricing.Mul(decimal.NewFromInt(tokens)).Div(decimal.NewFromInt(1000))
	return tokens, cost, nil
}


func numTokensFromMessages(messages []openai.ChatCompletionMessage, modelID string) (int64, error) {
	tkm, err := tiktoken.EncodingForModel(modelID)
	if err != nil {
		return 0, errors.Wrap(err, "encoding for model")
	}

	var tokensPerMessage, tokensPerName int
	switch modelID {
	case "gpt-3.5-turbo-0613",
		"gpt-3.5-turbo-16k-0613",
		"gpt-4-0314",
		"gpt-4-32k-0314",
		"gpt-4-0613",
		"gpt-4-32k-0613":
		tokensPerMessage = 3
		tokensPerName = 1
	case "gpt-3.5-turbo-0301":
		tokensPerMessage = 4 // every message follows <|start|>{role/name}\n{content}<|end|>\n
		tokensPerName = -1   // if there's a name, the role is omitted
	default:
		if strings.Contains(modelID, "gpt-3.5-turbo") {
			return numTokensFromMessages(messages, "gpt-3.5-turbo-0613")
		} else if strings.Contains(modelID, "gpt-4") {
			return numTokensFromMessages(messages, "gpt-4-0613")
		} else {
			return 0, errors.Errorf("num_tokens_from_messages() is not implemented for model %s", modelID)
		}
	}

	numTokens := 0
	for _, message := range messages {
		numTokens += tokensPerMessage
		numTokens += len(tkm.Encode(message.Content, nil, nil))
		numTokens += len(tkm.Encode(message.Role, nil, nil))
		numTokens += len(tkm.Encode(message.Name, nil, nil))
		if message.Name != "" {
			numTokens += tokensPerName
		}
	}
	numTokens += 3 // every reply is primed with <|start|>assistant<|message|>
	return int64(numTokens), nil
}
