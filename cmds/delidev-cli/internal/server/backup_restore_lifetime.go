// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"sync"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Admission is serialized with shutdown. Request cancellation cannot retire a
// registered restore; explicit server shutdown cancels and joins every owner.
// The existing credential/store/lifecycle gates still serialize actual effects.
type backupRestoreLifetime struct {
	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
	closed  bool
	pending sync.WaitGroup
}

func (s *Service) initializeBackupRestores(parent context.Context) {
	s.backupRestoreOnce.Do(func() { s.backupRestores.ctx, s.backupRestores.cancel = context.WithCancel(parent) })
}

func (s *Service) admitBackupRestore(request context.Context) (context.Context, func(), error) {
	s.initializeBackupRestores(context.Background())
	lifetime := &s.backupRestores
	lifetime.mu.Lock()
	defer lifetime.mu.Unlock()
	if err := request.Err(); err != nil {
		return nil, nil, domain.SafeError(err)
	}
	if lifetime.closed || lifetime.ctx.Err() != nil || s.stopping.Load() {
		return nil, nil, domain.Fail(domain.ServerUnavailable, "The server is stopping.", "Observe the original restore receipt after an explicit restart.")
	}
	// Preserve the original authenticated principal and values, without inheriting
	// the request deadline or disconnect. This creates no new restore identity.
	ctx, cancel := context.WithCancel(context.WithoutCancel(request))
	stop := context.AfterFunc(lifetime.ctx, cancel)
	lifetime.pending.Add(1)
	return ctx, func() { stop(); cancel(); lifetime.pending.Done() }, nil
}

func (s *Service) closeBackupRestores() {
	s.initializeBackupRestores(context.Background())
	lifetime := &s.backupRestores
	lifetime.mu.Lock()
	lifetime.closed = true
	lifetime.cancel()
	lifetime.mu.Unlock()
	// Keep database, credential and server-scope owners alive until every restore
	// has returned through its original prepared/publication recovery boundary.
	lifetime.pending.Wait()
}
