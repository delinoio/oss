package config

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Addr           string
	CodexBin       string
	CodexProfile   string
	MaxRetry       int
	RetryBackoffMS int
	AuthToken      string
}

func Load() Config {
	return Config{
		Addr:           normalizeEnv("DEXDEX_WORKER_ADDR", "127.0.0.1:7879"),
		CodexBin:       normalizeEnv("DEXDEX_CODEX_BIN", "codex"),
		CodexProfile:   strings.TrimSpace(os.Getenv("DEXDEX_CODEX_PROFILE")),
		MaxRetry:       normalizeEnvInt("DEXDEX_WORKER_MAX_RETRY", 3),
		RetryBackoffMS: normalizeEnvInt("DEXDEX_WORKER_RETRY_BACKOFF_MS", 600),
		AuthToken:      strings.TrimSpace(os.Getenv("DEXDEX_AUTH_TOKEN")),
	}
}

func normalizeEnv(key string, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func normalizeEnvInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}
