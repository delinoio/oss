package retention

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/delinoio/oss/cmds/derun/internal/contracts"
	"github.com/delinoio/oss/cmds/derun/internal/logging"
	"github.com/delinoio/oss/cmds/derun/internal/state"
)

type Result struct {
	Checked int `json:"checked"`
	Removed int `json:"removed"`
}

type cleanupLogResult string

const (
	cleanupLogResultRemoved cleanupLogResult = "removed"
	cleanupLogResultSkipped cleanupLogResult = "skipped"
	cleanupLogResultError   cleanupLogResult = "error"
)

type cleanupLogReason string

const (
	cleanupLogReasonNotExpired           cleanupLogReason = "not_expired"
	cleanupLogReasonActiveSession        cleanupLogReason = "active_session"
	cleanupLogReasonUnreadableExpired    cleanupLogReason = "unreadable_expired"
	cleanupLogReasonUnreadableNotExpired cleanupLogReason = "unreadable_not_expired"
	cleanupLogReasonUnreadableStatErr    cleanupLogReason = "unreadable_stat_error"
	cleanupLogReasonRemoveError          cleanupLogReason = "remove_error"
)

func Sweep(store *state.Store, ttl time.Duration, logger *logging.Logger) (Result, error) {
	if ttl <= 0 {
		return Result{}, fmt.Errorf("ttl must be positive")
	}
	root := filepath.Join(store.Root(), "sessions")
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return Result{}, nil
		}
		return Result{}, fmt.Errorf("read sessions dir: %w", err)
	}

	result := Result{}
	now := time.Now().UTC()
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		result.Checked++
		sessionID := entry.Name()
		sessionPath := filepath.Join(root, sessionID)
		detail, err := store.GetSession(sessionID)
		if err != nil {
			expiresAt, statErr := unreadableSessionExpiresAt(sessionPath, ttl)
			if statErr != nil {
				logCleanupResult(logger, sessionID, cleanupLogResultError, cleanupLogReasonUnreadableStatErr, statErr)
				continue
			}
			if now.Before(expiresAt) {
				logCleanupResult(logger, sessionID, cleanupLogResultSkipped, cleanupLogReasonUnreadableNotExpired, nil)
				continue
			}
			if err := os.RemoveAll(sessionPath); err != nil {
				logCleanupResult(logger, sessionID, cleanupLogResultError, cleanupLogReasonRemoveError, err)
				continue
			}
			result.Removed++
			logCleanupResult(logger, sessionID, cleanupLogResultRemoved, cleanupLogReasonUnreadableExpired, nil)
			continue
		}

		effectiveTTL := ttl
		if detail.RetentionSeconds > 0 {
			effectiveTTL = time.Duration(detail.RetentionSeconds) * time.Second
		}

		expiresAt := detail.StartedAt.Add(effectiveTTL)
		if detail.EndedAt != nil {
			expiresAt = detail.EndedAt.Add(effectiveTTL)
		}
		if now.Before(expiresAt) {
			logCleanupResult(logger, sessionID, cleanupLogResultSkipped, cleanupLogReasonNotExpired, nil)
			continue
		}
		if detail.State == contracts.DerunSessionStateRunning || detail.State == contracts.DerunSessionStateStarting {
			logCleanupResult(logger, sessionID, cleanupLogResultSkipped, cleanupLogReasonActiveSession, nil)
			continue
		}

		if err := os.RemoveAll(sessionPath); err != nil {
			logCleanupResult(logger, sessionID, cleanupLogResultError, cleanupLogReasonRemoveError, err)
			continue
		}
		result.Removed++
		logCleanupResult(logger, sessionID, cleanupLogResultRemoved, "", nil)
	}
	return result, nil
}

func unreadableSessionExpiresAt(sessionPath string, ttl time.Duration) (time.Time, error) {
	sessionInfo, err := os.Stat(sessionPath)
	if err != nil {
		return time.Time{}, fmt.Errorf("stat unreadable session directory: %w", err)
	}
	lastTouchedAt := sessionInfo.ModTime().UTC()

	artifacts, err := os.ReadDir(sessionPath)
	if err != nil {
		return time.Time{}, fmt.Errorf("read unreadable session artifacts: %w", err)
	}
	for _, artifact := range artifacts {
		artifactPath := filepath.Join(sessionPath, artifact.Name())
		artifactInfo, err := os.Stat(artifactPath)
		if err != nil {
			return time.Time{}, fmt.Errorf("stat unreadable session artifact %q: %w", artifact.Name(), err)
		}
		artifactTouchedAt := artifactInfo.ModTime().UTC()
		if artifactTouchedAt.After(lastTouchedAt) {
			lastTouchedAt = artifactTouchedAt
		}
	}

	return lastTouchedAt.Add(ttl), nil
}

func logCleanupResult(logger *logging.Logger, sessionID string, result cleanupLogResult, reason cleanupLogReason, err error) {
	fields := map[string]any{
		"session_id":     sessionID,
		"cleanup_result": string(result),
	}
	if reason != "" {
		fields["cleanup_reason"] = string(reason)
	}
	if err != nil {
		fields["error"] = err.Error()
	}
	logger.Event("cleanup_result", fields)
}
