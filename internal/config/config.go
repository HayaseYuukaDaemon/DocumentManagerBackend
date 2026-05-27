package config

import (
	"document-archive/internal/storage"
	"log/slog"
	"os"
	"strings"
)

type Config struct {
	Addr                  string
	AuthToken             string
	LogLevel              slog.Level
	DefaultStorageBackend storage.StorageName
}

func Load() Config {
	return Config{
		Addr:                  getenv("ARCHIVE_ADDR", ":8080"),
		AuthToken:             os.Getenv("ARCHIVE_TOKEN"),
		LogLevel:              parseLogLevel(getenv("ARCHIVE_LOG_LEVEL", "info")),
		DefaultStorageBackend: storage.StorageName(getenv("ARCHIVE_DEFAULT_STORAGE", string(storage.MemoryStorageName))),
	}
}

func getenv(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func parseLogLevel(raw string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
