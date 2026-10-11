// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

func TestWorkspaceAcceptanceFreezesRegisteredNameAndLaterRename(t *testing.T) {
	f := newScheduleDispatchFixture(t, domain.ScheduleSkipOverlap, false)
	session := domain.Session{MachineID: f.machine, ProjectID: f.project, Workspace: domain.Worktree}
	var original workspace.PrepareRequest
	f.mutate(t, func(tx *store.Tx) error {
		r, err := tx.Get(domain.RepositoryKind, f.repository)
		if err != nil {
			return err
		}
		repo, err := store.Decode[domain.Repository](r)
		if err != nil {
			return err
		}
		repo.Name = "oss"
		if _, err := tx.Put(domain.RepositoryKind, r.ID, r.Revision, "", "", repo); err != nil {
			return err
		}
		original, err = sessionWorkspaceRequest(tx, domain.NewID(), session)
		return err
	})
	accepted, _ := json.Marshal(original)
	if original.Repositories[0].DirectoryName != "oss" {
		t.Fatal("registered name not accepted")
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
		_, err = tx.Put(domain.RepositoryKind, r.ID, r.Revision, "", "", repo)
		return err
	})
	f.mutate(t, func(tx *store.Tx) error {
		later, err := sessionWorkspaceRequest(tx, domain.NewID(), session)
		if err != nil {
			return err
		}
		if later.Repositories[0].DirectoryName != "new_name" {
			t.Fatal("later acceptance lost new registration")
		}
		return nil
	})
	retained, _ := json.Marshal(original)
	if !bytes.Equal(accepted, retained) {
		t.Fatal("registration rename changed accepted request")
	}
}

func TestNamedWorkspaceAcceptanceRequiresSeparateCapability(t *testing.T) {
	f := newScheduleDispatchFixture(t, domain.ScheduleSkipOverlap, false)
	session := domain.Session{MachineID: f.machine, ProjectID: f.project, Workspace: domain.Worktree}
	f.mutate(t, func(tx *store.Tx) error {
		r, err := tx.Get(domain.MachineKind, f.machine)
		if err != nil {
			return err
		}
		machine, err := store.Decode[domain.Machine](r)
		if err != nil {
			return err
		}
		machine.WorkerCapabilities = []domain.WorkerCapability{domain.RemoteWorkspaceCloneV1}
		if _, err := tx.Put(domain.MachineKind, r.ID, r.Revision, "", "", machine); err != nil {
			return err
		}
		input, err := sessionWorkspaceRequest(tx, domain.NewID(), session)
		if err != nil {
			return err
		}
		if err := queueSessionWorkspace(tx, input.SessionID, &session, input); domain.SafeError(err).Code != domain.Unsupported {
			t.Fatal("capability 19 substituted for named directories", err)
		}
		if session.Preparation != nil {
			t.Fatal("unsupported acceptance published preparation")
		}
		return nil
	})
}
