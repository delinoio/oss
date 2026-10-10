// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

type nativeAppsReaderFixture struct {
	calls          atomic.Int32
	inventory      domain.NativeAppInventory
	enter, release chan struct{}
}

func (f *nativeAppsReaderFixture) ReadNativeApps(ctx context.Context, scope domain.NativeAppScope, force bool) (domain.NativeAppInventory, error) {
	f.calls.Add(1)
	if f.enter != nil {
		close(f.enter)
		select {
		case <-f.release:
		case <-ctx.Done():
			return domain.NativeAppInventory{}, ctx.Err()
		}
	}
	return f.inventory, nil
}
func workerNativeAppsFixture() (domain.NativeAppScope, domain.NativeAppInventory) {
	scope := domain.NativeAppScope{SessionID: domain.NewID(), MachineID: domain.NewID(), AccountID: domain.NewID(), ConnectionID: domain.NewID(), ConfigurationDigest: strings.Repeat("a", 64)}
	return scope, domain.NativeAppInventory{Scope: scope, InventoryID: domain.NewID(), Discovered: []domain.NativeAppDiscovery{}, Installed: []domain.NativeAppInstalled{}}
}
func TestSessionNativeAppsWorkerRejectsForeignOriginalAccountAndWorkerGenerations(t *testing.T) {
	scope, inventory := workerNativeAppsFixture()
	device, instance := domain.NewID(), domain.NewID()
	registry := &nativeAppsRegistry{}
	reader := &nativeAppsReaderFixture{inventory: inventory}
	cleanup, err := registry.register(scope, device, instance, reader)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	for _, scenario := range []string{"original", "foreign-account", "foreign-session", "foreign-connection", "foreign-machine", "foreign-config", "foreign-device", "foreign-instance"} {
		t.Run(scenario, func(t *testing.T) {
			request := workspace.NativeAppsReadRequest{Scope: scope, WorkerDeviceID: device, WorkerInstanceID: instance}
			switch scenario {
			case "foreign-account":
				request.Scope.AccountID = domain.NewID()
			case "foreign-session":
				request.Scope.SessionID = domain.NewID()
			case "foreign-connection":
				request.Scope.ConnectionID = domain.NewID()
			case "foreign-machine":
				request.Scope.MachineID = domain.NewID()
			case "foreign-config":
				request.Scope.ConfigurationDigest = strings.Repeat("b", 64)
			case "foreign-device":
				request.WorkerDeviceID = domain.NewID()
			case "foreign-instance":
				request.WorkerInstanceID = domain.NewID()
			}
			before := reader.calls.Load()
			value, err := registry.read(context.Background(), request)
			if scenario == "original" {
				if err != nil || value.Scope != scope || reader.calls.Load() != before+1 {
					t.Fatal("original scope observation lost")
				}
			} else if err == nil || value.InventoryID != "" || reader.calls.Load() != before {
				t.Fatal("foreign query reached original native owner")
			}
		})
	}
	if _, err := registry.register(scope, device, instance, reader); err == nil {
		t.Fatal("replacement silently borrowed live original owner")
	}
	cleanup()
	if _, err := registry.read(context.Background(), workspace.NativeAppsReadRequest{Scope: scope, WorkerDeviceID: device, WorkerInstanceID: instance}); err == nil {
		t.Fatal("closed native owner remained callable")
	}
}
func TestSessionNativeAppsWorkerNeverPublishesForeignInventory(t *testing.T) {
	scope, inventory := workerNativeAppsFixture()
	inventory.Scope.AccountID = domain.NewID()
	registry := &nativeAppsRegistry{}
	device, instance := domain.NewID(), domain.NewID()
	cleanup, err := registry.register(scope, device, instance, &nativeAppsReaderFixture{inventory: inventory})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	value, err := registry.read(context.Background(), workspace.NativeAppsReadRequest{Scope: scope, WorkerDeviceID: device, WorkerInstanceID: instance})
	if err == nil || value.InventoryID != "" {
		t.Fatal("foreign source snapshot published")
	}
}
