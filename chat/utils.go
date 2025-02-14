package chat

import (
	"context"

	"github.com/malonaz/sgpt/embed"
	"github.com/malonaz/sgpt/internal/cli"
	"github.com/malonaz/sgpt/internal/configuration"
	"github.com/malonaz/sgpt/internal/llm"
	"github.com/malonaz/sgpt/internal/role"
)

func getEmbeddingMessages(
	ctx context.Context, config *configuration.Config, llmClient llm.Client, input string,
) ([]*llm.Message, error) {
	store, err := embed.LoadStore(config)
	if err != nil {
		return nil, err
	}
	embeddings, err := embed.Content(ctx, llmClient, input)
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
	embeddingMessages := []*llm.Message{}
	embeddingMessages = append(embeddingMessages, &llm.Message{
		Role:    llm.SystemRole,
		Content: role.EmbeddingsAugmentedAssistant,
	})
	for i := 0; i < 10 && i < len(chunks); i++ {
		chunk := chunks[i]
		cli.FileInfo("inserting chunk from file %s\n", chunk.Filename)
		embeddingMessages = append(embeddingMessages, &llm.Message{
			Role:    llm.SystemRole,
			Content: chunk.Content,
		})
	}
	return embeddingMessages, nil
}

func pipeStream(stream llm.Stream) (chan *llm.StreamEvent, chan error) {
	eventChannel := make(chan *llm.StreamEvent)
	errorChannel := make(chan error)
	go func() {
		for {
			event, err := stream.Recv()
			if err != nil {
				errorChannel <- err
				return
			}
			eventChannel <- event
		}
	}()
	return eventChannel, errorChannel
}

func dedupeStrings(strings []string) []string {
	seen := make(map[string]bool)
	result := []string{}

	for _, str := range strings {
		if !seen[str] {
			seen[str] = true
			result = append(result, str)
		}
	}
	return result
}
