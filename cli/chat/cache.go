package chat

import (
	"context"
	"os"
	"path/filepath"
	"time"

	aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
	aipb "github.com/malonaz/core/genproto/ai/v1"
	"github.com/malonaz/core/go/aip"
	"github.com/malonaz/core/go/grpc/middleware"
	"github.com/malonaz/core/go/pbutil"
)

const modelsCacheMaxAge = 7 * 24 * time.Hour

func modelsCachePath() string {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		cacheDir = os.TempDir()
	}
	return filepath.Join(cacheDir, "sgpt", "models_cache.json")
}

func loadModelsFromCache() ([]*aipb.Model, bool) {
	cachePath := modelsCachePath()
	info, err := os.Stat(cachePath)
	if err != nil || time.Since(info.ModTime()) > modelsCacheMaxAge {
		return nil, false
	}
	data, err := os.ReadFile(cachePath)
	if err != nil {
		return nil, false
	}
	cachedModels, err := pbutil.JSONUnmarshalSlice[aipb.Model](pbutil.JsonUnmarshalOptions, data)
	if err != nil || len(cachedModels) == 0 {
		return nil, false
	}
	return cachedModels, true
}

func saveModelsToCache(cachedModels []*aipb.Model) {
	data, err := pbutil.JSONMarshalSlice(pbutil.JsonMarshalOptions, cachedModels)
	if err != nil {
		return
	}
	cachePath := modelsCachePath()
	os.MkdirAll(filepath.Dir(cachePath), 0755)
	os.WriteFile(cachePath, data, 0644)
}

func fetchModelsWithCache(ctx context.Context, aiClient aiservicepb.AiServiceClient, forceRefresh bool) ([]*aipb.Model, error) {
	if !forceRefresh {
		if cached, ok := loadModelsFromCache(); ok {
			return cached, nil
		}
	}
	fetchedModels, err := fetchModelsFromAPI(ctx, aiClient)
	if err != nil {
		return nil, err
	}
	saveModelsToCache(fetchedModels)
	return fetchedModels, nil
}

func fetchModelsFromAPI(ctx context.Context, aiClient aiservicepb.AiServiceClient) ([]*aipb.Model, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ctx = middleware.WithFieldMask(ctx, "next_page_token,models.name,models.ttt")
	listModelsRequest := &aiservicepb.ListModelsRequest{Parent: "providers/-"}
	return aip.Paginate[*aipb.Model](ctx, listModelsRequest, aiClient.ListModels)
}
