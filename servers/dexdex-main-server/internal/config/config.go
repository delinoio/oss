package config

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds runtime configuration for the DexDex main server.
type Config struct {
	ServerAddr              string
	StreamRetention         int
	StreamHeartbeatInterval time.Duration
	WorkerServerURL         string
	DeploymentMode          string
	DatabaseURL             string
	RedisURL                string
	PRPollIntervalSec       int
	SeedData                bool
}

// LoadConfig reads configuration from environment variables with defaults.
func LoadConfig(logger *slog.Logger) *Config {
	cfg := &Config{
		ServerAddr:              envOrDefault("DEXDEX_MAIN_SERVER_ADDR", "127.0.0.1:7878"),
		StreamRetention:         envIntOrDefault("DEXDEX_MAIN_STREAM_RETENTION", 1000),
		StreamHeartbeatInterval: envDurationOrDefault("DEXDEX_MAIN_STREAM_HEARTBEAT_INTERVAL", 30*time.Second),
		WorkerServerURL:         envOrDefault("DEXDEX_WORKER_SERVER_URL", "http://127.0.0.1:7879"),
		DeploymentMode:          envOrDefault("DEXDEX_DEPLOYMENT_MODE", "SINGLE_INSTANCE"),
		DatabaseURL:             strings.TrimSpace(os.Getenv("DEXDEX_DATABASE_URL")),
		RedisURL:                strings.TrimSpace(os.Getenv("DEXDEX_REDIS_URL")),
		PRPollIntervalSec:       envIntOrDefault("DEXDEX_PR_POLL_INTERVAL_SEC", 60),
		SeedData:                strings.EqualFold(strings.TrimSpace(os.Getenv("DEXDEX_SEED_DATA")), "true"),
	}

	// Validate deployment mode
	if cfg.DeploymentMode != "SINGLE_INSTANCE" && cfg.DeploymentMode != "SCALE" {
		logger.Warn("invalid DEXDEX_DEPLOYMENT_MODE, defaulting to SINGLE_INSTANCE",
			"value", cfg.DeploymentMode)
		cfg.DeploymentMode = "SINGLE_INSTANCE"
	}

	if cfg.DeploymentMode == "SCALE" && cfg.RedisURL == "" {
		logger.Warn("DEXDEX_REDIS_URL is required for SCALE deployment mode")
	}

	logger.Info("loaded configuration",
		"addr", cfg.ServerAddr,
		"stream_retention", cfg.StreamRetention,
		"heartbeat_interval", cfg.StreamHeartbeatInterval,
		"worker_url", cfg.WorkerServerURL,
		"deployment_mode", cfg.DeploymentMode,
		"database_configured", cfg.DatabaseURL != "",
		"redis_configured", cfg.RedisURL != "",
		"pr_poll_interval_sec", cfg.PRPollIntervalSec,
		"seed_data", cfg.SeedData,
	)

	return cfg
}

func envOrDefault(key, defaultVal string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return defaultVal
	}
	return v
}

func envIntOrDefault(key string, defaultVal int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return defaultVal
	}
	return n
}

func envDurationOrDefault(key string, defaultVal time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return defaultVal
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return defaultVal
	}
	return d
}
