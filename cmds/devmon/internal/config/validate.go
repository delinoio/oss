package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/devmon/internal/contracts"
)

var kebabCasePattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

func Validate(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}

	if cfg.Version != ConfigVersionV1 {
		return fmt.Errorf("version must be %d", ConfigVersionV1)
	}

	if cfg.Daemon.MaxConcurrentJobs <= 0 {
		return fmt.Errorf("daemon.max_concurrent_jobs must be greater than zero")
	}

	if !isValidLogLevel(cfg.Daemon.LogLevel) {
		return fmt.Errorf("daemon.log_level must be one of: debug, info, warn, error")
	}

	if len(cfg.Folders) == 0 {
		return fmt.Errorf("at least one folder must be configured")
	}

	folderIDs := make(map[string]struct{}, len(cfg.Folders))
	for folderIndex := range cfg.Folders {
		folder := &cfg.Folders[folderIndex]
		folderID := strings.TrimSpace(folder.ID)
		if folderID == "" {
			return fmt.Errorf("folder[%d].id is required", folderIndex)
		}
		if !kebabCasePattern.MatchString(folderID) {
			return fmt.Errorf("folder[%d].id must be kebab-case", folderIndex)
		}
		if _, exists := folderIDs[folderID]; exists {
			return fmt.Errorf("folder id must be unique: %s", folderID)
		}
		folderIDs[folderID] = struct{}{}
		folder.ID = folderID

		folderPath := strings.TrimSpace(folder.Path)
		if folderPath == "" {
			return fmt.Errorf("folder[%s].path is required", folderID)
		}
		folder.Path = folderPath

		pathInfo, err := os.Stat(folderPath)
		if err != nil {
			return fmt.Errorf("folder[%s].path is invalid: %w", folderID, err)
		}
		if !pathInfo.IsDir() {
			return fmt.Errorf("folder[%s].path must be a directory", folderID)
		}

		if len(folder.Jobs) == 0 {
			return fmt.Errorf("folder[%s] must define at least one job", folderID)
		}

		jobIDs := make(map[string]struct{}, len(folder.Jobs))
		for jobIndex := range folder.Jobs {
			job := &folder.Jobs[jobIndex]
			jobID := strings.TrimSpace(job.ID)
			if jobID == "" {
				return fmt.Errorf("folder[%s].job[%d].id is required", folderID, jobIndex)
			}
			if !kebabCasePattern.MatchString(jobID) {
				return fmt.Errorf("folder[%s].job[%d].id must be kebab-case", folderID, jobIndex)
			}
			if _, exists := jobIDs[jobID]; exists {
				return fmt.Errorf("folder[%s] has duplicate job id: %s", folderID, jobID)
			}
			jobIDs[jobID] = struct{}{}
			job.ID = jobID

			if job.Type == "" {
				job.Type = contracts.DevmonJobTypeShellCommand
			}
			if job.Type != contracts.DevmonJobTypeShellCommand {
				return fmt.Errorf("folder[%s].job[%s].type must be %q", folderID, jobID, contracts.DevmonJobTypeShellCommand)
			}

			job.Interval = strings.TrimSpace(job.Interval)
			if job.Interval == "" {
				return fmt.Errorf("folder[%s].job[%s].interval is required", folderID, jobID)
			}
			intervalDuration, err := time.ParseDuration(job.Interval)
			if err != nil {
				return fmt.Errorf("folder[%s].job[%s].interval parse failed: %w", folderID, jobID, err)
			}
			if intervalDuration <= 0 {
				return fmt.Errorf("folder[%s].job[%s].interval must be greater than zero", folderID, jobID)
			}
			job.IntervalDuration = intervalDuration

			job.Timeout = strings.TrimSpace(job.Timeout)
			if job.Timeout == "" {
				return fmt.Errorf("folder[%s].job[%s].timeout is required", folderID, jobID)
			}
			timeoutDuration, err := time.ParseDuration(job.Timeout)
			if err != nil {
				return fmt.Errorf("folder[%s].job[%s].timeout parse failed: %w", folderID, jobID, err)
			}
			if timeoutDuration <= 0 {
				return fmt.Errorf("folder[%s].job[%s].timeout must be greater than zero", folderID, jobID)
			}
			job.TimeoutDuration = timeoutDuration

			job.Shell = strings.TrimSpace(job.Shell)
			if job.Shell == "" {
				job.Shell = "sh"
			}

			job.Script = strings.TrimSpace(job.Script)
			if job.Script == "" {
				return fmt.Errorf("folder[%s].job[%s].script is required", folderID, jobID)
			}
		}
	}

	return nil
}

func isValidLogLevel(level string) bool {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug", "info", "warn", "error":
		return true
	default:
		return false
	}
}
