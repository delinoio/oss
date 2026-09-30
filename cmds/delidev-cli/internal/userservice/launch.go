// SPDX-License-Identifier: Apache-2.0
package userservice

import (
	"context"
	"path/filepath"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// LaunchAdmission pins the registration decision against install/start/stop/remove.
// Keep it through lifecycle intent publication and native detached spawn. It never
// calls the service manager, changes registration or changes service intent.
type LaunchAdmission struct {
	root string
	lock *security.Lock
}

func AdmitLaunch(ctx context.Context, root string) (*LaunchAdmission, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, domain.SafeError(err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, domain.SafeError(err)
	}
	m := New(canonical, Server, nil)
	if err := m.check(); err != nil {
		return nil, err
	}
	child, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		lock, err := security.TryLock(m.path("-control.lock"))
		if err == nil {
			return &LaunchAdmission{root: canonical, lock: lock}, nil
		}
		if domain.SafeError(err).Code != domain.Conflict {
			return nil, err
		}
		select {
		case <-child.Done():
			return nil, domain.SafeError(child.Err())
		case <-ticker.C:
		}
	}
}

func (a *LaunchAdmission) Managed() (installed, stopped bool, err error) {
	return ManagedIntent(a.root, Server)
}
func (a *LaunchAdmission) Close() error { return a.lock.Close() }
