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
		detail, err := store.GetSession(sessionID)
		if err != nil {
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
			continue
		}
		if detail.State == contracts.DerunSessionStateRunning || detail.State == contracts.DerunSessionStateStarting {
			continue
		}

		sessionPath := filepath.Join(root, sessionID)
		if err := os.RemoveAll(sessionPath); err != nil {
			logger.Event("cleanup_result", map[string]any{"session_id": sessionID, "cleanup_result": "error", "error": err.Error()})
			continue
		}
		result.Removed++
		logger.Event("cleanup_result", map[string]any{"session_id": sessionID, "cleanup_result": "removed"})
	}
	return result, nil
}
