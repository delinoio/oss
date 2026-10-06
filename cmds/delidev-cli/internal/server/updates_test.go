// SPDX-License-Identifier: Apache-2.0
package server

import (
	"connectrpc.com/connect"
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/updates"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"testing"
	"time"
)

func updateFixture(t *testing.T) (*oauthFixture, store.Record, domain.ID, domain.ID, context.Context) {
	t.Helper()
	f := newOAuthFixture(t)
	machine, device, instance := domain.NewID(), domain.NewID(), domain.NewID()
	var mr store.Record
	_, e := f.s.Store.Mutate(f.ctx, domain.NewID(), "fixture.update.machine", nil, func(tx *store.Tx) (any, error) {
		var e error
		mr, e = tx.Put(domain.MachineKind, machine, 0, "", "", domain.Machine{Name: "Fixture", OS: "darwin", Architecture: "arm64", Version: rpc.Version, WorkerCapabilities: []domain.WorkerCapability{domain.SignedWorkerUpdatesV1}})
		if e != nil {
			return nil, e
		}
		if _, e = tx.Put(domain.DeviceKind, device, 0, "", "", domain.Device{Name: "Fixture", Type: domain.WorkerDevice, MachineID: machine}); e != nil {
			return nil, e
		}
		return nil, tx.SetWorkerInstance(machine, instance, time.Now().UTC())
	})
	if e != nil {
		t.Fatal(e)
	}
	f.s.releaseFactory = func() (releaseClient, error) {
		return fixtureRelease{candidate: updates.Verified{Payload: updates.Payload{Version: "0.2.0", Artifacts: []updates.Artifact{{Component: updates.Worker, Target: "darwin-arm64"}}}, Canonical: []byte("fixture-signed-manifest"), ManifestSHA256: "fixture"}}, nil
	}
	return f, mr, device, instance, domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.WorkerDevice, MachineID: machine, DeviceID: device})
}
func TestWorkerUpdateOnceClaimFenceIdleAndReplay(t *testing.T) {
	f, mr, _, instance, wctx := updateFixture(t)
	check := &pb.CheckUpdateRequest{RequestId: string(domain.NewID()), Component: pb.UpdateComponent_UPDATE_COMPONENT_WORKER, Target: pb.UpdateTarget_UPDATE_TARGET_DARWIN_ARM64, CurrentVersion: rpc.Version, MachineId: string(mr.ID), ExpectedMachineRevision: mr.Revision}
	observed, e := f.s.CheckUpdate(f.ctx, connect.NewRequest(check))
	if e != nil {
		t.Fatal(e)
	}
	replay, e := f.s.CheckUpdate(f.ctx, connect.NewRequest(check))
	if e != nil || !replay.Msg.Replayed || replay.Msg.Update.Id != observed.Msg.Update.Id {
		t.Fatal("check replay", e)
	}
	accepted, e := f.s.RequestWorkerUpdate(f.ctx, connect.NewRequest(&pb.RequestWorkerUpdateRequest{Mutation: &pb.Mutation{Id: observed.Msg.Update.Id, ExpectedRevision: observed.Msg.Update.Revision, RequestId: string(domain.NewID())}}))
	if e != nil {
		t.Fatal(e)
	}
	// Terminal history remains an obligation until positive native cleanup.
	terminal := domain.NewID()
	var tr store.Record
	_, e = f.s.Store.Mutate(f.ctx, domain.NewID(), "fixture.update.terminal", nil, func(tx *store.Tx) (any, error) {
		var e error
		tr, e = tx.Put(domain.TerminalKind, terminal, 0, "", "", map[string]any{"machine_id": mr.ID, "cleanup_verified": false})
		return nil, e
	})
	if e != nil {
		t.Fatal(e)
	}
	claim := &pb.ClaimWorkerUpdateRequest{Mutation: &pb.Mutation{Id: accepted.Msg.Update.Id, ExpectedRevision: accepted.Msg.Update.Revision, RequestId: string(domain.NewID())}, InstanceId: string(instance)}
	if _, e = f.s.ClaimWorkerUpdate(wctx, connect.NewRequest(claim)); connect.CodeOf(e) != connect.CodeAborted {
		t.Fatal("unjoined terminal admitted", e)
	}
	_, e = f.s.Store.Mutate(f.ctx, domain.NewID(), "fixture.update.cleanup", nil, func(tx *store.Tx) (any, error) {
		_, e := tx.Put(domain.TerminalKind, tr.ID, tr.Revision, "", "", map[string]any{"machine_id": mr.ID, "cleanup_verified": true})
		return nil, e
	})
	if e != nil {
		t.Fatal(e)
	}
	claimed, e := f.s.ClaimWorkerUpdate(wctx, connect.NewRequest(claim))
	if e != nil {
		t.Fatal(e)
	}
	again, e := f.s.ClaimWorkerUpdate(wctx, connect.NewRequest(claim))
	if e != nil || !again.Msg.Replayed {
		t.Fatal("original claim replay", e)
	}
	if e = f.s.Store.Read(f.ctx, func(tx *store.Tx) error { return tx.WorkerUpdateAdmission(mr.ID) }); domain.SafeError(e).Code != domain.Conflict {
		t.Fatal("no admission fence", e)
	}
	if _, e = f.s.CancelUpdate(f.ctx, connect.NewRequest(&pb.CancelUpdateRequest{Mutation: &pb.Mutation{Id: claimed.Msg.Update.Id, ExpectedRevision: claimed.Msg.Update.Revision, RequestId: string(domain.NewID())}})); connect.CodeOf(e) != connect.CodeFailedPrecondition {
		t.Fatal("claimed update canceled", e)
	}
	report := &pb.ReportWorkerUpdateRequest{Mutation: &pb.Mutation{Id: claimed.Msg.Update.Id, ExpectedRevision: claimed.Msg.Update.Revision, RequestId: string(domain.NewID())}, InstanceId: string(instance), Outcome: pb.WorkerUpdateOutcome_WORKER_UPDATE_OUTCOME_FAILED, InstalledVersion: rpc.Version}
	if _, e = f.s.ReportWorkerUpdate(wctx, connect.NewRequest(report)); e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.ReportWorkerUpdate(wctx, connect.NewRequest(report)); e != nil {
		t.Fatal("original report replay", e)
	}
	if e = f.s.Store.Read(f.ctx, func(tx *store.Tx) error { return tx.WorkerUpdateAdmission(mr.ID) }); e != nil {
		t.Fatal("completed failure retains fence", e)
	}
}
func TestWorkerPendingUpdateCannotBeHiddenByCompletedHistory(t *testing.T) {
	f, mr, device, instance, wctx := updateFixture(t)
	var pending store.Record
	_, e := f.s.Store.Mutate(f.ctx, domain.NewID(), "fixture.update.history", nil, func(tx *store.Tx) (any, error) {
		for i := 0; i < store.MaxPage+5; i++ {
			if _, e := tx.Put(domain.UpdateKind, domain.NewID(), 0, "", "", updates.Operation{State: updates.Succeeded}); e != nil {
				return nil, e
			}
		}
		var e error
		pending, e = tx.Put(domain.UpdateKind, domain.NewID(), 0, "", "", updates.Operation{ServerID: f.s.Identity.ServerID, Actor: domain.Principal{Type: domain.OwnerDevice}, Component: updates.Worker, State: updates.Waiting, MachineID: mr.ID, DeviceID: device})
		return nil, e
	})
	if e != nil {
		t.Fatal(e)
	}
	result, e := f.s.PollWorkerUpdate(wctx, connect.NewRequest(&pb.PollWorkerUpdateRequest{InstanceId: string(instance)}))
	if e != nil || result.Msg.Update == nil || result.Msg.Update.Id != string(pending.ID) || !result.Msg.Idle {
		t.Fatal("history starved original pending update", e)
	}
}
func TestWorkerAutomaticChecksNeverRepeatCanceledAttempt(t *testing.T) {
	f, _, _, _, _ := updateFixture(t)
	if e := f.s.observeWorkerUpdates(f.ctx); e != nil {
		t.Fatal(e)
	}
	rows, e := f.s.Store.List(f.ctx, store.Filter{Kind: domain.UpdateKind, Limit: store.MaxPage})
	if e != nil || len(rows) != 1 {
		t.Fatal(e, len(rows))
	}
	r := rows[0]
	if _, e = f.s.CancelUpdate(f.ctx, connect.NewRequest(&pb.CancelUpdateRequest{Mutation: &pb.Mutation{Id: string(r.ID), ExpectedRevision: r.Revision, RequestId: string(domain.NewID())}})); e != nil {
		t.Fatal(e)
	}
	if e = f.s.observeWorkerUpdates(f.ctx); e != nil {
		t.Fatal(e)
	}
	rows, _ = f.s.Store.List(f.ctx, store.Filter{Kind: domain.UpdateKind, Limit: store.MaxPage})
	if len(rows) != 1 {
		t.Fatal("automatic check repeated canceled release")
	}
}

func TestWorkerUpdateInspectionRetainsExactOriginalAndDeviceScope(t *testing.T) {
	f, mr, device, instance, wctx := updateFixture(t)
	original, pending, foreign := domain.NewID(), domain.NewID(), domain.NewID()
	_, e := f.s.Store.Mutate(f.ctx, domain.NewID(), "fixture.update.exact-inspection", nil, func(tx *store.Tx) (any, error) {
		for _, value := range []struct {
			id     domain.ID
			state  updates.State
			device domain.ID
		}{{original, updates.Succeeded, device}, {pending, updates.Waiting, device}, {foreign, updates.Succeeded, domain.NewID()}} {
			_, e := tx.Put(domain.UpdateKind, value.id, 0, "", "", updates.Operation{ServerID: f.s.Identity.ServerID, Actor: domain.Principal{Type: domain.OwnerDevice}, Component: updates.Worker, State: value.state, MachineID: mr.ID, DeviceID: value.device})
			if e != nil {
				return nil, e
			}
		}
		return nil, nil
	})
	if e != nil {
		t.Fatal(e)
	}
	read := func(id domain.ID) (*connect.Response[pb.PollWorkerUpdateResponse], error) {
		return f.s.PollWorkerUpdate(wctx, connect.NewRequest(&pb.PollWorkerUpdateRequest{InstanceId: string(instance), OriginalUpdateId: string(id)}))
	}
	r, e := read(original)
	if e != nil || r.Msg.Update == nil || r.Msg.Update.Id != string(original) || r.Msg.Idle {
		t.Fatal("Exact original observation acquired latest/idle authority", e)
	}
	if _, e = read(foreign); connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("Foreign device update exposed", e)
	}
	if _, e = read("invalid"); connect.CodeOf(e) != connect.CodeInvalidArgument {
		t.Fatal("Malformed original ID accepted", e)
	}
}
