package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/delinoio/oss/servers/dexdex-main-server/internal/contracts"
)

type Config struct {
	DeploymentMode    contracts.DeploymentMode
	Addr              string
	WorkerAddr        string
	SQLitePath        string
	PostgresDSN       string
	RedisAddr         string
	RedisStreamPrefix string
	AuthToken         string
}

func Load() (Config, error) {
	deploymentMode := contracts.ParseDeploymentMode(strings.TrimSpace(os.Getenv("DEXDEX_DEPLOYMENT_MODE")))
	cfg := Config{
		DeploymentMode:    deploymentMode,
		Addr:              normalizeEnv("DEXDEX_MAIN_ADDR", "127.0.0.1:7878"),
		WorkerAddr:        normalizeEnv("DEXDEX_WORKER_ADDR", "http://127.0.0.1:7879"),
		SQLitePath:        normalizeEnv("DEXDEX_SQLITE_PATH", "./.local/dexdex-main.sqlite3"),
		PostgresDSN:       strings.TrimSpace(os.Getenv("DEXDEX_POSTGRES_DSN")),
		RedisAddr:         normalizeEnv("DEXDEX_REDIS_ADDR", "127.0.0.1:6379"),
		RedisStreamPrefix: normalizeEnv("DEXDEX_REDIS_STREAM_PREFIX", "dexdex:events"),
		AuthToken:         strings.TrimSpace(os.Getenv("DEXDEX_AUTH_TOKEN")),
	}

	if cfg.DeploymentMode == contracts.DeploymentModeScale {
		if cfg.PostgresDSN == "" {
			return Config{}, fmt.Errorf("DEXDEX_POSTGRES_DSN is required when DEXDEX_DEPLOYMENT_MODE=SCALE")
		}
		if cfg.RedisAddr == "" {
			return Config{}, fmt.Errorf("DEXDEX_REDIS_ADDR is required when DEXDEX_DEPLOYMENT_MODE=SCALE")
		}
	}

	return cfg, nil
}

func normalizeEnv(key string, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}
