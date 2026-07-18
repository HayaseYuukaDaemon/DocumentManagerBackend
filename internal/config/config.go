package config

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"
	"time"

	"document-archive/internal/storage"

	"gopkg.in/yaml.v3"
)

type Permissions string

const (
	DocumentCreate  Permissions = "document:create"
	DocumentUpdate  Permissions = "document:update"
	DocumentDelete  Permissions = "document:delete"
	DocumentRead    Permissions = "document:read"
	DocumentRefresh Permissions = "document:refresh"
)

type Role struct {
	Name        string        `yaml:"name"`
	Permissions []Permissions `yaml:"permissions"`
	Admin       bool          `yaml:"admin"`
}

func (r Role) HasPermission(permission Permissions) bool {
	if r.Admin {
		return true
	}
	return slices.Contains(r.Permissions, permission)
}

type S3Config struct {
	Endpoint        string `yaml:"endpoint"`
	Bucket          string `yaml:"bucket"`
	Region          string `yaml:"region"`
	AccessKeyID     string `yaml:"access_key_id"`
	SecretAccessKey string `yaml:"secret_access_key"`
	SessionToken    string `yaml:"session_token"`
	UsePathStyle    bool   `yaml:"use_path_style"`
}

type Config struct {
	Addr                  string              `yaml:"addr"`
	LogLevel              slog.Level          `yaml:"log_level"`
	DefaultStorageBackend storage.StorageName `yaml:"default_storage"`
	DocumentStore         string              `yaml:"document_store"`
	SQLitePath            string              `yaml:"sqlite_path"`
	DeletedSweepInterval  time.Duration       `yaml:"deleted_sweep_interval"`
	AllowCORS             []string            `yaml:"allow_cors"`
	S3                    S3Config            `yaml:"s3"`
	Roles                 map[string]Role     `yaml:"role"`
}

type fileConfig struct {
	Addr                  *string         `yaml:"addr"`
	LogLevel              *string         `yaml:"log_level"`
	DefaultStorageBackend *string         `yaml:"default_storage"`
	DocumentStore         *string         `yaml:"document_store"`
	SQLitePath            *string         `yaml:"sqlite_path"`
	DeletedSweepInterval  *string         `yaml:"deleted_sweep_interval"`
	AllowCORS             []string        `yaml:"allow_cors"`
	S3                    fileS3Config    `yaml:"s3"`
	Roles                 map[string]Role `yaml:"role"`
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

const configFileName = "config.yml"

func Load() (Config, error) {
	return load(configFileName)
}

func load(path string) (Config, error) {
	cfg := defaultConfig()

	fileCfg, err := readFileConfig(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := writeDefaultConfig(path, cfg); err != nil {
			return Config{}, err
		}
		fileCfg, err = readFileConfig(path)
	}
	if err != nil {
		return Config{}, err
	}
	if err := applyFileConfig(&cfg, fileCfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func readFileConfig(path string) (fileConfig, error) {
	data, err := os.ReadFile(path)
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

func writeDefaultConfig(path string, cfg Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal default config: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func defaultConfig() Config {
	return Config{
		Addr:                  ":8080",
		LogLevel:              slog.LevelInfo,
		DefaultStorageBackend: storage.MemoryStorageName,
		DocumentStore:         "sqlite",
		SQLitePath:            "document-archive.db",
		DeletedSweepInterval:  24 * time.Hour,
	}
}

func applyFileConfig(cfg *Config, fileCfg fileConfig) error {
	if fileCfg.Addr != nil {
		cfg.Addr = *fileCfg.Addr
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
	if fileCfg.DeletedSweepInterval != nil {
		interval, err := parseDuration(*fileCfg.DeletedSweepInterval)
		if err != nil {
			return fmt.Errorf("parse deleted_sweep_interval: %w", err)
		}
		cfg.DeletedSweepInterval = interval
	}
	if fileCfg.AllowCORS != nil {
		cfg.AllowCORS = append([]string(nil), fileCfg.AllowCORS...)
	}
	if fileCfg.S3.Endpoint != nil {
		cfg.S3.Endpoint = *fileCfg.S3.Endpoint
	}
	if fileCfg.S3.Bucket != nil {
		cfg.S3.Bucket = *fileCfg.S3.Bucket
	}
	if fileCfg.S3.Region != nil {
		cfg.S3.Region = *fileCfg.S3.Region
	}
	if fileCfg.S3.AccessKeyID != nil {
		cfg.S3.AccessKeyID = *fileCfg.S3.AccessKeyID
	}
	if fileCfg.S3.SecretAccessKey != nil {
		cfg.S3.SecretAccessKey = *fileCfg.S3.SecretAccessKey
	}
	if fileCfg.S3.SessionToken != nil {
		cfg.S3.SessionToken = *fileCfg.S3.SessionToken
	}
	if fileCfg.S3.UsePathStyle != nil {
		cfg.S3.UsePathStyle = *fileCfg.S3.UsePathStyle
	}
	if fileCfg.Roles != nil {
		cfg.Roles = make(map[string]Role, len(fileCfg.Roles))
		for token, role := range fileCfg.Roles {
			role.Permissions = append([]Permissions(nil), role.Permissions...)
			cfg.Roles[token] = role
		}
	}
	return nil
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

func parseDuration(raw string) (time.Duration, error) {
	value, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil {
		return 0, err
	}
	if value < 0 {
		return 0, errors.New("duration must not be negative")
	}
	return value, nil
}
