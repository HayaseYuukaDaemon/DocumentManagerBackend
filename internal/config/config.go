package config

import (
	"bytes"
	"document-archive/internal/storage"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Addr                  string
	AuthToken             string
	LogLevel              slog.Level
	DefaultStorageBackend storage.StorageName
	DocumentStore         string
	SQLitePath            string
	AllowCORS             []string
	S3Endpoint            string
	S3Bucket              string
	S3Region              string
	S3AccessKeyID         string
	S3SecretAccessKey     string
	S3SessionToken        string
	S3UsePathStyle        bool
}

const configFileName = "config.yml"

type fileConfig struct {
	Addr                  *string      `yaml:"addr"`
	AuthToken             *string      `yaml:"auth_token"`
	LogLevel              *string      `yaml:"log_level"`
	DefaultStorageBackend *string      `yaml:"default_storage"`
	DocumentStore         *string      `yaml:"document_store"`
	SQLitePath            *string      `yaml:"sqlite_path"`
	AllowCORS             []string     `yaml:"allow_cors"`
	S3                    fileS3Config `yaml:"s3"`
}

type fileS3Config struct {
	Endpoint        *string `yaml:"endpoint"`
	Bucket          *string `yaml:"bucket"`
	Region          *string `yaml:"region"`
	AccessKeyID     *string `yaml:"access_key_id"`
	SecretAccessKey *string `yaml:"secret_access_key"`
	SessionToken    *string `yaml:"session_token"`
	UsePathStyle    *bool   `yaml:"use_path_style"`
}

func Load() (Config, error) {
	return load(configFileName)
}

func load(path string) (Config, error) {
	cfg := defaultConfig()

	fileCfg, err := readFileConfig(path)
	if err != nil {
		return Config{}, err
	}
	applyFileConfig(&cfg, fileCfg)
	return cfg, nil
}

func defaultConfig() Config {
	return Config{
		Addr:                  ":8080",
		LogLevel:              slog.LevelInfo,
		DefaultStorageBackend: storage.MemoryStorageName,
		DocumentStore:         "sqlite",
		SQLitePath:            "document-archive.db",
	}
}

func readFileConfig(path string) (fileConfig, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return fileConfig{}, nil
	}
	if err != nil {
		return fileConfig{}, fmt.Errorf("read %s: %w", path, err)
	}

	var cfg fileConfig
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return fileConfig{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, nil
}

func applyFileConfig(cfg *Config, fileCfg fileConfig) {
	if fileCfg.Addr != nil {
		cfg.Addr = *fileCfg.Addr
	}
	if fileCfg.AuthToken != nil {
		cfg.AuthToken = *fileCfg.AuthToken
	}
	if fileCfg.LogLevel != nil {
		cfg.LogLevel = parseLogLevel(*fileCfg.LogLevel)
	}
	if fileCfg.DefaultStorageBackend != nil {
		cfg.DefaultStorageBackend = parseStorageName(*fileCfg.DefaultStorageBackend)
	}
	if fileCfg.DocumentStore != nil {
		cfg.DocumentStore = strings.ToLower(strings.TrimSpace(*fileCfg.DocumentStore))
	}
	if fileCfg.SQLitePath != nil {
		cfg.SQLitePath = *fileCfg.SQLitePath
	}
	if fileCfg.AllowCORS != nil {
		cfg.AllowCORS = append([]string(nil), fileCfg.AllowCORS...)
	}
	if fileCfg.S3.Endpoint != nil {
		cfg.S3Endpoint = *fileCfg.S3.Endpoint
	}
	if fileCfg.S3.Bucket != nil {
		cfg.S3Bucket = *fileCfg.S3.Bucket
	}
	if fileCfg.S3.Region != nil {
		cfg.S3Region = *fileCfg.S3.Region
	}
	if fileCfg.S3.AccessKeyID != nil {
		cfg.S3AccessKeyID = *fileCfg.S3.AccessKeyID
	}
	if fileCfg.S3.SecretAccessKey != nil {
		cfg.S3SecretAccessKey = *fileCfg.S3.SecretAccessKey
	}
	if fileCfg.S3.SessionToken != nil {
		cfg.S3SessionToken = *fileCfg.S3.SessionToken
	}
	if fileCfg.S3.UsePathStyle != nil {
		cfg.S3UsePathStyle = *fileCfg.S3.UsePathStyle
	}
}

func parseStorageName(raw string) storage.StorageName {
	return storage.StorageName(strings.ToLower(strings.TrimSpace(raw)))
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
