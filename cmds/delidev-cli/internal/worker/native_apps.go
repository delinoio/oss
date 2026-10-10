// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"sync"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

type nativeAppsReader interface {
	ReadNativeApps(context.Context, domain.NativeAppScope, bool) (domain.NativeAppInventory, error)
}
type nativeAppsOwner struct {
	mu               sync.Mutex
	closed           bool
	scope            domain.NativeAppScope
	device, instance domain.ID
	reader           nativeAppsReader
}
type nativeAppsRegistry struct {
	mu     sync.Mutex
	owners map[domain.ID]*nativeAppsOwner
}

func (r *nativeAppsRegistry) register(scope domain.NativeAppScope, device, instance domain.ID, reader nativeAppsReader) (func(), error) {
	if r == nil || scope.Validate() != nil || device.Validate() != nil || instance.Validate() != nil || reader == nil {
		return nil, domain.NativeAppsUnavailable()
	}
	owner := &nativeAppsOwner{scope: scope, device: device, instance: instance, reader: reader}
	r.mu.Lock()
	if r.owners == nil {
		r.owners = map[domain.ID]*nativeAppsOwner{}
	}
	if r.owners[scope.SessionID] != nil {
		r.mu.Unlock()
		return nil, domain.NativeAppsUnavailable()
	}
	r.owners[scope.SessionID] = owner
	r.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			if r.owners[scope.SessionID] == owner {
				delete(r.owners, scope.SessionID)
			}
			r.mu.Unlock()
			// Join in-progress observations before native process/authentication cleanup.
			owner.mu.Lock()
			owner.closed = true
			owner.reader = nil
			owner.mu.Unlock()
		})
	}, nil
}
func (r *nativeAppsRegistry) read(ctx context.Context, request workspace.NativeAppsReadRequest) (domain.NativeAppInventory, error) {
	if r == nil || request.Validate() != nil {
		return domain.NativeAppInventory{}, domain.NativeAppsUnavailable()
	}
	r.mu.Lock()
	owner := r.owners[request.Scope.SessionID]
	r.mu.Unlock()
	if owner == nil {
		return domain.NativeAppInventory{}, domain.NativeAppsUnavailable()
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if owner.closed || owner.reader == nil || owner.scope != request.Scope || owner.device != request.WorkerDeviceID || owner.instance != request.WorkerInstanceID {
		return domain.NativeAppInventory{}, domain.NativeAppsUnavailable()
	}
	result, err := owner.reader.ReadNativeApps(ctx, owner.scope, request.ForceRefresh)
	if err != nil || result.Validate() != nil || result.Scope != owner.scope {
		return domain.NativeAppInventory{}, domain.NativeAppsUnavailable()
	}
	return result, nil
}
