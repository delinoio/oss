package store

import (
	"context"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// File operations share ownership with Close, which must wait for cleanup.
// Request/job waiters must still honor their deadlines while another image is
// being copied. TryLock keeps cancellation independent of the current owner.
func lockBackupContext(ctx context.Context, lock *sync.Mutex) error {
	if err := ctx.Err(); err != nil {
		return domain.SafeError(err)
	}
	if lock.TryLock() {
		return nil
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return domain.SafeError(ctx.Err())
		case <-ticker.C:
			if err := ctx.Err(); err != nil {
				return domain.SafeError(err)
			}
			if lock.TryLock() {
				return nil
			}
		}
	}
}
