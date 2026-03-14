package worker

import (
	"context"
	"log/slog"
	"time"

	dexdexv1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/store"
)

// WorktreeCoordinator periodically scans for orphaned worktree assignments
// and triggers cleanup via the worker server.
type WorktreeCoordinator struct {
	dispatcher *Dispatcher
	client     *Client
	store      store.Store
	logger     *slog.Logger
	interval   time.Duration
	staleAfter time.Duration
}

// NewWorktreeCoordinator creates a new WorktreeCoordinator.
func NewWorktreeCoordinator(dispatcher *Dispatcher, client *Client, s store.Store, logger *slog.Logger) *WorktreeCoordinator {
	return &WorktreeCoordinator{
		dispatcher: dispatcher,
		client:     client,
		store:      s,
		logger:     logger,
		interval:   5 * time.Minute,
		staleAfter: 10 * time.Minute,
	}
}

// Start begins periodic stale worktree scanning in a background goroutine.
func (wc *WorktreeCoordinator) Start(ctx context.Context) {
	go wc.run(ctx)
}

func (wc *WorktreeCoordinator) run(ctx context.Context) {
	ticker := time.NewTicker(wc.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			wc.cleanupStale(ctx)
		}
	}
}

func (wc *WorktreeCoordinator) cleanupStale(ctx context.Context) {
	workspaces := wc.store.ListWorkspaces()
	for _, ws := range workspaces {
		active := wc.store.ListActiveWorktrees(ws.WorkspaceId)
		for _, assignment := range active {
			// Check if the assignment has a matching active dispatch
			wc.dispatcher.mu.RLock()
			_, hasActive := wc.dispatcher.activeSubs[assignment.SubTaskID]
			wc.dispatcher.mu.RUnlock()

			if hasActive {
				continue
			}

			// If no active dispatch and stale, trigger cleanup
			if time.Since(assignment.UpdatedAt) > wc.staleAfter {
				wc.logger.Warn("cleaning up stale worktree assignment",
					"workspace_id", assignment.WorkspaceID,
					"session_id", assignment.SessionID,
					"sub_task_id", assignment.SubTaskID,
					"state", assignment.State.String(),
					"stale_since", assignment.UpdatedAt,
				)

				// Attempt to cancel on worker (best-effort)
				if err := wc.client.CancelExecution(ctx, assignment.SessionID); err != nil {
					wc.logger.Debug("cancel stale execution (may already be done)",
						"session_id", assignment.SessionID,
						"error", err,
					)
				}

				// Mark as cleaned
				assignment.State = dexdexv1.WorktreeState_WORKTREE_STATE_CLEANED
				assignment.UpdatedAt = time.Now()
				wc.store.UpsertWorktreeAssignment(assignment.WorkspaceID, assignment)
			}
		}
	}
}
