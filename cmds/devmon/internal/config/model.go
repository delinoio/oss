package config

import (
	"time"

	"github.com/delinoio/oss/cmds/devmon/internal/contracts"
)

const (
	ConfigVersionV1 = 1
)

type Config struct {
	Version int
	Daemon  DaemonConfig
	Folders []FolderConfig
}

type DaemonConfig struct {
	MaxConcurrentJobs int
	StartupRun        bool
	LogLevel          string
}

type FolderConfig struct {
	ID   string
	Path string
	Jobs []JobConfig
}

type JobConfig struct {
	ID         string
	Type       contracts.DevmonJobType
	Enabled    bool
	Interval   string
	Timeout    string
	Shell      string
	Script     string
	StartupRun *bool

	IntervalDuration time.Duration
	TimeoutDuration  time.Duration
}

func defaultConfig() Config {
	return Config{
		Daemon: DaemonConfig{
			MaxConcurrentJobs: 1,
			StartupRun:        true,
			LogLevel:          "info",
		},
	}
}

func defaultFolderConfig() FolderConfig {
	return FolderConfig{}
}

func defaultJobConfig() JobConfig {
	return JobConfig{
		Type:    contracts.DevmonJobTypeShellCommand,
		Enabled: true,
		Shell:   "sh",
	}
}
