// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"testing"
)

func journalFixture(t *testing.T) (*firstDispatchFixture, context.Context, domain.SkillReadRequest, domain.ID, json.RawMessage) {
	t.Helper()
	f := newFirstDispatchFixture(t)
	id := domain.NewID()
	raw := json.RawMessage(`{"fixture":"original"}`)
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	s := domain.SkillReadRequest{MachineID: domain.ID(f.machine.Id), AgentID: domain.ID(f.agent.Id), ActorID: f.service.Identity.ServerID, WorkerDeviceID: f.workerDevice, WorkerInstanceID: domain.ID(f.workerInstance), AgentRevision: 1, Selections: []domain.SkillBinding{{InventoryID: domain.NewID(), SkillID: domain.NewID(), ContentRevision: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", SnapshotID: id, WorkerDeviceID: f.workerDevice}}}
	liveSkillPreparations.Lock()
	liveSkillPreparations.owners[f.service.Store.Root()] = map[domain.ID]bool{id: true}
	liveSkillPreparations.Unlock()
	t.Cleanup(func() { f.service.finishSkillPreparation(id) })
	owner := skillPreparationOwner{RequestID: id, Operation: "session.create", Identity: raw, Actor: domain.Principal{Type: domain.OwnerDevice}}
	if e := f.service.retainSkillPreparation(context.WithValue(ctx, skillPreparationContextKey{}, owner), &s); e != nil {
		t.Fatal(e)
	}
	return f, ctx, s, id, raw
}
func TestPreparationJournalBeforeDispatchAndOriginalMutationJoin(t *testing.T) {
	f, ctx, s, id, _ := journalFixture(t)
	v, e := f.service.retainedSkillPreparation(id)
	if e != nil || v.State != skillPreparationPending || domain.ValidateSkillPreparation(v.Scope) != nil || v.Scope.Preparation.ServerID != f.service.Identity.ServerID {
		t.Fatal(v, e)
	}
	if e = f.service.reconcileSkillPreparations(ctx); e != nil {
		t.Fatal(e)
	}
	v, _ = f.service.retainedSkillPreparation(id)
	if v.State != skillPreparationPending {
		t.Fatal("cleanup crossed original mutation", v.State)
	}
	f.service.finishSkillPreparation(id)
	if e = f.service.reconcileSkillPreparations(ctx); e != nil {
		t.Fatal(e)
	}
	v, _ = f.service.retainedSkillPreparation(id)
	if v.State != skillPreparationCleaning || v.Scope.WorkerDeviceID != s.WorkerDeviceID || v.Scope.Preparation.ScopeDigest != s.Preparation.ScopeDigest {
		t.Fatal("offline cleanup lost original ownership", v)
	}
}
func TestPreparationReceiptRetainsAcceptedCopies(t *testing.T) {
	f, ctx, _, id, raw := journalFixture(t)
	if _, e := f.service.Store.Mutate(ctx, id, "session.create", raw, func(*store.Tx) (any, error) { return struct{ Accepted bool }{true}, nil }); e != nil {
		t.Fatal(e)
	}
	f.service.finishSkillPreparation(id)
	if e := f.service.reconcileSkillPreparations(ctx); e != nil {
		t.Fatal(e)
	}
	v, e := f.service.retainedSkillPreparation(id)
	if e != nil || v.State != skillPreparationAccepted {
		t.Fatal(v, e)
	}
}
func TestPreparationConflictingReceiptKeepsUncertainOwnership(t *testing.T) {
	f, ctx, _, id, _ := journalFixture(t)
	if _, e := f.service.Store.Mutate(ctx, id, "session.create", map[string]string{"fixture": "different"}, func(*store.Tx) (any, error) { return true, nil }); e != nil {
		t.Fatal(e)
	}
	f.service.finishSkillPreparation(id)
	if e := f.service.reconcileSkillPreparations(ctx); e != nil {
		t.Fatal(e)
	}
	v, e := f.service.retainedSkillPreparation(id)
	if e != nil || v.State != skillPreparationPending {
		t.Fatal("unknown receipt dispatched cleanup", v, e)
	}
}
func TestPreparationRejectedConcurrentRetryCannotFinishOriginalOwner(t *testing.T) {
	f, ctx, s, id, raw := journalFixture(t)
	_, finish, e := f.service.prepareSkills(ctx, s, id, "session.create", raw)
	if e == nil {
		t.Fatal("duplicate active request admitted")
	}
	finish()
	liveSkillPreparations.Lock()
	active := liveSkillPreparations.owners[f.service.Store.Root()][id]
	liveSkillPreparations.Unlock()
	if !active {
		t.Fatal("rejected handler released original owner")
	}
}

func TestPreparationCanceledReportLossReconcilesOriginalReadLane(t *testing.T) {
	f, ctx, stream := skillReaderFixture(t)
	id := domain.NewID()
	scope := domain.SkillReadRequest{MachineID: domain.ID(f.machine.Id), AgentID: domain.ID(f.agent.Id), Selections: []domain.SkillBinding{{InventoryID: domain.NewID(), SkillID: domain.NewID(), ContentRevision: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", SnapshotID: id, WorkerDeviceID: f.workerDevice}}}
	callctx, cancel := context.WithCancel(domain.WithPrincipal(ctx, domain.Principal{Type: domain.OwnerDevice}))
	done := make(chan error, 1)
	go func() {
		_, finish, e := f.service.prepareSkills(callctx, scope, id, "session.create", map[string]string{"fixture": "report-loss"})
		defer finish()
		done <- e
	}()
	if !stream.Receive() {
		t.Fatal(stream.Err())
	}
	var request workspace.ReadRequest
	if e := domain.Decode(stream.Msg().RequestJson, &request); e != nil || request.Skills == nil || request.Skills.Preparation == nil {
		t.Fatal(request, e)
	}
	v, e := f.service.retainedSkillPreparation(id)
	if e != nil || v.State != skillPreparationPending || v.Scope.Preparation.ScopeDigest != request.Skills.Preparation.ScopeDigest {
		t.Fatal("dispatch preceded durable ownership", v, e)
	}
	cancel()
	if e = <-done; e == nil {
		t.Fatal("lost response accepted")
	}
	reconciled := make(chan error, 1)
	go func() {
		reconciled <- f.service.reconcileSkillPreparations(domain.WithPrincipal(ctx, domain.Principal{Type: domain.OwnerDevice}))
	}()
	if !stream.Receive() {
		t.Fatal(stream.Err())
	}
	var cleanup workspace.ReadRequest
	if e = domain.Decode(stream.Msg().RequestJson, &cleanup); e != nil || cleanup.Skills == nil || cleanup.Skills.Action != domain.CleanupSkillPreparation || *cleanup.Skills.Preparation != *request.Skills.Preparation || cleanup.Skills.WorkerDeviceID != f.workerDevice {
		t.Fatal("cleanup changed original authority", cleanup, e)
	}
	raw, _ := json.Marshal(domain.SkillReadResult{Entries: []domain.SkillEntry{}})
	if _, e = f.workerClient.ReportWorkspaceRead(ctx, ownerRequest(f.workerIdentity, &pb.ReportWorkspaceReadRequest{MachineId: f.machine.Id, InstanceId: f.workerInstance, ReadId: string(cleanup.ID), DocumentJson: raw})); e != nil {
		t.Fatal(e)
	}
	if e = <-reconciled; e != nil {
		t.Fatal(e)
	}
	v, e = f.service.retainedSkillPreparation(id)
	if e != nil || v.State != skillPreparationRemoved {
		t.Fatal(v, e)
	}
}

func TestPreparationFailedMutationJoinsBeforeCleanup(t *testing.T) {
	f, ctx, _, id, raw := journalFixture(t)
	if _, e := f.service.Store.Mutate(ctx, id, "session.create", raw, func(*store.Tx) (any, error) {
		return nil, domain.Fail(domain.Conflict, "fixture concurrent revision change", "")
	}); e == nil {
		t.Fatal("failed original mutation accepted")
	}
	if e := f.service.reconcileSkillPreparations(ctx); e != nil {
		t.Fatal(e)
	}
	v, _ := f.service.retainedSkillPreparation(id)
	if v.State != skillPreparationPending {
		t.Fatal("mutation ownership was not joined", v.State)
	}
	f.service.finishSkillPreparation(id)
	if e := f.service.reconcileSkillPreparations(ctx); e != nil {
		t.Fatal(e)
	}
	v, _ = f.service.retainedSkillPreparation(id)
	if v.State != skillPreparationCleaning {
		t.Fatal("positively absent receipt did not retain offline cleanup", v.State)
	}
}
func TestPreparationExclusiveStoreRestartRetainsOriginalOfflineCleanup(t *testing.T) {
	f, ctx, scope, id, _ := journalFixture(t)
	root := f.service.Store.Root()
	if e := f.service.Store.Close(); e != nil {
		t.Fatal(e)
	}
	reopened, e := store.Open(ctx, root)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { reopened.Close() })
	// Real restart discards process-local claims only after the exclusive Store
	// lock proves the old server process cannot still publish an acceptance.
	liveSkillPreparations.Lock()
	delete(liveSkillPreparations.owners[root], id)
	liveSkillPreparations.Unlock()
	restarted := &Service{Store: reopened, Identity: f.service.Identity}
	if e = restarted.reconcileSkillPreparations(ctx); e != nil {
		t.Fatal(e)
	}
	v, e := restarted.retainedSkillPreparation(id)
	if e != nil || v.State != skillPreparationCleaning || *v.Scope.Preparation != *scope.Preparation || v.Scope.WorkerDeviceID != scope.WorkerDeviceID {
		t.Fatal(v, e)
	}
}
