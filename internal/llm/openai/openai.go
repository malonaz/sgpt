package openai

import (
	"context"
	"fmt"
	"net/http"

	openai "github.com/sashabaranov/go-openai"

	"github.com/malonaz/sgpt/internal/llm"
)

const (
	SmallEmbeddings3 = string(openai.SmallEmbedding3)
)

type Opts struct {
	BaseURL string `long:"base-url" env:"BASE_URL" description:"base url" required:"true"`
	APIKey  string `long:"api-key" env:"API_KEY" description:"api key" required:"true"`
}

// Client for openai.
type Client struct {
	opts   *Opts
	client *openai.Client
}

func NewClient(opts *Opts, options ...any) *Client {
	config := openai.DefaultConfig(opts.APIKey)
	config.BaseURL = opts.BaseURL
	for _, option := range options {
		switch t := option.(type) {
		case *http.Client:
			config.HTTPClient = t
		default:
			panic(fmt.Errorf("unknown option type %T", option))
		}
	}

	client := openai.NewClientWithConfig(config)
	return &Client{
		opts:   opts,
		client: client,
	}
}

func (c *Client) Get() *openai.Client {
	return c.client
}

type CompletionStreamWrapper struct {
	stream *openai.CompletionStream
}

func (s *CompletionStreamWrapper) Close() { s.stream.Close() }
func (s *CompletionStreamWrapper) Recv() (*llm.StreamEvent, error) {
	response, err := s.stream.Recv()
	if err != nil {
		return nil, err
	}
	if len(response.Choices) == 0 {
		return nil, fmt.Errorf("CompletionResponse returned no choice: %+v", response)
	}
	return &llm.StreamEvent{
		Token:        response.Choices[0].Text,
		FinishReason: response.Choices[0].FinishReason,
	}, nil
}

type ChatCompletionStreamWrapper struct {
	stream *openai.ChatCompletionStream
}

func (s *ChatCompletionStreamWrapper) Close() { s.stream.Close() }
func (s *ChatCompletionStreamWrapper) Recv() (*llm.StreamEvent, error) {
	response, err := s.stream.Recv()
	if err != nil {
		return nil, err
	}
	if len(response.Choices) == 0 {
		return nil, fmt.Errorf("ChatCompletionResponse returned no choice: %+v", response)
	}
	return &llm.StreamEvent{
		Token:        response.Choices[0].Delta.Content,
		FinishReason: string(response.Choices[0].FinishReason),
	}, nil
}

func (c *Client) CreateTextGeneration(ctx context.Context, request *llm.CreateTextGenerationRequest) (llm.Stream, error) {
	if request.UseChatCompletion {
		request := openai.ChatCompletionRequest{
			Model:            request.Model,
			Stop:             request.StopWords,
			MaxTokens:        request.MaxTokens,
			Temperature:      request.Temperature,
			TopP:             request.TopP,
			PresencePenalty:  request.PresencePenalty,
			FrequencyPenalty: request.FrequencyPenalty,
			Stream:           true,
			Messages: []openai.ChatCompletionMessage{
				{
					Role:    "user",
					Content: request.Prompt,
				},
			},
		}
		stream, err := c.client.CreateChatCompletionStream(ctx, request)
		if err != nil {
			return nil, fmt.Errorf("creating completion stream: %v", err)
		}
		return &ChatCompletionStreamWrapper{stream}, nil
	}

	completionRequest := openai.CompletionRequest{
		Model:            request.Model,
		Prompt:           request.Prompt,
		Stop:             request.StopWords,
		MaxTokens:        request.MaxTokens,
		Temperature:      request.Temperature,
		TopP:             request.TopP,
		PresencePenalty:  request.PresencePenalty,
		FrequencyPenalty: request.FrequencyPenalty,
		Stream:           true,
	}

	stream, err := c.client.CreateCompletionStream(ctx, completionRequest)
	if err != nil {
		return nil, fmt.Errorf("creating completion stream: %v", err)
	}
	return &CompletionStreamWrapper{stream}, nil
}

func (c *Client) CreateEmbedding(ctx context.Context, request *llm.CreateEmbeddingRequest) ([]float32, error) {
	embeddingsRequest := openai.EmbeddingRequest{
		Input: []string{request.Input},
		Model: openai.EmbeddingModel(request.Model),
	}
	response, err := c.client.CreateEmbeddings(ctx, embeddingsRequest)
	if err != nil {
		return nil, fmt.Errorf("creating embedding: %w", err)
	}
	return response.Data[0].Embedding, nil
}
