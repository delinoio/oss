// SPDX-License-Identifier: Apache-2.0
package server

import (
	"connectrpc.com/connect"
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"net/http"
	"testing"
	"time"
)

func nativeAppsFixture(t *testing.T) (*continuationFixture, context.Context, *connect.ServerStreamForClient[pb.WatchWorkspaceReadsResponse]) {
	t.Helper()
	f := newContinuationFixture(t, domain.ExecutionSucceeded)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.apps-capability", nil, func(tx *store.Tx) (any, error) {
		row, m, err := activeMachine(tx, domain.ID(f.machine.Id))
		if err != nil {
			return nil, err
		}
		m.WorkerCapabilities = append(m.WorkerCapabilities, domain.SessionNativeAppsV1)
		return tx.Put(row.Kind, row.ID, row.Revision, "", "", m)
	})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := f.workerClient.WatchWorkspaceReads(ctx, ownerRequest(f.workerIdentity, &pb.WatchWorkspaceReadsRequest{MachineId: f.machine.Id, InstanceId: f.workerInstance}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stream.Close() })
	if !stream.Receive() || !stream.Msg().Heartbeat {
		t.Fatal("missing reader", stream.Err())
	}
	return f, ctx, stream
}
func TestSessionNativeAppsInventorySelectionRevocationAndReplay(t *testing.T) {
	f, ctx, stream := nativeAppsFixture(t)
	client := delidevv1connect.NewSessionNativeAppsServiceClient(http.DefaultClient, f.endpoint.URL)
	read := &pb.ReadSessionAppsRequest{RequestId: string(domain.NewID()), SessionId: string(f.input.SessionID)}
	done := make(chan *pb.ReadSessionAppsResponse, 1)
	errors := make(chan error, 1)
	go func() {
		r, err := client.ReadSessionApps(ctx, ownerRequest(f.identity, read))
		if err != nil {
			errors <- err
			return
		}
		done <- r.Msg
	}()
	if !stream.Receive() {
		t.Fatal(stream.Err())
	}
	var original workspace.ReadRequest
	if domain.Decode(stream.Msg().RequestJson, &original) != nil || original.NativeApps == nil || original.NativeApps.WorkerDeviceID != f.workerDevice || original.NativeApps.WorkerInstanceID != domain.ID(f.workerInstance) || original.NativeApps.Scope != domain.NativeAppsAssignmentScope(f.input) {
		t.Fatal("foreign observation", original)
	}
	inventory := domain.NativeAppInventory{Scope: original.NativeApps.Scope, InventoryID: domain.NewID(), Discovered: []domain.NativeAppDiscovery{{ID: "available", Name: "Available App", Accessible: true, Enabled: true}, {ID: "callable", Name: "Callable App", Accessible: true, Enabled: true}}, Installed: []domain.NativeAppInstalled{{ID: "callable", Enabled: true, Callable: true}}}
	raw, _ := json.Marshal(inventory)
	if _, err := f.workerClient.ReportWorkspaceRead(ctx, ownerRequest(f.workerIdentity, &pb.ReportWorkspaceReadRequest{MachineId: f.machine.Id, InstanceId: f.workerInstance, ReadId: string(original.ID), DocumentJson: raw})); err != nil {
		t.Fatal(err)
	}
	var result *pb.ReadSessionAppsResponse
	select {
	case result = <-done:
	case err := <-errors:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	replayedRead, readErr := client.ReadSessionApps(ctx, ownerRequest(f.identity, read))
	if readErr != nil || replayedRead.Msg.InventoryId != result.InventoryId {
		t.Fatal("inventory replay lost original result", readErr)
	}
	changedRead := *read
	changedRead.ForceRefresh = true
	if _, readErr := client.ReadSessionApps(ctx, ownerRequest(f.identity, &changedRead)); readErr == nil {
		t.Fatal("same read changed refresh semantics")
	}
	if !result.Complete || len(result.Discovered) != 2 || len(result.Installed) != 1 || !result.Installed[0].Callable || result.Selection != nil {
		t.Fatal("fabricated authority", result)
	}
	request := &pb.UpdateSessionAppsRequest{Mutation: &pb.Mutation{Id: string(f.input.SessionID), RequestId: string(domain.NewID()), ExpectedRevision: f.refresh(t).Revision}, SessionId: string(f.input.SessionID), OriginalAccountId: string(inventory.Scope.AccountID), OriginalConnectionId: string(inventory.Scope.ConnectionID), ConfigurationDigest: inventory.Scope.ConfigurationDigest, InventoryId: string(inventory.InventoryID), SelectedAppIds: []string{"available", "callable"}}
	accepted, err := client.UpdateSessionApps(ctx, ownerRequest(f.identity, request))
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Msg.Selection.Revision != 1 || len(accepted.Msg.Selection.AppIds) != 2 {
		t.Fatal("selection absent")
	}
	var projected map[string]any
	_ = json.Unmarshal(accepted.Msg.Receipt.DocumentJson, &projected)
	if projected["native_apps"] != nil || projected["native_apps_observation"] != nil {
		t.Fatal("private authority exposed")
	}
	revoke := *request
	revoke.Mutation = &pb.Mutation{Id: request.Mutation.Id, RequestId: string(domain.NewID()), ExpectedRevision: accepted.Msg.Receipt.Revision}
	revoke.SelectedAppIds = []string{}
	revoked, err := client.UpdateSessionApps(ctx, ownerRequest(f.identity, &revoke))
	if err != nil {
		t.Fatal(err)
	}
	if revoked.Msg.Selection.Revision != 2 || len(revoked.Msg.Selection.AppIds) != 0 {
		t.Fatal("revocation absent")
	}
	replay, err := client.UpdateSessionApps(ctx, ownerRequest(f.identity, request))
	if err != nil || !replay.Msg.Replayed || replay.Msg.Selection.Revision != 1 || len(replay.Msg.Selection.AppIds) != 2 {
		t.Fatal("receipt reinterpreted", err)
	}
	var current domain.SessionNativeAppSelection
	if err := f.service.Store.Read(ctx, func(tx *store.Tx) error {
		_, session, _, err := nativeAppsScope(tx, f.input.SessionID)
		if err != nil {
			return err
		}
		current = *session.NativeApps
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	frozen := domain.SessionNativeAppSelection{Scope: inventory.Scope, InventoryID: inventory.InventoryID, Revision: 1, AppIDs: []string{"available", "callable"}}
	if domain.AdmitNativeAppCall(frozen, current, inventory, "callable") == nil {
		t.Fatal("revocation lent stale call authority")
	}
	changed := *request
	changed.SelectedAppIds = []string{"callable"}
	if _, err := client.UpdateSessionApps(ctx, ownerRequest(f.identity, &changed)); err == nil {
		t.Fatal("same request changed payload")
	}
}
func TestSessionNativeAppsRejectWorkerAndUnavailableOriginalProfile(t *testing.T) {
	f := newContinuationFixture(t, domain.ExecutionSucceeded)
	client := delidevv1connect.NewSessionNativeAppsServiceClient(http.DefaultClient, f.endpoint.URL)
	request := &pb.ReadSessionAppsRequest{RequestId: string(domain.NewID()), SessionId: string(f.input.SessionID)}
	if _, err := client.ReadSessionApps(context.Background(), ownerRequest(f.workerIdentity, request)); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("worker became product client", err)
	}
	if _, err := client.ReadSessionApps(context.Background(), ownerRequest(f.identity, request)); err == nil {
		t.Fatal("unnegotiated original profile admitted")
	}
}
