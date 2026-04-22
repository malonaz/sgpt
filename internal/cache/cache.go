package cache

import (
	"os"
	"path/filepath"
	"time"

	"github.com/malonaz/core/go/pbutil"
	"google.golang.org/protobuf/proto"
)

func path(key string) string {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		cacheDir = os.TempDir()
	}
	return filepath.Join(cacheDir, "sgpt", key)
}

// Get loads a cached proto message. Returns nil, false if missing or expired.
func Get[T proto.Message](key string, maxAge time.Duration, empty T) (T, bool) {
	cachePath := path(key)
	info, err := os.Stat(cachePath)
	if err != nil || time.Since(info.ModTime()) > maxAge {
		return empty, false
	}
	data, err := os.ReadFile(cachePath)
	if err != nil {
		return empty, false
	}
	if err := pbutil.Unmarshal(data, empty); err != nil {
		return empty, false
	}
	return empty, true
}

// Store writes a proto message to the cache under the given key.
func Store[T proto.Message](key string, message T) error {
	data, err := pbutil.Marshal(message)
	if err != nil {
		return err
	}
	cachePath := path(key)
	if err := os.MkdirAll(filepath.Dir(cachePath), 0755); err != nil {
		return err
	}
	return os.WriteFile(cachePath, data, 0644)
}
