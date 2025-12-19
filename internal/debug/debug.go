package debug

import (
	"log/slog"
	"os"
	"sync"
)

var (
	once   sync.Once
	logger *slog.Logger
)

// GetLogger returns a singleton slog logger instance
func GetLogger() *slog.Logger {
	once.Do(func() {
		f, err := os.OpenFile("/tmp/sgpt-debug.log", os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
		if err != nil {
			panic(err)
		}
		logger = slog.New(slog.NewTextHandler(f, &slog.HandlerOptions{
			Level:     slog.LevelDebug,
			AddSource: true,
		}))
	})
	return logger
}
