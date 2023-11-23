package chat

import (
	"context"

	"github.com/sashabaranov/go-openai"

	"github.com/malonaz/sgpt/embed"
	"github.com/malonaz/sgpt/internal/cli"
	"github.com/malonaz/sgpt/internal/configuration"
	"github.com/malonaz/sgpt/internal/role"
)

func getEmbeddingMessages(
	ctx context.Context, config *configuration.Config, openAIClient *openai.Client, input string,
) ([]openai.ChatCompletionMessage, error) {
	store, err := embed.LoadStore(config)
	if err != nil {
		return nil, err
	}
	embeddings, err := embed.Content(ctx, openAIClient, input)
	if err != nil {
		return nil, err
	}
	chunks, err := store.Search(embeddings)
	if err != nil {
		return nil, err
	}
	if len(chunks) == 0 {
		return nil, nil
	}
	embeddingMessages := []openai.ChatCompletionMessage{}
	embeddingMessages = append(embeddingMessages, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleSystem,
		Content: role.EmbeddingsAugmentedAssistant,
	})
	for i := 0; i < 10 && i < len(chunks); i++ {
		chunk := chunks[i]
		cli.FileInfo("inserting chunk from file %s\n", chunk.Filename)
		embeddingMessages = append(embeddingMessages, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleSystem,
			Content: chunk.Content,
		})
	}
	return embeddingMessages, nil
}

func pipeStream(stream *openai.ChatCompletionStream) (chan string, chan error) {
	tokenChannel := make(chan string)
	errorChannel := make(chan error)
	go func() {
		for {
			response, err := stream.Recv()
			if err != nil {
				errorChannel <- err
				return
			}
			tokenChannel <- response.Choices[0].Delta.Content
		}
	}()
	return tokenChannel, errorChannel
}
