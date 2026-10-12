// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

func TestNamedWorkspaceAssignmentRejectsDowngradedWorkerAndPreservesLegacy(t *testing.T) {
	legacy := workspace.PrepareRequest{Repositories: []workspace.RepositorySpec{{ID: domain.NewID()}}}
	raw, _ := json.Marshal(legacy)
	job := domain.Job{Type: domain.PrepareWorkspaceJob, Input: raw}
	machine := domain.Machine{WorkerCapabilities: []domain.WorkerCapability{domain.RemoteWorkspaceCloneV1}}
	if err := requireNamedWorkspaceJob(machine, job); err != nil {
		t.Fatal("legacy preparation rejected", err)
	}
	legacy.Repositories[0].DirectoryName = "oss"
	job.Input, _ = json.Marshal(legacy)
	if err := requireNamedWorkspaceJob(machine, job); domain.SafeError(err).Code != domain.Unsupported {
		t.Fatal("downgraded Worker accepted named request", err)
	}
	machine.WorkerCapabilities = append(machine.WorkerCapabilities, domain.NamedWorkspaceDirectoriesV1)
	if err := requireNamedWorkspaceJob(machine, job); err != nil {
		t.Fatal("negotiated Worker rejected", err)
	}
}

func TestDowngradedNamedExecutionIsTerminalAndReconciled(t *testing.T) {
	ctx := context.Background()
	f := newConfiguredAuthorityFixture(t, "http://127.0.0.1:1", nil, true)
	preparation, _ := json.Marshal(workspace.PrepareRequest{SessionID: f.input.SessionID, MachineID: f.input.MachineID, Type: domain.Worktree, Repositories: []workspace.RepositorySpec{{ID: domain.NewID(), DirectoryName: "repository"}}})
	f.input.Preparation = preparation
	input, _ := json.Marshal(f.input)
	_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.named-execution", nil, func(tx *store.Tx) (any, error) {
		r, err := tx.Get(domain.JobKind, f.job)
		if err != nil {
			return nil, err
		}
		job, err := store.Decode[domain.Job](r)
		if err != nil {
			return nil, err
		}
		job.Input = input
		if _, err := tx.PutJob(r.ID, r.Revision, r.SessionID, r.ProjectID, job); err != nil {
			return nil, err
		}
		ir, err := tx.Get(domain.QueueKind, f.input.InputID)
		if err != nil {
			return nil, err
		}
		queued, err := store.Decode[domain.QueuedInput](ir)
		if err != nil {
			return nil, err
		}
		queued.Delivery = domain.InputClaimed
		if _, err := tx.Put(ir.Kind, ir.ID, ir.Revision, ir.SessionID, ir.ProjectID, queued); err != nil {
			return nil, err
		}
		return r, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.service.Store.Mutate(ctx, domain.NewID(), "worker.claim", nil, func(tx *store.Tx) (any, error) {
		r, err := tx.Get(domain.JobKind, f.job)
		if err != nil {
			return nil, err
		}
		job, err := store.Decode[domain.Job](r)
		if err != nil {
			return nil, err
		}
		return rejectDowngradedNamedWorkspaceJob(tx, r, job, workspace.NamedDirectoriesUnsupported())
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.service.Store.Read(ctx, func(tx *store.Tx) error {
		r, err := tx.Get(domain.JobKind, f.job)
		if err != nil {
			return err
		}
		job, err := store.Decode[domain.Job](r)
		if err != nil || job.State != domain.JobFailed || job.FinishedAt == nil || job.Problem == nil || job.Problem.Code != domain.Unsupported {
			t.Fatal("downgraded execution was not terminally rejected", job, err)
		}
		_, session, err := sessionRecord(tx, f.input.SessionID)
		if err != nil {
			return err
		}
		if session.Recovery != domain.NeedsRecovery || session.Dispatch != domain.DispatchPaused {
			t.Fatal("execution rejection did not retain reconciliation ownership", session)
		}
		ir, err := tx.Get(domain.QueueKind, f.input.InputID)
		if err != nil {
			return err
		}
		queued, err := store.Decode[domain.QueuedInput](ir)
		if err != nil || queued.Delivery != domain.InputUncertain {
			t.Fatal("execution input lost its uncertainty", queued, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestDowngradedNamedCompactionReleasesUnclaimedAction(t *testing.T) {
	ctx := context.Background()
	f := newConfiguredAuthorityFixture(t, "http://127.0.0.1:1", nil, true)
	jobID := domain.NewID()
	_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.named-compaction", nil, func(tx *store.Tx) (any, error) {
		sr, session, err := sessionRecord(tx, f.input.SessionID)
		if err != nil {
			return nil, err
		}
		session.CompactionJobID = jobID
		if _, err := tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session); err != nil {
			return nil, err
		}
		return tx.PutJob(jobID, 0, sr.ID, sr.ProjectID, domain.Job{Type: domain.CompactSessionJob, State: domain.JobQueued, MachineID: f.input.MachineID, Input: json.RawMessage(`{}`), AcceptedAt: time.Now().UTC()})
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.service.Store.Mutate(ctx, domain.NewID(), "worker.claim", nil, func(tx *store.Tx) (any, error) {
		r, err := tx.Get(domain.JobKind, jobID)
		if err != nil {
			return nil, err
		}
		job, err := store.Decode[domain.Job](r)
		if err != nil {
			return nil, err
		}
		return rejectDowngradedNamedWorkspaceJob(tx, r, job, workspace.NamedDirectoriesUnsupported())
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.service.Store.Read(ctx, func(tx *store.Tx) error {
		r, err := tx.Get(domain.JobKind, jobID)
		if err != nil {
			return err
		}
		job, err := store.Decode[domain.Job](r)
		if err != nil || job.State != domain.JobFailed || job.Problem == nil || job.Problem.Code != domain.Unsupported {
			t.Fatal("downgraded compaction was not terminally rejected", job, err)
		}
		_, session, err := sessionRecord(tx, f.input.SessionID)
		if err != nil {
			return err
		}
		if session.CompactionJobID != "" || session.Recovery != domain.NoRecovery {
			t.Fatal("unclaimed compaction retained ownership or required native recovery", session)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
