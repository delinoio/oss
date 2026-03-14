package config

import (
	"log/slog"
	"os"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// Config holds runtime configuration for the DexDex worker server.
type Config struct {
	ServerAddr          string
	WorkerID            string
	MainServerURL       string
	WorktreeRoot        string
	RepoCacheRoot       string
	MaxParallelSubtasks int
	AgentExecTimeoutSec int
	AgentIdleTimeoutSec int
	SeedData            bool
}

// LoadConfig reads configuration from environment variables with defaults.
func LoadConfig(logger *slog.Logger) *Config {
	workerID := strings.TrimSpace(os.Getenv("DEXDEX_WORKER_ID"))
	if workerID == "" {
		workerID = uuid.NewString()
	}

	homeDir, _ := os.UserHomeDir()
	defaultWorktreeRoot := homeDir + "/.dexdex/worktrees"
	defaultRepoCacheRoot := homeDir + "/.dexdex/repo-cache"

	cfg := &Config{
		ServerAddr:          envOrDefault("DEXDEX_WORKER_SERVER_ADDR", "127.0.0.1:7879"),
		WorkerID:            workerID,
		MainServerURL:       envOrDefault("DEXDEX_MAIN_SERVER_URL", "http://127.0.0.1:7878"),
		WorktreeRoot:        envOrDefault("DEXDEX_WORKTREE_ROOT", defaultWorktreeRoot),
		RepoCacheRoot:       envOrDefault("DEXDEX_REPO_CACHE_ROOT", defaultRepoCacheRoot),
		MaxParallelSubtasks: envIntOrDefault("DEXDEX_MAX_PARALLEL_SUBTASKS", 3),
		AgentExecTimeoutSec: envIntOrDefault("DEXDEX_AGENT_EXEC_TIMEOUT_SEC", 1800),
		AgentIdleTimeoutSec: envIntOrDefault("DEXDEX_AGENT_IDLE_TIMEOUT_SEC", 300),
		SeedData:            strings.EqualFold(strings.TrimSpace(os.Getenv("DEXDEX_SEED_DATA")), "true"),
	}

	logger.Info("loaded configuration",
		"addr", cfg.ServerAddr,
		"worker_id", cfg.WorkerID,
		"main_server_url", cfg.MainServerURL,
		"worktree_root", cfg.WorktreeRoot,
		"repo_cache_root", cfg.RepoCacheRoot,
		"max_parallel_subtasks", cfg.MaxParallelSubtasks,
		"agent_exec_timeout_sec", cfg.AgentExecTimeoutSec,
		"agent_idle_timeout_sec", cfg.AgentIdleTimeoutSec,
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
