package config

import (
	"bytes"
	"document-archive/internal/storage"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
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
	S3Endpoint            string
	S3Bucket              string
	S3Region              string
	S3AccessKeyID         string
	S3SecretAccessKey     string
	S3SessionToken        string
	S3UsePathStyle        bool
}

const configFileName = "config.yml"

type configValues struct {
	Addr                  string
	AuthToken             string
	LogLevel              string
	DefaultStorageBackend string
	DocumentStore         string
	SQLitePath            string
	S3Endpoint            string
	S3Bucket              string
	S3Region              string
	S3AccessKeyID         string
	S3SecretAccessKey     string
	S3SessionToken        string
	S3UsePathStyle        bool
}

type fileConfig struct {
	Addr                  *string      `yaml:"addr"`
	AuthToken             *string      `yaml:"auth_token"`
	LogLevel              *string      `yaml:"log_level"`
	DefaultStorageBackend *string      `yaml:"default_storage"`
	DocumentStore         *string      `yaml:"document_store"`
	SQLitePath            *string      `yaml:"sqlite_path"`
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

type envLookup func(string) (string, bool)

func Load() (Config, error) {
	return load(configFileName, os.LookupEnv)
}

func load(path string, lookup envLookup) (Config, error) {
	values := defaultConfigValues()

	fileCfg, err := readFileConfig(path)
	if err != nil {
		return Config{}, err
	}
	applyFileConfig(&values, fileCfg)
	if err := applyEnvConfig(&values, lookup); err != nil {
		return Config{}, err
	}
	return values.Config(), nil
}

func defaultConfigValues() configValues {
	return configValues{
		Addr:                  ":8080",
		LogLevel:              "info",
		DefaultStorageBackend: string(storage.MemoryStorageName),
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

func applyFileConfig(values *configValues, cfg fileConfig) {
	if cfg.Addr != nil {
		values.Addr = *cfg.Addr
	}
	if cfg.AuthToken != nil {
		values.AuthToken = *cfg.AuthToken
	}
	if cfg.LogLevel != nil {
		values.LogLevel = *cfg.LogLevel
	}
	if cfg.DefaultStorageBackend != nil {
		values.DefaultStorageBackend = *cfg.DefaultStorageBackend
	}
	if cfg.DocumentStore != nil {
		values.DocumentStore = *cfg.DocumentStore
	}
	if cfg.SQLitePath != nil {
		values.SQLitePath = *cfg.SQLitePath
	}
	if cfg.S3.Endpoint != nil {
		values.S3Endpoint = *cfg.S3.Endpoint
	}
	if cfg.S3.Bucket != nil {
		values.S3Bucket = *cfg.S3.Bucket
	}
	if cfg.S3.Region != nil {
		values.S3Region = *cfg.S3.Region
	}
	if cfg.S3.AccessKeyID != nil {
		values.S3AccessKeyID = *cfg.S3.AccessKeyID
	}
	if cfg.S3.SecretAccessKey != nil {
		values.S3SecretAccessKey = *cfg.S3.SecretAccessKey
	}
	if cfg.S3.SessionToken != nil {
		values.S3SessionToken = *cfg.S3.SessionToken
	}
	if cfg.S3.UsePathStyle != nil {
		values.S3UsePathStyle = *cfg.S3.UsePathStyle
	}
}

func applyEnvConfig(values *configValues, lookup envLookup) error {
	applyStringEnv(&values.Addr, lookup, "ARCHIVE_ADDR")
	applyStringEnv(&values.AuthToken, lookup, "ARCHIVE_TOKEN")
	applyStringEnv(&values.LogLevel, lookup, "ARCHIVE_LOG_LEVEL")
	applyStringEnv(&values.DefaultStorageBackend, lookup, "ARCHIVE_DEFAULT_STORAGE")
	applyStringEnv(&values.DocumentStore, lookup, "ARCHIVE_DOCUMENT_STORE")
	applyStringEnv(&values.SQLitePath, lookup, "ARCHIVE_SQLITE_PATH")
	applyStringEnv(&values.S3Endpoint, lookup, "ARCHIVE_S3_ENDPOINT")
	applyStringEnv(&values.S3Bucket, lookup, "ARCHIVE_S3_BUCKET")
	applyStringEnv(&values.S3Region, lookup, "ARCHIVE_S3_REGION")
	applyStringEnv(&values.S3AccessKeyID, lookup, "ARCHIVE_S3_ACCESS_KEY_ID")
	applyStringEnv(&values.S3SecretAccessKey, lookup, "ARCHIVE_S3_SECRET_ACCESS_KEY")
	applyStringEnv(&values.S3SessionToken, lookup, "ARCHIVE_S3_SESSION_TOKEN")
	if raw, ok := lookup("ARCHIVE_S3_USE_PATH_STYLE"); ok {
		value, err := parseBool(raw)
		if err != nil {
			return fmt.Errorf("parse ARCHIVE_S3_USE_PATH_STYLE: %w", err)
		}
		values.S3UsePathStyle = value
	}
	return nil
}

func applyStringEnv(target *string, lookup envLookup, key string) {
	if value, ok := lookup(key); ok {
		*target = value
	}
}

func (values configValues) Config() Config {
	return Config{
		Addr:                  values.Addr,
		AuthToken:             values.AuthToken,
		LogLevel:              parseLogLevel(values.LogLevel),
		DefaultStorageBackend: storage.StorageName(strings.ToLower(strings.TrimSpace(values.DefaultStorageBackend))),
		DocumentStore:         strings.ToLower(strings.TrimSpace(values.DocumentStore)),
		SQLitePath:            values.SQLitePath,
		S3Endpoint:            values.S3Endpoint,
		S3Bucket:              values.S3Bucket,
		S3Region:              values.S3Region,
		S3AccessKeyID:         values.S3AccessKeyID,
		S3SecretAccessKey:     values.S3SecretAccessKey,
		S3SessionToken:        values.S3SessionToken,
		S3UsePathStyle:        values.S3UsePathStyle,
	}
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

func parseBool(raw string) (bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false, nil
	}
	return strconv.ParseBool(raw)
}
