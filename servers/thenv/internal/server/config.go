package server

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

const (
	envListenAddr           = "THENV_LISTEN_ADDR"
	envDatabasePath         = "THENV_DB_PATH"
	envJWTSecret            = "THENV_JWT_SECRET"
	envWorkspaceMasterKeys  = "THENV_WORKSPACE_MASTER_KEYS"
	envDefaultMasterKey     = "THENV_DEFAULT_MASTER_KEY"
	envSuperAdmins          = "THENV_SUPER_ADMINS"
	envLogLevel             = "THENV_LOG_LEVEL"
	defaultListenAddr       = ":8080"
	defaultDatabaseFilePath = "servers/thenv/data/thenv.db"
)

type Config struct {
	ListenAddr    string
	DatabasePath  string
	JWTSecret     []byte
	WorkspaceKeys map[string][]byte
	DefaultKey    []byte
	SuperAdmins   map[string]struct{}
	LogLevel      slog.Level
}

func LoadConfig() (Config, error) {
	cfg := Config{
		ListenAddr:    defaultValue(os.Getenv(envListenAddr), defaultListenAddr),
		DatabasePath:  defaultValue(os.Getenv(envDatabasePath), defaultDatabaseFilePath),
		WorkspaceKeys: map[string][]byte{},
		SuperAdmins:   map[string]struct{}{},
		LogLevel:      parseLogLevel(os.Getenv(envLogLevel)),
	}

	jwtSecret := strings.TrimSpace(os.Getenv(envJWTSecret))
	if jwtSecret == "" {
		return Config{}, fmt.Errorf("%s is required", envJWTSecret)
	}
	cfg.JWTSecret = []byte(jwtSecret)

	workspaceKeys, err := parseWorkspaceKeys(os.Getenv(envWorkspaceMasterKeys))
	if err != nil {
		return Config{}, err
	}
	cfg.WorkspaceKeys = workspaceKeys

	defaultKey := strings.TrimSpace(os.Getenv(envDefaultMasterKey))
	if defaultKey != "" {
		cfg.DefaultKey = deriveMasterKey(defaultKey)
	}

	for _, subject := range strings.Split(os.Getenv(envSuperAdmins), ",") {
		subject = strings.TrimSpace(subject)
		if subject == "" {
			continue
		}
		cfg.SuperAdmins[subject] = struct{}{}
	}

	if err := ensureDatabaseDir(cfg.DatabasePath); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func ensureDatabaseDir(path string) error {
	dir := filepath.Dir(path)
	if dir == "." {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create database directory: %w", err)
	}
	return nil
}

func parseWorkspaceKeys(raw string) (map[string][]byte, error) {
	keys := map[string][]byte{}
	if strings.TrimSpace(raw) == "" {
		return keys, nil
	}
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		parts := strings.SplitN(pair, ":", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid workspace key pair: %s", pair)
		}
		workspaceID := strings.TrimSpace(parts[0])
		keyMaterial := strings.TrimSpace(parts[1])
		if workspaceID == "" || keyMaterial == "" {
			return nil, errors.New("workspace key pairs must include workspace id and key")
		}
		keys[workspaceID] = deriveMasterKey(keyMaterial)
	}
	return keys, nil
}

func deriveMasterKey(value string) []byte {
	if decoded, err := hex.DecodeString(value); err == nil && len(decoded) >= 32 {
		sum := sha256.Sum256(decoded)
		return sum[:]
	}
	sum := sha256.Sum256([]byte(value))
	return sum[:]
}

func parseLogLevel(raw string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func defaultValue(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
