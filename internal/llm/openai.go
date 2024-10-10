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

type CompletionStreamWrapper struct {
	stream *openai.CompletionStream
}

func (s *CompletionStreamWrapper) Close() { s.stream.Close() }
func (s *CompletionStreamWrapper) Recv() (*StreamEvent, error) {
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

type ChatCompletionStreamWrapper struct {
	stream *openai.ChatCompletionStream
}

func (s *ChatCompletionStreamWrapper) Close() { s.stream.Close() }
func (s *ChatCompletionStreamWrapper) Recv() (*StreamEvent, error) {
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
		messages = append(messages, openai.ChatCompletionMessage{Content: message.Content, Role: message.Role})
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
	return &ChatCompletionStreamWrapper{stream}, nil
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
