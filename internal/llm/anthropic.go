package llm

import (
	"context"
	"fmt"
	"io"

	"github.com/liushuangls/go-anthropic/v2"
)

const (
	AnthropicCompletionModel string = "claude-v1" // Example model name.
)

// AnthropicClient wraps the go-anthropic client.
type AnthropicClient struct {
	client *anthropic.Client
}

func NewAnthropicClient(apiKey string) *AnthropicClient {
	client := anthropic.NewClient(
		apiKey,
		anthropic.WithBetaVersion(anthropic.BetaPromptCaching20240731),
		anthropic.WithBetaVersion(anthropic.BetaOutput128k20250219),
	)
	return &AnthropicClient{client: client}
}

// AnthropicCompletionStreamWrapper wraps the Anthropic streaming responses for chat requests.
type AnthropicCompletionStreamWrapper struct {
	tokens          chan string
	reasoningTokens chan string
	err             chan error
}

func (s *AnthropicCompletionStreamWrapper) Close() {
}

func (s *AnthropicCompletionStreamWrapper) Recv() (*StreamEvent, error) {
	select {
	case token := <-s.tokens:
		return &StreamEvent{
			Token: token,
		}, nil
	case token := <-s.reasoningTokens:
		return &StreamEvent{
			ReasoningToken: token,
		}, nil
	case err := <-s.err:
		if err == nil {
			return nil, io.EOF
		}
		return nil, err
	}
}

// CreateTextGeneration sends a text generation request to the Anthropic API.
func (c *AnthropicClient) CreateTextGeneration(ctx context.Context, request *CreateTextGenerationRequest) (Stream, error) {
	cacheControl := &anthropic.MessageCacheControl{
		Type: anthropic.CacheControlTypeEphemeral,
	}
	messages := make([]anthropic.Message, 0, len(request.Messages))
	for _, message := range request.Messages {
		switch message.Role {
		case UserRole, SystemRole:
			messages = append(messages, anthropic.NewUserTextMessage(message.Content))
		case AssistantRole:
			messages = append(messages, anthropic.NewAssistantTextMessage(message.Content))
		}
	}
	for _, message := range messages {
		for _, messageContent := range message.Content {
			messageContent.CacheControl = cacheControl
		}
	}

	sw := &AnthropicCompletionStreamWrapper{
		tokens:          make(chan string, 100),
		reasoningTokens: make(chan string, 100),
		err:             make(chan error, 1),
	}
	anthropicRequest := anthropic.MessagesStreamRequest{
		MessagesRequest: anthropic.MessagesRequest{
			Model:     anthropic.Model(request.Model),
			Messages:  messages,
			MaxTokens: request.MaxTokens,
		},
		OnContentBlockDelta: func(data anthropic.MessagesEventContentBlockDeltaData) {
			if data.Delta.MessageContentThinking != nil && data.Delta.Thinking != "" {
				sw.reasoningTokens <- data.Delta.Thinking
			}

			if data.Delta.Text != nil && *data.Delta.Text != "" {
				sw.tokens <- *data.Delta.Text
			}
		},
		OnError: func(r anthropic.ErrorResponse) {
			sw.err <- fmt.Errorf("%s - [%s]: %s", r.Type, r.Error.Type, r.Error.Message)
		},
	}
	if request.ThinkingTokens > 0 {
		anthropicRequest.Thinking = &anthropic.Thinking{
			Type:         anthropic.ThinkingTypeEnabled,
			BudgetTokens: request.ThinkingTokens,
		}
	}

	go func() {
		_, err := c.client.CreateMessagesStream(ctx, anthropicRequest)
		sw.err <- err
	}()
	return sw, nil
}

// CreateEmbedding returns an error because Anthropic does not provide embeddings.
func (c *AnthropicClient) CreateEmbedding(ctx context.Context, request *CreateEmbeddingRequest) ([]float32, error) {
	return nil, fmt.Errorf("embeddings are not supported by Anthropic models")
}
