// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestPrimaryWorkRescansOriginalDependencyBlockedStorage(t *testing.T) {
	for _, scenario := range []struct {
		name     string
		recovery bool
		terminal int
	}{
		{name: "cleanup"}, {name: "recovery", recovery: true}, {name: "later-terminal", terminal: 1}, {name: "across-pages", terminal: store.MaxPage + 1},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			f := newStorageFixture(t)
			var preparation workspace.PrepareRequest
			var manifest workspace.Manifest
			if err := f.service.Store.Read(f.ownerContext, func(tx *store.Tx) error {
				var err error
				preparation, manifest, err = workspaceReadScope(tx, f.session)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			blocked, child, ordinary, later := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
			input := workspace.StorageRequest{Version: 1, OperationID: blocked, Action: workspace.StorageCleanup, PreviousState: domain.WorkspacePresent, SnapshotID: domain.NewID(), PreviewDigest: strings.Repeat("a", 64), Preparation: preparation, Manifest: manifest}
			if scenario.recovery {
				original := input
				original.OperationID = domain.NewID()
				claim := workspace.StorageJournalClaim{JobID: original.OperationID, InstanceID: f.instance, Revision: 1, AssignmentDigest: strings.Repeat("b", 64)}
				input.Action = workspace.StorageRecover
				input.Recovery = &workspace.StorageRecovery{Original: original, InstanceID: claim.InstanceID, Revision: claim.Revision, AssignmentDigest: claim.AssignmentDigest, Claims: []workspace.StorageJournalClaim{claim}}
			}
			if err := input.Validate(); err != nil {
				t.Fatal("invalid original storage selection", err)
			}
			raw, _ := json.Marshal(input)
			_, err := f.service.Store.Mutate(f.ownerContext, domain.NewID(), "fixture.dependent-storage", nil, func(tx *store.Tx) (any, error) {
				if err := tx.RegisterSidechat(f.session, child); err != nil {
					return nil, err
				}
				if _, err := tx.PutJob(blocked, 0, f.session, "", domain.Job{Type: domain.WorkspaceStorageJob, State: domain.JobQueued, MachineID: f.machine, Input: raw, AcceptedAt: time.Now().UTC()}); err != nil {
					return nil, err
				}
				for i := 0; i < scenario.terminal; i++ {
					if _, err := tx.PutJob(domain.NewID(), 0, "", "", domain.Job{Type: domain.InspectRepositoryJob, State: domain.JobSucceeded, MachineID: f.machine, Input: []byte(`{}`), Output: []byte(`{}`), AcceptedAt: time.Now().UTC()}); err != nil {
						return nil, err
					}
				}
				// Allocate after the intervening terminal records to exercise stable pages.
				ordinary, later = domain.NewID(), domain.NewID()
				for _, id := range []domain.ID{ordinary, later} {
					if _, err := tx.PutJob(id, 0, "", "", domain.Job{Type: domain.InspectRepositoryJob, State: domain.JobQueued, MachineID: f.machine, Input: []byte(`{"path":"/isolated/fixture","preferred_remote":"origin"}`), AcceptedAt: time.Now().UTC()}); err != nil {
						return nil, err
					}
				}
				row, value, err := sessionRecord(tx, f.session)
				if err != nil {
					return nil, err
				}
				value.Storage = &domain.WorkspaceStorage{State: domain.WorkspaceStoragePending, JobID: blocked}
				return tx.Put(row.Kind, row.ID, row.Revision, row.SessionID, row.ProjectID, value)
			})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			stream, err := f.worker.WatchWork(ctx, ownerRequest(f.workerIdentity, &pb.WatchWorkRequest{MachineId: string(f.machine), InstanceId: string(f.instance)}))
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			if !stream.Receive() || !stream.Msg().Heartbeat {
				t.Fatal("primary readiness missing", stream.Err())
			}
			if !stream.Receive() || stream.Msg().Job == nil || stream.Msg().Job.Id != string(ordinary) {
				t.Fatal("later independent work did not progress", stream.Msg(), stream.Err())
			}
			assigned := stream.Msg().Job
			assertQueued := func(id domain.ID) {
				t.Helper()
				row, err := f.service.Store.Get(context.Background(), domain.JobKind, id)
				if err != nil {
					t.Fatal(err)
				}
				job, err := store.Decode[domain.Job](row)
				if err != nil || job.State != domain.JobQueued || row.Revision != 1 {
					t.Fatal("primary preclaimed blocked or later work", id, job.State, row.Revision, err)
				}
			}
			assertQueued(blocked)
			assertQueued(later)
			_, err = f.service.Store.Mutate(f.ownerContext, domain.NewID(), "fixture.independent-cancel", nil, func(tx *store.Tx) (any, error) { return nil, tx.RequestJobCancellation(ordinary) })
			if err != nil {
				t.Fatal(err)
			}
			if !stream.Receive() || stream.Msg().CancelJobId != string(ordinary) {
				t.Fatal("targeted controls stalled behind dependency", stream.Msg(), stream.Err())
			}
			// Represent completed independent retirement only in this isolated durable
			// index fixture. No native cleanup is executed or claimed by this test.
			db, err := sql.Open("sqlite", filepath.Join(f.service.Store.Root(), "state.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			result, err := db.ExecContext(context.Background(), "DELETE FROM metadata WHERE key=? AND value=?", "sidechat-dependency:"+string(f.session)+":"+string(child), child)
			db.Close()
			if err != nil {
				t.Fatal(err)
			}
			n, err := result.RowsAffected()
			if err != nil || n != 1 {
				t.Fatal("original dependent was not retired", n, err)
			}
			_, err = f.service.Store.Mutate(f.ownerContext, domain.NewID(), "fixture.retirement-wake", nil, func(tx *store.Tx) (any, error) {
				row, err := tx.Get(domain.SessionKind, f.session)
				if err != nil {
					return nil, err
				}
				value, err := store.Decode[domain.Session](row)
				if err != nil {
					return nil, err
				}
				return tx.Put(row.Kind, row.ID, row.Revision, row.SessionID, row.ProjectID, value)
			})
			if err != nil {
				t.Fatal(err)
			}
			assertQueued(blocked)
			assertQueued(later)
			// Positively terminalize only B; A's original native operation stays unrun.
			_, err = f.worker.ReportWork(context.Background(), ownerRequest(f.workerIdentity, &pb.ReportWorkRequest{Mutation: &pb.Mutation{Id: assigned.Id, ExpectedRevision: assigned.Revision, RequestId: string(domain.NewID())}, MachineId: string(f.machine), InstanceId: string(f.instance), Problem: &pb.ErrorDetail{Code: string(domain.Canceled)}}))
			if err != nil {
				t.Fatal(err)
			}
			if !stream.Receive() || stream.Msg().Job == nil || stream.Msg().Job.Id != string(blocked) {
				t.Fatal("original stream did not revisit eligible storage", stream.Msg(), stream.Err())
			}
			if stream.Msg().Job.Revision != 2 {
				t.Fatal("original job was claimed more than once", stream.Msg().Job.Revision)
			}
			assertQueued(later)
		})
	}
}

type blockedStorageReference struct {
	session, child, job domain.ID
}

func newDependencyBlockedPage(t *testing.T, recovery bool) (*storageFixture, []blockedStorageReference) {
	t.Helper()
	f := newStorageFixture(t)
	template, err := store.Decode[domain.Session](f.sessionRecord())
	if err != nil {
		t.Fatal(err)
	}
	preparations := make([]workspace.PrepareRequest, store.MaxPage)
	manifests := make([]workspace.Manifest, store.MaxPage)
	for i := range preparations {
		preparations[i] = workspace.PrepareRequest{SessionID: domain.NewID(), MachineID: f.machine, Type: domain.GeneralChat}
		manifests[i], err = f.manager.Prepare(context.Background(), preparations[i])
		if err != nil {
			t.Fatal(err)
		}
	}
	blocked := make([]blockedStorageReference, store.MaxPage)
	var beforeBlocked domain.ID
	_, err = f.service.Store.Mutate(f.ownerContext, domain.NewID(), "fixture.dependency-page", nil, func(tx *store.Tx) (any, error) {
		// Create all preparation records first. The next complete page then
		// contains only blocked jobs, each with its own canonical session owner.
		for i, preparation := range preparations {
			input, err := json.Marshal(preparation)
			if err != nil {
				return nil, err
			}
			output, err := json.Marshal(manifests[i])
			if err != nil {
				return nil, err
			}
			id := domain.NewID()
			beforeBlocked = id
			if _, err := tx.PutJob(id, 0, preparation.SessionID, "", domain.Job{Type: domain.PrepareWorkspaceJob, State: domain.JobSucceeded, MachineID: f.machine, Input: input, Output: output, AcceptedAt: time.Now().UTC()}); err != nil {
				return nil, err
			}
			value := template
			value.Preparation = &domain.SessionPreparation{JobID: id, State: domain.PreparationReady}
			if _, err := tx.Put(domain.SessionKind, preparation.SessionID, 0, preparation.SessionID, "", value); err != nil {
				return nil, err
			}
		}
		for i, preparation := range preparations {
			ref := blockedStorageReference{session: preparation.SessionID, child: domain.NewID(), job: domain.NewID()}
			input := workspace.StorageRequest{Version: 1, OperationID: ref.job, Action: workspace.StorageCleanup, PreviousState: domain.WorkspacePresent, SnapshotID: domain.NewID(), PreviewDigest: strings.Repeat("a", 64), Preparation: preparation, Manifest: manifests[i]}
			if recovery {
				original := input
				original.OperationID = domain.NewID()
				claim := workspace.StorageJournalClaim{JobID: original.OperationID, InstanceID: f.instance, Revision: 1, AssignmentDigest: strings.Repeat("b", 64)}
				input.Action = workspace.StorageRecover
				input.Recovery = &workspace.StorageRecovery{Original: original, InstanceID: claim.InstanceID, Revision: claim.Revision, AssignmentDigest: claim.AssignmentDigest, Claims: []workspace.StorageJournalClaim{claim}}
			}
			if err := input.Validate(); err != nil {
				return nil, err
			}
			raw, err := json.Marshal(input)
			if err != nil {
				return nil, err
			}
			if err := tx.RegisterSidechat(ref.session, ref.child); err != nil {
				return nil, err
			}
			if _, err := tx.PutJob(ref.job, 0, ref.session, "", domain.Job{Type: domain.WorkspaceStorageJob, State: domain.JobQueued, MachineID: f.machine, Input: raw, AcceptedAt: time.Now().UTC()}); err != nil {
				return nil, err
			}
			row, value, err := sessionRecord(tx, ref.session)
			if err != nil {
				return nil, err
			}
			value.Storage = &domain.WorkspaceStorage{State: domain.WorkspaceStoragePending, JobID: ref.job}
			if _, err := tx.Put(row.Kind, row.ID, row.Revision, row.SessionID, row.ProjectID, value); err != nil {
				return nil, err
			}
			blocked[i] = ref
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.service.Store.Read(f.ownerContext, func(tx *store.Tx) error {
		page, err := tx.Jobs(f.machine, "", "", beforeBlocked, store.MaxPage)
		if err != nil {
			return err
		}
		if len(page) != store.MaxPage {
			t.Fatalf("fixture needs a full blocked page, got %d jobs", len(page))
		}
		for i, row := range page {
			if row.ID != blocked[i].job || row.SessionID != blocked[i].session {
				t.Fatal("fixture page is not the original blocked storage sequence")
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return f, blocked
}

func assertDependencyPageQueued(t *testing.T, f *storageFixture, blocked []blockedStorageReference) {
	t.Helper()
	for _, ref := range blocked {
		row, err := f.service.Store.Get(context.Background(), domain.JobKind, ref.job)
		if err != nil {
			t.Fatal(err)
		}
		job, err := store.Decode[domain.Job](row)
		if err != nil || job.State != domain.JobQueued || row.Revision != 1 {
			t.Fatal("blocked storage was claimed", ref.job, job.State, row.Revision, err)
		}
	}
}

func retireBlockedStorageDependent(t *testing.T, f *storageFixture, ref blockedStorageReference) {
	t.Helper()
	// This fixture removes only the original index entry. It neither executes
	// native cleanup nor signals the store; ReportWork supplies the later wake.
	db, err := sql.Open("sqlite", filepath.Join(f.service.Store.Root(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	result, err := db.ExecContext(context.Background(), "DELETE FROM metadata WHERE key=? AND value=?", "sidechat-dependency:"+string(ref.session)+":"+string(ref.child), ref.child)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		t.Fatal("original dependent was not retired", n, err)
	}
}

func TestPrimaryWorkAdvancesPastFullDependencyBlockedPage(t *testing.T) {
	for _, recovery := range []bool{false, true} {
		name := "cleanup"
		if recovery {
			name = "recovery"
		}
		t.Run(name, func(t *testing.T) {
			f, blocked := newDependencyBlockedPage(t, recovery)
			ordinary := domain.NewID()
			_, err := f.service.Store.Mutate(f.ownerContext, domain.NewID(), "fixture.after-dependency-page", nil, func(tx *store.Tx) (any, error) {
				return tx.PutJob(ordinary, 0, "", "", domain.Job{Type: domain.InspectRepositoryJob, State: domain.JobQueued, MachineID: f.machine, Input: []byte(`{"path":"/isolated/fixture","preferred_remote":"origin"}`), AcceptedAt: time.Now().UTC()})
			})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			stream, err := f.worker.WatchWork(ctx, ownerRequest(f.workerIdentity, &pb.WatchWorkRequest{MachineId: string(f.machine), InstanceId: string(f.instance)}))
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			if !stream.Receive() || !stream.Msg().Heartbeat {
				t.Fatal("primary readiness missing", stream.Err())
			}
			if !stream.Receive() || stream.Msg().Job == nil || stream.Msg().Job.Id != string(ordinary) {
				t.Fatal("full blocked page starved independent work", stream.Msg(), stream.Err())
			}
			assigned := stream.Msg().Job
			assertDependencyPageQueued(t, f, blocked)
			retireBlockedStorageDependent(t, f, blocked[0])
			_, err = f.worker.ReportWork(ctx, ownerRequest(f.workerIdentity, &pb.ReportWorkRequest{Mutation: &pb.Mutation{Id: assigned.Id, ExpectedRevision: assigned.Revision, RequestId: string(domain.NewID())}, MachineId: string(f.machine), InstanceId: string(f.instance), Problem: &pb.ErrorDetail{Code: string(domain.Canceled)}}))
			if err != nil {
				t.Fatal(err)
			}
			if !stream.Receive() || stream.Msg().Job == nil || stream.Msg().Job.Id != string(blocked[0].job) || stream.Msg().Job.Revision != 2 {
				t.Fatal("completion did not revisit the earliest blocked job", stream.Msg(), stream.Err())
			}
			assertDependencyPageQueued(t, f, blocked[1:])
		})
	}
}

func TestPrimaryWorkFullBlockedPageHeartbeatAndCancellation(t *testing.T) {
	f, blocked := newDependencyBlockedPage(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	stream, err := f.worker.WatchWork(ctx, ownerRequest(f.workerIdentity, &pb.WatchWorkRequest{MachineId: string(f.machine), InstanceId: string(f.instance)}))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	for i := 0; i < 2; i++ {
		if !stream.Receive() || !stream.Msg().Heartbeat {
			t.Fatal("all-blocked stream did not remain live", stream.Msg(), stream.Err())
		}
	}
	assertDependencyPageQueued(t, f, blocked)
	cancel()
	if stream.Receive() || stream.Err() == nil {
		t.Fatal("primary stream did not stop after cancellation", stream.Msg(), stream.Err())
	}
	assertDependencyPageQueued(t, f, blocked)
}
