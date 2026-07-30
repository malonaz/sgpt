package store

import (
	"context"
	"fmt"
	"time"

	aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/aip"
	"github.com/malonaz/core/go/grpc/middleware"

	"github.com/malonaz/sgpt/internal/cache"
	"github.com/malonaz/sgpt/internal/configuration"
)

const (
	modelsCacheKey    = "models_cache.pb"
	modelsCacheMaxAge = 24 * time.Hour
)

// ListModels returns models, served from the disk cache unless stale or forceRefresh is set.
func (s *Store) ListModels(ctx context.Context, forceRefresh bool) ([]*aipb.Model, error) {
	if !forceRefresh {
		listModelsResponse, ok := cache.Get(modelsCacheKey, modelsCacheMaxAge, &aiservicepb.ListModelsResponse{})
		if ok && len(listModelsResponse.Models) > 0 {
			return listModelsResponse.Models, nil
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// Only name & ttt are needed; a strict read mask keeps the payload small.
	ctx = middleware.WithReadMaskStrict(ctx, "name,ttt")
	listModelsRequest := &aiservicepb.ListModelsRequest{Parent: "providers/-"}
	models, err := aip.Paginate[*aipb.Model](ctx, listModelsRequest, s.aiServiceClient.ListModels)
	if err != nil {
		return nil, err
	}
	cache.Store(modelsCacheKey, &aiservicepb.ListModelsResponse{Models: models})
	return models, nil
}

// ResolveModel resolves a model name or configured alias to a full model,
// refreshing the cache once if the model is not found.
func (s *Store) ResolveModel(ctx context.Context, nameOrAlias string) (*aipb.Model, error) {
	modelName, err := configuration.ResolveModelAlias(s.configuration, nameOrAlias)
	if err != nil {
		return nil, err
	}
	for _, forceRefresh := range []bool{false, true} {
		models, err := s.ListModels(ctx, forceRefresh)
		if err != nil {
			return nil, err
		}
		for _, model := range models {
			if model.Name == modelName {
				return model, nil
			}
		}
	}
	return nil, fmt.Errorf("model not found: %s", modelName)
}

// TextToTextStream opens a streaming completion against the AI service.
func (s *Store) TextToTextStream(
	ctx context.Context,
	textToTextStreamRequest *aiservicepb.TextToTextStreamRequest,
) (aiservicepb.AiService_TextToTextStreamClient, error) {
	return s.aiServiceClient.TextToTextStream(ctx, textToTextStreamRequest)
}
