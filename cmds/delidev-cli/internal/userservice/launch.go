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
	kind Kind
	lock *security.Lock
}

func AdmitLaunch(ctx context.Context, root string) (*LaunchAdmission, error) {
	return AdmitKindLaunch(ctx, root, Server)
}

// AdmitKindLaunch shares the original service-control gate without granting
// manager control. Desktop Workers must not compete with installed services.
func AdmitKindLaunch(ctx context.Context, root string, kind Kind) (*LaunchAdmission, error) {
	if !kind.Valid() {
		return nil, domain.Fail(domain.InvalidArgument, "Invalid service kind.", "Select server or worker.")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, domain.SafeError(err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, domain.SafeError(err)
	}
	m := New(canonical, kind, nil)
	if err := m.check(); err != nil {
		return nil, err
	}
	child, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := child.Err(); err != nil {
			return nil, domain.SafeError(err)
		}
		lock, err := security.TryLock(m.path("-control.lock"))
		if err == nil {
			if err := child.Err(); err != nil {
				lock.Close()
				return nil, domain.SafeError(err)
			}
			return &LaunchAdmission{root: canonical, kind: kind, lock: lock}, nil
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
	return ManagedIntent(a.root, a.kind)
}
func (a *LaunchAdmission) Close() error { return a.lock.Close() }
