package config

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"reflect"
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

type ObjectStorageConfig struct {
	Type storage.StorageType `yaml:"type"`
	S3   *storage.S3Config   `yaml:"s3,omitempty"`
}

type Config struct {
	Addr                 string                                      `yaml:"addr"`
	LogLevel             slog.Level                                  `yaml:"log_level"`
	DefaultStorageName   storage.StorageName                         `yaml:"default_storage"`
	DocumentStore        string                                      `yaml:"document_store"`
	SQLitePath           string                                      `yaml:"sqlite_path"`
	DeletedSweepInterval time.Duration                               `yaml:"deleted_sweep_interval"`
	AllowCORS            []string                                    `yaml:"allow_cors"`
	Storages             map[storage.StorageName]ObjectStorageConfig `yaml:"storages"`
	Roles                map[string]Role                             `yaml:"role"`
}

const configFileName = "config.yml"

func Load() (Config, error) {
	return load(configFileName)
}

func load(path string) (Config, error) {
	cfg, err := readConfig(path)
	if errors.Is(err, os.ErrNotExist) {
		cfg = defaultConfig()
		if err := writeDefaultConfig(path, cfg); err != nil {
			return Config{}, err
		}
		return readConfig(path)
	}
	return cfg, err
}

func readConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read %s: %w", path, err)
	}

	var cfg Config
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := requireConfigFields(data); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.DeletedSweepInterval < 0 {
		return Config{}, fmt.Errorf("parse %s: deleted_sweep_interval must not be negative", path)
	}
	cfg.DefaultStorageName = storage.StorageName(strings.TrimSpace(string(cfg.DefaultStorageName)))
	cfg.DocumentStore = strings.ToLower(strings.TrimSpace(cfg.DocumentStore))
	if err := normalizeAndValidateStorages(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, nil
}

func normalizeAndValidateStorages(cfg *Config) error {
	storages := make(map[storage.StorageName]ObjectStorageConfig, len(cfg.Storages))
	for rawName, storageConfig := range cfg.Storages {
		name := storage.StorageName(strings.TrimSpace(string(rawName)))
		if name == "" {
			return errors.New("storage name must not be empty")
		}
		if _, exists := storages[name]; exists {
			return fmt.Errorf("duplicate storage name after normalization: %s", name)
		}

		storageConfig.Type = storage.StorageType(strings.ToLower(strings.TrimSpace(string(storageConfig.Type))))
		switch storageConfig.Type {
		case storage.MemoryStorageType:
			if storageConfig.S3 != nil {
				return fmt.Errorf("storage %q of type memory must not contain s3 config", name)
			}
		case storage.S3StorageType:
			if storageConfig.S3 == nil {
				return fmt.Errorf("storage %q of type s3 requires s3 config", name)
			}
		default:
			return fmt.Errorf("storage %q has unsupported type %q", name, storageConfig.Type)
		}
		storages[name] = storageConfig
	}
	cfg.Storages = storages

	if cfg.DefaultStorageName == "" {
		return errors.New("default_storage must not be empty")
	}
	if _, ok := cfg.Storages[cfg.DefaultStorageName]; !ok {
		return fmt.Errorf("default storage not configured: %s", cfg.DefaultStorageName)
	}
	return nil
}

func requireConfigFields(data []byte) error {
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return err
	}
	if len(document.Content) == 0 {
		return errors.New("config must be a mapping")
	}
	return requireStructFields(document.Content[0], reflect.TypeOf(Config{}), "")
}

func requireStructFields(node *yaml.Node, typ reflect.Type, prefix string) error {
	if node.Kind != yaml.MappingNode {
		if prefix == "" {
			return errors.New("config must be a mapping")
		}
		return fmt.Errorf("config field %q must be a mapping", prefix)
	}

	values := make(map[string]*yaml.Node, len(node.Content)/2)
	for i := 0; i < len(node.Content); i += 2 {
		values[node.Content[i].Value] = node.Content[i+1]
	}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if !field.IsExported() {
			continue
		}
		tagParts := strings.Split(field.Tag.Get("yaml"), ",")
		name := tagParts[0]
		if name == "" || name == "-" {
			continue
		}
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		value, ok := values[name]
		if !ok {
			if slices.Contains(tagParts[1:], "omitempty") {
				continue
			}
			return fmt.Errorf("missing required config field %q", path)
		}
		fieldType := field.Type
		if fieldType.Kind() == reflect.Pointer {
			if value.Tag == "!!null" {
				continue
			}
			fieldType = fieldType.Elem()
		}
		if fieldType.Kind() == reflect.Struct {
			if err := requireStructFields(value, fieldType, path); err != nil {
				return err
			}
		}
		if fieldType.Kind() == reflect.Map {
			elementType := fieldType.Elem()
			if elementType.Kind() == reflect.Pointer {
				elementType = elementType.Elem()
			}
			if elementType == reflect.TypeOf(ObjectStorageConfig{}) {
				if value.Kind != yaml.MappingNode {
					return fmt.Errorf("config field %q must be a mapping", path)
				}
				for i := 0; i < len(value.Content); i += 2 {
					entryPath := path + "." + value.Content[i].Value
					if err := requireStructFields(value.Content[i+1], elementType, entryPath); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
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

// Default returns a new Config populated with the service defaults.
func Default() Config {
	return defaultConfig()
}

func defaultConfig() Config {
	return Config{
		Addr:                 ":8080",
		LogLevel:             slog.LevelInfo,
		DefaultStorageName:   storage.StorageName("memory"),
		DocumentStore:        "sqlite",
		SQLitePath:           "document-archive.db",
		DeletedSweepInterval: 24 * time.Hour,
		AllowCORS:            []string{},
		Storages: map[storage.StorageName]ObjectStorageConfig{
			"memory": {Type: storage.MemoryStorageType},
		},
		Roles: map[string]Role{},
	}
}
