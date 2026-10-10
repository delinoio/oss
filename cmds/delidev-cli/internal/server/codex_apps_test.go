// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestCodexAppsNegativeCatalogNeedsOriginalFullRefresh(t *testing.T) {
	original := domain.CodexAppConfiguration{Version: 1, SessionID: domain.NewID(), AccountID: domain.NewID(), Generation: domain.NewID(), AppIDs: []string{"selected-original", "keep"}}
	next := original.Clone()
	next.Generation = domain.NewID()
	next.AppIDs = []string{"keep"}
	inventory := domain.CodexAppsInventory{Version: 1, OperationID: domain.NewID(), ClaimID: domain.NewID(), NativeCatalogRefreshVerified: true, SessionID: original.SessionID, AccountID: original.AccountID, ConfigurationGeneration: next.Generation, ExecutionID: domain.NewID(), ExecutionJobID: domain.NewID(), MachineID: domain.NewID(), InstanceID: domain.NewID(), NativeThreadID: "original-native-thread", ObservedAt: time.Now().UTC(), Apps: []domain.CodexApp{{ID: "selected-original", Name: "Original", Discovered: true, Accessible: true, Installed: true}, {ID: "keep", Name: "Keep", Selected: true, Enabled: true, Installed: true, Callable: true}}}
	if !codexAppsNegativeInventory(original, next, inventory) {
		t.Fatal("verified negative refresh denied")
	}
	inventory.Apps[0].Callable = true
	inventory.Apps[0].Enabled = true
	if codexAppsNegativeInventory(original, next, inventory) {
		t.Fatal("removed app remained callable")
	}
	inventory.Apps = inventory.Apps[1:]
	if !codexAppsNegativeInventory(original, next, inventory) {
		t.Fatal("full original catalog absence denied")
	}
	inventory.NativeCatalogRefreshVerified = false
	if codexAppsNegativeInventory(original, next, inventory) {
		t.Fatal("absence inferred fresh native proof")
	}
}
func TestCodexAppsSelectionUsesAvailabilityWithoutInferringCallability(t *testing.T) {
	configuration := domain.CodexAppConfiguration{Version: 1, SessionID: domain.NewID(), AccountID: domain.NewID(), Generation: domain.NewID(), AppIDs: []string{}}
	inventory := domain.CodexAppsInventory{Version: 1, OperationID: domain.NewID(), ClaimID: domain.NewID(), NativeCatalogRefreshVerified: true, SessionID: configuration.SessionID, AccountID: configuration.AccountID, ConfigurationGeneration: configuration.Generation, ExecutionID: domain.NewID(), ExecutionJobID: domain.NewID(), MachineID: domain.NewID(), InstanceID: domain.NewID(), NativeThreadID: "original-native-thread", ObservedAt: time.Now().UTC(), Apps: []domain.CodexApp{{ID: "available", Name: "Native label", Discovered: true, Accessible: true, Installed: true}}}
	value := &store.CodexAppsSnapshot{Configuration: &configuration, Inventory: &inventory}
	if !codexAppsAvailableSelection(value, []string{"available"}) {
		t.Fatal("available noncallable app cannot be explicitly selected")
	}
	if codexAppsAvailableSelection(value, []string{"foreign"}) {
		t.Fatal("invented identity admitted")
	}
	inventory.AccountID = domain.NewID()
	if codexAppsAvailableSelection(value, []string{"available"}) {
		t.Fatal("foreign catalog transferred")
	}
	if !codexAppsAvailableSelection(nil, []string{}) || codexAppsAvailableSelection(nil, []string{"available"}) {
		t.Fatal("absent inventory inferred authority")
	}
}
func TestCodexAppsDedicatedResourcesAndAbsentMetadata(t *testing.T) {
	response, err := codexAppsResponse(nil, false)
	if err != nil || response.Configuration != nil || response.Operation != nil || response.Inventory != nil {
		t.Fatal(response, err)
	}
	configuration := domain.CodexAppConfiguration{Version: 1, SessionID: domain.NewID(), AccountID: domain.NewID(), Generation: domain.NewID(), AppIDs: []string{}}
	response, err = codexAppsResponse(&store.CodexAppsSnapshot{Revision: 9, SessionID: configuration.SessionID, Configuration: &configuration}, true)
	if err != nil {
		t.Fatal(err)
	}
	resource := response.Configuration
	if resource.Kind != pb.EntityKind_ENTITY_KIND_UNSPECIFIED || resource.SchemaVersion != 1 || resource.Id != string(configuration.Generation) || resource.SessionId != string(configuration.SessionID) || resource.Revision != 9 || !response.Replayed {
		t.Fatal(resource)
	}
	var decoded domain.CodexAppConfiguration
	if json.Unmarshal(resource.DocumentJson, &decoded) != nil || decoded.Validate() != nil {
		t.Fatal(string(resource.DocumentJson))
	}
}
func TestCodexAppsRejectsMissingForeignAndWorkerProductMutations(t *testing.T) {
	for _, actor := range []domain.Principal{{Type: domain.WorkerDevice, DeviceID: domain.NewID(), MachineID: domain.NewID()}, {Type: domain.ClientDevice, DeviceID: domain.NewID()}} {
		ctx := domain.WithPrincipal(context.Background(), actor)
		if _, err := codexAppsMutation(ctx, nil, string(domain.NewID()), "", 1, nil, domain.CodexAppsInspect); err == nil {
			t.Fatal("missing mutation admitted")
		}
	}
	service := &Service{}
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.WorkerDevice, DeviceID: domain.NewID(), MachineID: domain.NewID()})
	if _, err := service.GetCodexApps(ctx, connect.NewRequest(&pb.GetCodexAppsRequest{SessionId: string(domain.NewID())})); err == nil {
		t.Fatal("Worker admitted product read")
	}
}
