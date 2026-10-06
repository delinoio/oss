// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

func TestRemoteRepositoryRegistrationWithoutMachineAndPortableImport(t *testing.T) {
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	input := domain.Repository{RemoteURL: "git@github.com:fixture/project.git", Checkouts: []domain.Checkout{}, AutoFetch: true}
	raw, _ := json.Marshal(input)
	result, err := SaveConfiguration(ctx, db, ConfigurationMutation{RequestID: domain.NewID(), Kind: domain.RepositoryKind, Document: raw})
	if err != nil {
		t.Fatal(err)
	}
	var accepted store.Record
	if domain.Decode(result.Data, &accepted) != nil {
		t.Fatal("no durable save result")
	}
	job, err := store.Decode[domain.Job](accepted)
	if err != nil || job.State != domain.JobSucceeded {
		t.Fatal("URL-only job did not finish", err, job.State)
	}
	var saved repositorySaveOutput
	if domain.Decode(job.Output, &saved) != nil {
		t.Fatal("no repository receipt")
	}
	record, err := db.Get(ctx, domain.RepositoryKind, saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := store.Decode[domain.Repository](record)
	if err != nil || repository.Name != "project" || repository.RemoteURL != input.RemoteURL || len(repository.Checkouts) != 0 {
		t.Fatal("source changed", err)
	}
	projectRaw, _ := json.Marshal(domain.Project{Name: "Remote project", Repositories: []domain.ID{record.ID}, PrimaryRepository: record.ID})
	if _, err := SaveConfiguration(ctx, db, ConfigurationMutation{RequestID: domain.NewID(), Kind: domain.ProjectKind, Document: projectRaw}); err != nil {
		t.Fatal("project connection required a machine", err)
	}
	var bundle domain.ConfigurationBundle
	if err := db.Read(ctx, func(tx *store.Tx) error { var err error; bundle, err = exportConfiguration(tx); return err }); err != nil {
		t.Fatal(err)
	}
	if len(bundle.Machines) != 0 {
		t.Fatal("URL-only export acquired machine mapping")
	}
	entries := []domain.ConfigurationEntry{}
	for _, entry := range bundle.Entries {
		if entry.Kind == domain.RepositoryKind || entry.Kind == domain.ProjectKind {
			entries = append(entries, entry)
		}
	}
	bundle.Entries = entries
	selection := domain.ConfigurationImportSelection{Bundle: bundle}
	if err := db.Read(ctx, func(tx *store.Tx) error { _, err := buildConfigurationPlan(tx, selection); return err }); err != nil {
		t.Fatal("URL-only import needs mappings", err)
	}
	missing, _ := json.Marshal(domain.Repository{Name: "legacy", Checkouts: []domain.Checkout{{MachineID: domain.NewID(), Path: "/old"}}})
	if _, err := SaveConfiguration(ctx, db, ConfigurationMutation{RequestID: domain.NewID(), Kind: domain.RepositoryKind, Document: missing}); err == nil {
		t.Fatal("new save accepted absent URL")
	}
	legacy := domain.Repository{Name: "historical", Checkouts: []domain.Checkout{{MachineID: domain.NewID(), Path: "/old"}}}
	if err := legacy.Validate(); err != nil {
		t.Fatal("historical decode was removed", err)
	}
}

func TestRemoteWorkspaceSelectionPinsURLAndIgnoresCheckout(t *testing.T) {
	f := newScheduleDispatchFixture(t, domain.ScheduleAllowOverlap, false)
	f.mutate(t, func(tx *store.Tx) error {
		r, err := tx.Get(domain.RepositoryKind, f.repository)
		if err != nil {
			return err
		}
		repo, err := store.Decode[domain.Repository](r)
		if err != nil {
			return err
		}
		repo.Checkouts = nil
		_, err = tx.Put(r.Kind, r.ID, r.Revision, "", "", repo)
		return err
	})
	var pinned workspace.PrepareRequest
	if err := f.service.Store.Read(f.ctx, func(tx *store.Tx) error {
		session := domain.Session{AgentID: f.agent, MachineID: f.machine, ProjectID: f.project, Workspace: domain.Worktree}
		var err error
		pinned, err = sessionWorkspaceRequest(tx, domain.NewID(), session)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if pinned.Repositories[0].SourceKind != workspace.RemoteCloneSource || pinned.Repositories[0].Checkout != "" || pinned.Repositories[0].RemoteURL == "" {
		t.Fatal("Worktree retained a local dependency")
	}
}
