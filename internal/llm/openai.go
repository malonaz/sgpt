package llm

import (
	"context"
	"fmt"

	"github.com/sashabaranov/go-openai"
)

const (
	SmallEmbeddings3 = string(openai.SmallEmbedding3)
)

// OpenAIClient for openai.
type OpenAIClient struct {
	opts   *Opts
	client *openai.Client
}

func NewOpenAIClient(apiKey, apiHost string) *OpenAIClient {
	openAIConfig := openai.DefaultConfig(apiKey)
	openAIConfig.BaseURL = apiHost
	client := openai.NewClientWithConfig(openAIConfig)
	return &OpenAIClient{client: client}
}

func (c *OpenAIClient) Get() *openai.Client {
	return c.client
}

type OpenAICompletionStreamWrapper struct {
	stream *openai.CompletionStream
}

func (s *OpenAICompletionStreamWrapper) Close() { s.stream.Close() }
func (s *OpenAICompletionStreamWrapper) Recv() (*StreamEvent, error) {
	response, err := s.stream.Recv()
	if err != nil {
		return nil, err
	}
	if len(response.Choices) == 0 {
		return nil, fmt.Errorf("CompletionResponse returned no choice: %+v", response)
	}
	return &StreamEvent{
		Token:        response.Choices[0].Text,
		FinishReason: response.Choices[0].FinishReason,
	}, nil
}

type ChatOpenAICompletionStreamWrapper struct {
	stream *openai.ChatCompletionStream
}

func (s *ChatOpenAICompletionStreamWrapper) Close() { s.stream.Close() }
func (s *ChatOpenAICompletionStreamWrapper) Recv() (*StreamEvent, error) {
	response, err := s.stream.Recv()
	if err != nil {
		return nil, err
	}
	if len(response.Choices) == 0 {
		return nil, fmt.Errorf("ChatCompletionResponse returned no choice: %+v", response)
	}
	return &StreamEvent{
		Token:        response.Choices[0].Delta.Content,
		FinishReason: string(response.Choices[0].FinishReason),
	}, nil
}

func (c *OpenAIClient) CreateTextGeneration(ctx context.Context, request *CreateTextGenerationRequest) (Stream, error) {
	messages := make([]openai.ChatCompletionMessage, 0, len(request.Messages))
	for _, message := range request.Messages {
		var role string
		switch message.Role {
		case UserRole:
			role = openai.ChatMessageRoleUser
		case SystemRole:
			role = openai.ChatMessageRoleSystem
		case AssistantRole:
			role = openai.ChatMessageRoleAssistant
		default:
			return nil, fmt.Errorf("unknown role: %s", message.Role)
		}
		messages = append(messages, openai.ChatCompletionMessage{Content: message.Content, Role: role})
	}
	openAIRequest := openai.ChatCompletionRequest{
		Model:            request.Model,
		Stop:             request.StopWords,
		MaxTokens:        request.MaxTokens,
		Temperature:      request.Temperature,
		TopP:             request.TopP,
		PresencePenalty:  request.PresencePenalty,
		FrequencyPenalty: request.FrequencyPenalty,
		Stream:           true,
		Messages:         messages,
	}
	stream, err := c.client.CreateChatCompletionStream(ctx, openAIRequest)
	if err != nil {
		return nil, fmt.Errorf("creating completion stream: %v", err)
	}
	return &ChatOpenAICompletionStreamWrapper{stream}, nil
}

func (c *OpenAIClient) CreateEmbedding(ctx context.Context, request *CreateEmbeddingRequest) ([]float32, error) {
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
