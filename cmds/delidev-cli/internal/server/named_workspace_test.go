// SPDX-License-Identifier: Apache-2.0
package server

import (
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	"testing"
)

func TestNamedWorkspaceAcceptanceRetainsFrozenRegisteredName(t *testing.T) {
	f := newScheduleDispatchFixture(t, domain.ScheduleAllowOverlap, false)
	_, occurrence := f.accept(t, domain.ManualOccurrence)
	session, _ := store.Decode[domain.Session](f.record(t, domain.SessionKind, occurrence.SessionID))
	job, _ := store.Decode[domain.Job](f.record(t, domain.JobKind, session.Preparation.JobID))
	var original workspace.PrepareRequest
	if domain.Decode(job.Input, &original) != nil || original.Repositories[0].DirectoryName != "Fixture" {
		t.Fatal("accepted name missing")
	}
	f.mutate(t, func(tx *store.Tx) error {
		r, err := tx.Get(domain.RepositoryKind, f.repository)
		if err != nil {
			return err
		}
		repo, err := store.Decode[domain.Repository](r)
		if err != nil {
			return err
		}
		repo.Name = "new/name"
		_, err = tx.Put(r.Kind, r.ID, r.Revision, "", "", repo)
		return err
	})
	frozen, _ := store.Decode[domain.Job](f.record(t, domain.JobKind, session.Preparation.JobID))
	if string(frozen.Input) != string(job.Input) {
		t.Fatal("rename changed original request")
	}
	err := f.service.Store.Read(f.ctx, func(tx *store.Tx) error {
		later, err := sessionWorkspaceRequest(tx, domain.NewID(), session)
		if err != nil {
			return err
		}
		if later.Repositories[0].DirectoryName != "new_name" {
			t.Fatal("later preparation did not use current name")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestNamedWorkspaceCapabilityPreservesLegacyAndFailsBeforeQueue(t *testing.T) {
	legacy := workspace.PrepareRequest{Repositories: []workspace.RepositorySpec{{ID: domain.NewID(), SourceKind: workspace.RemoteCloneSource}}}
	oldMachine := domain.Machine{WorkerCapabilities: []domain.WorkerCapability{domain.RemoteWorkspaceCloneV1}}
	if err := requireNamedWorkspaceCapability(oldMachine, legacy); err != nil {
		t.Fatal("legacy request was gated", err)
	}
	named := legacy
	named.Repositories = append([]workspace.RepositorySpec(nil), legacy.Repositories...)
	named.Repositories[0].DirectoryName = "oss"
	if err := requireNamedWorkspaceCapability(oldMachine, named); domain.SafeError(err).Code != domain.Unsupported {
		t.Fatal("named request admitted to old peer", err)
	}
	raw, _ := json.Marshal(named)
	if err := requireNamedJobCapability(oldMachine, domain.Job{Type: domain.PrepareWorkspaceJob, Input: raw}); domain.SafeError(err).Code != domain.Unsupported {
		t.Fatal("downgraded assignment admitted", err)
	}
	f := newScheduleDispatchFixture(t, domain.ScheduleAllowOverlap, false)
	f.mutate(t, func(tx *store.Tx) error {
		r, err := tx.Get(domain.MachineKind, f.machine)
		if err != nil {
			return err
		}
		m, _ := store.Decode[domain.Machine](r)
		m.WorkerCapabilities = []domain.WorkerCapability{domain.RemoteWorkspaceCloneV1}
		_, err = tx.Put(r.Kind, r.ID, r.Revision, "", "", m)
		return err
	})
	err := f.service.Store.Read(f.ctx, func(tx *store.Tx) error {
		session := domain.Session{MachineID: f.machine, Workspace: domain.Worktree}
		return queueSessionWorkspace(tx, domain.NewID(), &session, named)
	})
	if domain.SafeError(err).Code != domain.Unsupported {
		t.Fatal("unsupported queue accepted", err)
	}
}
