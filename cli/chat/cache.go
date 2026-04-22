package chat

import (
	"context"
	"time"

	aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/aip"
	"github.com/malonaz/core/go/grpc/middleware"

	"github.com/malonaz/sgpt/internal/cache"
)

const (
	modelsCacheKey    = "models_cache.pb"
	modelsCacheMaxAge = 24 * time.Hour
)

func fetchModelsWithCache(ctx context.Context, aiClient aiservicepb.AiServiceClient, forceRefresh bool) ([]*aipb.Model, error) {
	if !forceRefresh {
		listModelsResponse, ok := cache.Get(modelsCacheKey, modelsCacheMaxAge, &aiservicepb.ListModelsResponse{})
		if ok && len(listModelsResponse.Models) > 0 {
			return listModelsResponse.Models, nil
		}
	}
	fetchedModels, err := fetchModelsFromAPI(ctx, aiClient)
	if err != nil {
		return nil, err
	}
	listModelsResponse := &aiservicepb.ListModelsResponse{Models: fetchedModels}
	cache.Store(modelsCacheKey, listModelsResponse)
	return fetchedModels, nil
}

func fetchModelsFromAPI(ctx context.Context, aiClient aiservicepb.AiServiceClient) ([]*aipb.Model, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ctx = middleware.WithFieldMask(ctx, "next_page_token,models.name,models.ttt")
	listModelsRequest := &aiservicepb.ListModelsRequest{Parent: "providers/-"}
	return aip.Paginate[*aipb.Model](ctx, listModelsRequest, aiClient.ListModels)
}
