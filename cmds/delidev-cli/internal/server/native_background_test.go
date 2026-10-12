// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func backgroundWorkerRequest[T any](f *publicationFixture, m *T) *connect.Request[T] {
	r := connect.NewRequest(m)
	r.Header().Set("Authorization", "Bearer "+f.workerToken)
	return r
}
func backgroundServerFixture(t *testing.T) (*publicationFixture, domain.NativeBackgroundInventory) {
	t.Helper()
	f := newPublicationFixture(t)
	f.registerGrant(t)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	_, e := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.background-capability", nil, func(tx *store.Tx) (any, error) {
		r, m, e := activeMachine(tx, f.input.MachineID)
		if e != nil {
			return nil, e
		}
		m.WorkerCapabilities = append(m.WorkerCapabilities, domain.CodexNativeBackgroundTerminalsV1)
		return tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, m)
	})
	if e != nil {
		t.Fatal(e)
	}
	i := domain.NativeBackgroundInventory{Version: 1, JobID: f.job, ExecutionID: f.input.ExecutionID, NativeThreadID: domain.NativeIdentity(f.thread), Sequence: 1, Terminals: []domain.NativeBackgroundTerminal{{ItemID: "bg-one", ProcessID: "500", Command: "original-one", Cwd: "/original", Present: true}, {ItemID: "bg-two", ProcessID: "501", Command: "original-two", Cwd: "/original", Present: true}}}
	publishBackgroundFixture(t, f, i, nil, 0)
	return f, i
}
func publishBackgroundFixture(t *testing.T, f *publicationFixture, i domain.NativeBackgroundInventory, o *domain.NativeBackgroundObservation, expected uint64) *pb.PublishNativeBackgroundResponse {
	t.Helper()
	raw, _ := json.Marshal(domain.NativeBackgroundPublication{Inventory: i, Action: o})
	r, e := f.client.PublishNativeBackground(context.Background(), backgroundWorkerRequest(f, &pb.PublishNativeBackgroundRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.SessionID), ExpectedRevision: expected}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), JobId: string(f.job), ObservationJson: raw}))
	if e != nil {
		t.Fatal(e)
	}
	return r.Msg
}
func requestBackgroundFixture(t *testing.T, f *publicationFixture, i domain.NativeBackgroundInventory, op pb.NativeBackgroundOperation, item, process string) (*pb.RequestNativeBackgroundRequest, *pb.RequestNativeBackgroundResponse) {
	t.Helper()
	client := delidevv1connect.NewSessionServiceClient(f.http.Client(), f.http.URL)
	current, e := client.GetNativeBackground(context.Background(), ownerRequest(f.service.Identity, &pb.GetNativeBackgroundRequest{SessionId: string(f.input.SessionID)}))
	if e != nil {
		t.Fatal(e)
	}
	req := &pb.RequestNativeBackgroundRequest{Mutation: acctMutation(current.Msg.Session, domain.NewID()), Operation: op, ExecutionId: string(i.ExecutionID), InventorySequence: i.Sequence, ItemId: item, ProcessId: process}
	r, e := client.RequestNativeBackground(context.Background(), ownerRequest(f.service.Identity, req))
	if e != nil {
		t.Fatal(e)
	}
	return req, r.Msg
}
func TestNativeBackgroundReceiptsOriginalClaimsAndIndependentProductTerminal(t *testing.T) {
	f, i := backgroundServerFixture(t)
	sourceRecord, e := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	if e != nil {
		t.Fatal(e)
	}
	source, _ := store.Decode[domain.Session](sourceRecord)
	sourceExecution, _ := json.Marshal(source.Execution)
	// A retained independent product terminal resource is outside native control.
	product := domain.NewID()
	var before store.Record
	_, e = f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.independent-terminal", nil, func(tx *store.Tx) (any, error) {
		var e error
		before, e = tx.Put(domain.TerminalKind, product, 0, f.input.SessionID, "", domain.Terminal{OwnerInstanceID: f.instance, MachineID: f.input.MachineID, InstanceID: f.instance, State: domain.TerminalRunning, Rows: 24, Columns: 80, Cwd: "/independent-product"})
		return before, e
	})
	if e != nil {
		t.Fatal(e)
	}
	req, accepted := requestBackgroundFixture(t, f, i, pb.NativeBackgroundOperation_NATIVE_BACKGROUND_OPERATION_TERMINATE, "bg-one", "500")
	client := delidevv1connect.NewSessionServiceClient(f.http.Client(), f.http.URL)
	replayed, e := client.RequestNativeBackground(context.Background(), ownerRequest(f.service.Identity, req))
	if e != nil || !replayed.Msg.Replayed || replayed.Msg.Action.Id != accepted.Action.Id {
		t.Fatal("receipt replay admitted another control", e)
	}
	claim := &pb.ClaimNativeBackgroundRequest{Mutation: acctMutation(accepted.Action, domain.NewID()), MachineId: string(f.input.MachineID), InstanceId: string(f.instance), JobId: string(f.job)}
	claimed, e := f.client.ClaimNativeBackground(context.Background(), backgroundWorkerRequest(f, claim))
	if e != nil {
		t.Fatal(e)
	}
	replay, e := f.client.ClaimNativeBackground(context.Background(), backgroundWorkerRequest(f, claim))
	if e != nil || !replay.Msg.Replayed {
		t.Fatal("exact claim receipt did not replay", e)
	}
	other := *claim
	other.Mutation = acctMutation(accepted.Action, domain.NewID())
	if _, e = f.client.ClaimNativeBackground(context.Background(), backgroundWorkerRequest(f, &other)); e == nil {
		t.Fatal("second claim obtained send authority")
	}
	yes := true
	i.Sequence++
	i.Terminals[0].Present = false
	published := publishBackgroundFixture(t, f, i, &domain.NativeBackgroundObservation{ActionID: domain.ID(claimed.Msg.Action.Id), Acknowledged: true, Delivery: domain.NativeBackgroundAcknowledged, Terminated: &yes}, 1)
	var session domain.Session
	if domain.Decode(published.Session.DocumentJson, &session) != nil || session.NativeBackground.PendingActionID != "" || session.NativeBackground.NativeEmpty || session.NativeBackground.Terminals[0].ExitCode != nil {
		t.Fatal("ACK became exit/cleanup or retained incorrect target")
	}
	retainedExecution, _ := json.Marshal(session.Execution)
	if string(sourceExecution) != string(retainedExecution) || source.ContextRevision != session.ContextRevision || source.NativeShell != session.NativeShell || session.CompactionJobID != "" || source.LastCompactionJobID != session.LastCompactionJobID {
		t.Fatal("background control rewrote ordinary checkpoint/context/execution")
	}
	_, clean := requestBackgroundFixture(t, f, i, pb.NativeBackgroundOperation_NATIVE_BACKGROUND_OPERATION_CLEAN, "", "")
	var a domain.NativeBackgroundAction
	if domain.Decode(clean.Action.DocumentJson, &a) != nil || len(a.Targets) != 1 || a.Targets[0].ProcessID != "501" {
		t.Fatal("clean adopted missing or product process")
	}
	var after store.Record
	e = f.service.Store.Read(context.Background(), func(tx *store.Tx) error { var e error; after, e = tx.Get(domain.TerminalKind, product); return e })
	if e != nil || before.Revision != after.Revision || string(before.Data) != string(after.Data) {
		t.Fatal("native controls modified product terminal", e)
	}
}
func TestNativeBackgroundLostClaimRetainsUncertaintyAfterOwnerCompletion(t *testing.T) {
	f, i := backgroundServerFixture(t)
	_, accepted := requestBackgroundFixture(t, f, i, pb.NativeBackgroundOperation_NATIVE_BACKGROUND_OPERATION_TERMINATE, "bg-one", "500")
	claim := &pb.ClaimNativeBackgroundRequest{Mutation: acctMutation(accepted.Action, domain.NewID()), MachineId: string(f.input.MachineID), InstanceId: string(f.instance), JobId: string(f.job)}
	if _, e := f.client.ClaimNativeBackground(context.Background(), backgroundWorkerRequest(f, claim)); e != nil {
		t.Fatal(e)
	}
	terminal := f.event(domain.ExecutionTurnFinished, 3)
	terminal.Outcome = domain.ExecutionSucceeded
	f.publish(t, terminal)
	f.reportCompletion(t, f.completion())
	client := delidevv1connect.NewSessionServiceClient(f.http.Client(), f.http.URL)
	action := accepted.Action.Id
	r, e := client.GetNativeBackground(context.Background(), ownerRequest(f.service.Identity, &pb.GetNativeBackgroundRequest{SessionId: string(f.input.SessionID), ActionId: &action}))
	if e != nil {
		t.Fatal(e)
	}
	var session domain.Session
	var retained domain.NativeBackgroundAction
	if domain.Decode(r.Msg.Session.DocumentJson, &session) != nil || domain.Decode(r.Msg.Action.DocumentJson, &retained) != nil || !session.NativeBackground.OwnerLost || retained.State != domain.NativeBackgroundUncertain || retained.Observation.Acknowledged || session.Outcome != domain.ExecutionSucceeded {
		t.Fatal("lost ACK was replayed or changed ordinary turn outcome")
	}
	if _, e = f.client.ClaimNativeBackground(context.Background(), backgroundWorkerRequest(f, claim)); e == nil {
		t.Fatal("old claim receipt reopened completed owner")
	}
	req := &pb.RequestNativeBackgroundRequest{Mutation: acctMutation(r.Msg.Session, domain.NewID()), Operation: pb.NativeBackgroundOperation_NATIVE_BACKGROUND_OPERATION_LIST, ExecutionId: string(i.ExecutionID), InventorySequence: i.Sequence}
	if _, e = client.RequestNativeBackground(context.Background(), ownerRequest(f.service.Identity, req)); e == nil {
		t.Fatal("retained inventory adopted a replacement native process")
	}
}
func TestNativeBackgroundRejectsStaleSessionExecutionAndNativeIDs(t *testing.T) {
	f, i := backgroundServerFixture(t)
	client := delidevv1connect.NewSessionServiceClient(f.http.Client(), f.http.URL)
	r, e := client.GetNativeBackground(context.Background(), ownerRequest(f.service.Identity, &pb.GetNativeBackgroundRequest{SessionId: string(f.input.SessionID)}))
	if e != nil {
		t.Fatal(e)
	}
	for _, change := range []func(*pb.RequestNativeBackgroundRequest){func(r *pb.RequestNativeBackgroundRequest) { r.ExecutionId = string(domain.NewID()) }, func(r *pb.RequestNativeBackgroundRequest) { r.InventorySequence++ }, func(r *pb.RequestNativeBackgroundRequest) { r.ProcessId = "900" }, func(r *pb.RequestNativeBackgroundRequest) { r.ItemId = "borrowed" }, func(r *pb.RequestNativeBackgroundRequest) { r.Mutation.ExpectedRevision++ }} {
		req := &pb.RequestNativeBackgroundRequest{Mutation: acctMutation(r.Msg.Session, domain.NewID()), Operation: pb.NativeBackgroundOperation_NATIVE_BACKGROUND_OPERATION_TERMINATE, ExecutionId: string(i.ExecutionID), InventorySequence: i.Sequence, ItemId: "bg-one", ProcessId: "500"}
		change(req)
		if _, e := client.RequestNativeBackground(context.Background(), ownerRequest(f.service.Identity, req)); e == nil {
			t.Fatal("stale or foreign scope admitted native control")
		}
	}
}

func TestNativeBackgroundDisconnectedOwnerCannotClaimOrHideRetainedStatus(t *testing.T) {
	f, i := backgroundServerFixture(t)
	_, accepted := requestBackgroundFixture(t, f, i, pb.NativeBackgroundOperation_NATIVE_BACKGROUND_OPERATION_TERMINATE, "bg-one", "500")
	claim := &pb.ClaimNativeBackgroundRequest{Mutation: acctMutation(accepted.Action, domain.NewID()), MachineId: string(f.input.MachineID), InstanceId: string(f.instance), JobId: string(f.job)}
	if _, e := f.client.ClaimNativeBackground(context.Background(), backgroundWorkerRequest(f, claim)); e != nil {
		t.Fatal(e)
	}
	accounts := delidevv1connect.NewAccountServiceClient(f.http.Client(), f.http.URL)
	if _, e := accounts.DisconnectAccount(context.Background(), ownerRequest(f.service.Identity, &pb.DisconnectAccountRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: 1}})); e != nil {
		t.Fatal(e)
	}
	client := delidevv1connect.NewSessionServiceClient(f.http.Client(), f.http.URL)
	id := accepted.Action.Id
	r, e := client.GetNativeBackground(context.Background(), ownerRequest(f.service.Identity, &pb.GetNativeBackgroundRequest{SessionId: string(f.input.SessionID), ActionId: &id}))
	if e != nil {
		t.Fatal(e)
	}
	var session domain.Session
	var a domain.NativeBackgroundAction
	if domain.Decode(r.Msg.Session.DocumentJson, &session) != nil || domain.Decode(r.Msg.Action.DocumentJson, &a) != nil || !session.NativeBackground.OwnerLost || a.State != domain.NativeBackgroundUncertain || a.Observation.Acknowledged {
		t.Fatal("disconnect discarded original uncertainty")
	}
	if _, e = f.client.ClaimNativeBackground(context.Background(), backgroundWorkerRequest(f, claim)); e == nil {
		t.Fatal("disconnect reopened original claim")
	}
}

func TestNativeBackgroundRootTerminalKeepsLiveChildOwnerAndAllowsJoinedClosure(t *testing.T) {
	f, i := backgroundServerFixture(t)
	// Retain an original live child; root outcome alone is not joined cleanup.
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.original-child", nil, func(tx *store.Tx) (any, error) {
		sr, session, e := sessionRecord(tx, f.input.SessionID)
		if e != nil {
			return nil, e
		}
		session.Execution.Subagents = domain.SubagentState{"original-child": {ID: domain.NewID(), ParentID: string(f.thread), Status: domain.SubagentRunning}}
		return tx.Put(sr.Kind, sr.ID, sr.Revision, sr.SessionID, sr.ProjectID, session)
	})
	if err != nil {
		t.Fatal(err)
	}
	terminal := f.event(domain.ExecutionTurnFinished, 3)
	terminal.Outcome = domain.ExecutionSucceeded
	f.publish(t, terminal)
	requestBackgroundFixture(t, f, i, pb.NativeBackgroundOperation_NATIVE_BACKGROUND_OPERATION_LIST, "", "")
	_, err = f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.joined-child", nil, func(tx *store.Tx) (any, error) {
		sr, session, e := sessionRecord(tx, f.input.SessionID)
		if e != nil {
			return nil, e
		}
		child := session.Execution.Subagents["original-child"]
		child.Status = domain.SubagentCompleted
		session.Execution.Subagents["original-child"] = child
		return tx.Put(sr.Kind, sr.ID, sr.Revision, sr.SessionID, sr.ProjectID, session)
	})
	if err != nil {
		t.Fatal(err)
	}
	i.Sequence++
	i.OwnerLost = true
	result := publishBackgroundFixture(t, f, i, nil, 1)
	var retained domain.Session
	if domain.Decode(result.Session.DocumentJson, &retained) != nil || !retained.NativeBackground.OwnerLost || retained.Execution.CleanupVerified {
		t.Fatal("observer closure was rejected or fabricated cleanup")
	}
}
