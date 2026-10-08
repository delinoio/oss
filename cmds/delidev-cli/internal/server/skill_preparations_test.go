// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
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
	v, e := readSkillPreparation(f.service.skillPreparationPath(id))
	if e != nil || v.State != skillPreparationPending || domain.ValidateSkillPreparation(v.Scope) != nil || v.Scope.Preparation.ServerID != f.service.Identity.ServerID {
		t.Fatal(v, e)
	}
	if e = f.service.reconcileSkillPreparations(ctx); e != nil {
		t.Fatal(e)
	}
	v, _ = readSkillPreparation(f.service.skillPreparationPath(id))
	if v.State != skillPreparationPending {
		t.Fatal("cleanup crossed original mutation", v.State)
	}
	f.service.finishSkillPreparation(id)
	if e = f.service.reconcileSkillPreparations(ctx); e != nil {
		t.Fatal(e)
	}
	v, _ = readSkillPreparation(f.service.skillPreparationPath(id))
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
	v, e := readSkillPreparation(f.service.skillPreparationPath(id))
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
	v, e := readSkillPreparation(f.service.skillPreparationPath(id))
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
