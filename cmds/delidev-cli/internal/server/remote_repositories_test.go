// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/testgit"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func TestRemoteScheduleClonesWithoutCheckoutAndClaimsExecution(t *testing.T) {
	// Native discovery/account state is a private protocol fixture. Real Git
	// preparation and the execution lease prove the scheduled workspace boundary,
	// without invoking an installed harness or an external account.
	f := newFirstDispatchFixtureWorkspaceProfile(t, domain.Codex, domain.PlanMode, "/fixture/codex", "", "fixture-model", domain.Worktree)
	ctx := context.Background()
	projectRecord, err := f.service.Store.Get(ctx, domain.ProjectKind, f.selection.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.Decode[domain.Project](projectRecord)
	if err != nil {
		t.Fatal(err)
	}
	sources := map[string]string{}
	_, err = f.service.Store.Mutate(domain.WithPrincipal(ctx, domain.Principal{Type: domain.OwnerDevice}), domain.NewID(), "fixture.remote-schedule", nil, func(tx *store.Tx) (any, error) {
		for _, id := range project.Repositories {
			row, err := tx.Get(domain.RepositoryKind, id)
			if err != nil {
				return nil, err
			}
			repository, err := store.Decode[domain.Repository](row)
			if err != nil {
				return nil, err
			}
			sources[repository.RemoteURL] = repository.Checkouts[0].Path
			repository.Checkouts = nil
			if _, err := tx.Put(row.Kind, row.ID, row.Revision, "", "", repository); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	client := delidevv1connect.NewScheduleServiceClient(http.DefaultClient, f.endpoint.URL)
	definition := domain.ScheduleDefinition{Name: "Remote scheduled clone", AgentID: f.selection.AgentID, MachineID: f.selection.MachineID, ProjectID: f.selection.ProjectID, Workspace: domain.Worktree, Mode: domain.PlanMode, Prompt: "scheduled fixture input", Cron: "0 0 1 1 *", Timezone: "UTC", Overlap: domain.ScheduleAllowOverlap}
	raw, _ := json.Marshal(definition)
	saved, err := client.SaveSchedule(ctx, ownerRequest(f.identity, &pb.SaveScheduleRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID())}, SchemaVersion: 1, DefinitionJson: raw}))
	if err != nil {
		t.Fatal(err)
	}
	run, err := client.RunScheduleNow(ctx, ownerRequest(f.identity, &pb.RunScheduleNowRequest{Mutation: acctMutation(saved.Msg.Schedule, domain.NewID())}))
	if err != nil || run.Msg.Session == nil {
		t.Fatal("schedule did not accept a session", err)
	}
	nextJob := func() *pb.Resource {
		for f.workerStream.Receive() {
			if job := f.workerStream.Msg().Job; job != nil {
				return job
			}
		}
		t.Fatal("scheduled work stream ended", f.workerStream.Err())
		return nil
	}
	assigned := nextJob()
	var job domain.Job
	var preparation workspace.PrepareRequest
	if domain.Decode(assigned.DocumentJson, &job) != nil || job.Type != domain.PrepareWorkspaceJob || domain.Decode(job.Input, &preparation) != nil || string(preparation.SessionID) != run.Msg.Session.Id {
		t.Fatal("schedule preparation identity changed")
	}
	for _, repository := range preparation.Repositories {
		if repository.Checkout != "" || repository.SourceKind != workspace.RemoteCloneSource || sources[repository.RemoteURL] == "" {
			t.Fatal("schedule depended on a connected checkout")
		}
	}
	manager := workspace.Manager{Root: f.workerRoot, Git: workspace.Git{Executable: testgit.Executable(t, sources)}}
	manifest, err := manager.Prepare(ctx, preparation)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(manifest)
	if _, err := f.workerClient.ReportWork(ctx, ownerRequest(f.workerIdentity, &pb.ReportWorkRequest{Mutation: acctMutation(assigned, domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, OutputJson: raw})); err != nil {
		t.Fatal(err)
	}
	current, err := f.service.Store.Get(ctx, domain.SessionKind, preparation.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.service.dispatchExecution(ctx, current); err != nil {
		t.Fatal("prepared schedule did not dispatch", err)
	}
	execution := nextJob()
	var input domain.ExecutionJobInput
	if domain.Decode(execution.DocumentJson, &job) != nil || job.Type != domain.ExecuteSessionJob || domain.Decode(job.Input, &input) != nil || input.SessionID != preparation.SessionID || input.Input.Prompt != definition.Prompt {
		t.Fatal("scheduled execution lost its accepted input")
	}
	var accepted workspace.Manifest
	if domain.Decode(input.Manifest, &accepted) != nil || accepted.PrimaryPath != manifest.PrimaryPath {
		t.Fatal("execution did not retain the managed clone")
	}
	lease, err := manager.ClaimFirstExecution(ctx, domain.ID(execution.Id), input.ExecutionID, preparation, accepted)
	if err != nil {
		t.Fatal("scheduled clone was not executable", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
}

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
	destination, secrets := newDoctorFixture(t)
	preview := transferPreview(t, destination, selection)
	requestID := domain.NewID()
	imported := transferApply(t, destination, preview, requestID)
	if imported.State != domain.JobSucceeded || len(imported.Resources) != 2 || len(secrets.refs) != 0 {
		t.Fatal("URL-only import required Worker or credentials", imported)
	}
	if replay := transferApply(t, destination, preview, requestID); replay.State != domain.JobSucceeded || len(replay.Resources) != 2 {
		t.Fatal("import receipt was not reused", replay)
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
	read := func(mode domain.WorkspaceType) (workspace.PrepareRequest, error) {
		var pinned workspace.PrepareRequest
		err := f.service.Store.Read(f.ctx, func(tx *store.Tx) error {
			session := domain.Session{AgentID: f.agent, MachineID: f.machine, ProjectID: f.project, Workspace: mode}
			if mode == domain.Local {
				session.LocalOrigin = &domain.LocalOrigin{MachineID: f.machine, DeviceID: f.device}
			}
			var err error
			pinned, err = sessionWorkspaceRequest(tx, domain.NewID(), session)
			return err
		})
		return pinned, err
	}
	withCheckout, err := read(domain.Worktree)
	if err != nil || withCheckout.Repositories[0].Checkout != "" || withCheckout.Repositories[0].SourceKind != workspace.RemoteCloneSource {
		t.Fatal("Worktree used optional Local folder", err)
	}
	local, err := read(domain.Local)
	if err != nil || local.Repositories[0].Checkout == "" || local.Repositories[0].SourceKind != workspace.LocalCheckoutSource || local.Repositories[0].AutoFetch {
		t.Fatal("Local did not use connected folder as-is", err)
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
		repo.Checkouts = nil
		_, err = tx.Put(r.Kind, r.ID, r.Revision, "", "", repo)
		return err
	})
	pinned, err := read(domain.Worktree)
	if err != nil {
		t.Fatal(err)
	}
	if pinned.Repositories[0].SourceKind != workspace.RemoteCloneSource || pinned.Repositories[0].Checkout != "" || pinned.Repositories[0].RemoteURL == "" {
		t.Fatal("Worktree retained a local dependency")
	}
	if _, err := read(domain.Local); domain.SafeError(err).Code != domain.MissingInput {
		t.Fatal("Local silently switched execution mode", err)
	}
	_, occurrence := f.accept(t, domain.ManualOccurrence)
	session, _ := store.Decode[domain.Session](f.record(t, domain.SessionKind, occurrence.SessionID))
	jobRecord := f.record(t, domain.JobKind, session.Preparation.JobID)
	f.mutate(t, func(tx *store.Tx) error {
		r, err := tx.Get(domain.RepositoryKind, f.repository)
		if err != nil {
			return err
		}
		repository, _ := store.Decode[domain.Repository](r)
		repository.RemoteURL = "https://github.com/fixture/reconfigured.git"
		_, err = tx.Put(r.Kind, r.ID, r.Revision, "", "", repository)
		return err
	})
	job, _ := store.Decode[domain.Job](f.record(t, domain.JobKind, jobRecord.ID))
	var accepted workspace.PrepareRequest
	if domain.Decode(job.Input, &accepted) != nil || accepted.Repositories[0].RemoteURL != pinned.Repositories[0].RemoteURL || accepted.Repositories[0].SourceKind != workspace.RemoteCloneSource {
		t.Fatal("configuration edit changed accepted schedule source")
	}
	f.mutate(t, func(tx *store.Tx) error {
		r, err := tx.Get(domain.MachineKind, f.machine)
		if err != nil {
			return err
		}
		machine, _ := store.Decode[domain.Machine](r)
		machine.WorkerCapabilities = nil
		_, err = tx.Put(r.Kind, r.ID, r.Revision, "", "", machine)
		return err
	})
	if err := f.service.Store.Read(f.ctx, func(tx *store.Tx) error {
		return queueSessionWorkspace(tx, domain.NewID(), &session, pinned)
	}); domain.SafeError(err).Code != domain.Unsupported {
		t.Fatal("unsupported Worker accepted managed clone", err)
	}
	f.mutate(t, func(tx *store.Tx) error {
		r, err := tx.Get(domain.RepositoryKind, f.repository)
		if err != nil {
			return err
		}
		repository, _ := store.Decode[domain.Repository](r)
		repository.RemoteURL = ""
		repository.Checkouts = []domain.Checkout{{MachineID: f.machine, Path: local.Repositories[0].Checkout}}
		_, err = tx.Put(r.Kind, r.ID, r.Revision, "", "", repository)
		return err
	})
	if _, err := read(domain.Local); domain.SafeError(err).Code != domain.InvalidArgument {
		t.Fatal("new Local preparation accepted an absent URL", err)
	}
}

func TestRemoteForkResultRequiresIndependentSourceAndImmutableURL(t *testing.T) {
	for _, kind := range []workspace.RepositorySourceKind{workspace.RemoteCloneSource, workspace.IndependentForkSource, workspace.LocalCheckoutSource} {
		t.Run(string(kind), func(t *testing.T) {
			id := domain.NewID()
			source := workspace.Manifest{PrimaryPath: "/original/repository", Repositories: []workspace.PreparedRepository{{ID: id, SourceKind: kind, RemoteURL: "https://github.com/fixture/repo.git", Path: "/original/repository", Source: "/original/repository"}}}
			raw, _ := json.Marshal(source)
			input := domain.ForkJobInput{Workspace: domain.Worktree}
			input.SourceAssignment.Manifest = raw
			clone, err := workspace.ForkRequiresManagedClone(input)
			if err != nil || !clone {
				t.Fatal("Fork lost managed clone capability gate", err)
			}
			commit := "0123456789012345678901234567890123456789"
			ref := domain.Reference{Type: domain.CommitReference, Name: commit}
			preparation := workspace.PrepareRequest{PrimaryRepository: id, Repositories: []workspace.RepositorySpec{{ID: id, Checkout: source.PrimaryPath, SourceKind: workspace.IndependentForkSource, RemoteURL: source.Repositories[0].RemoteURL, Base: ref, Starting: ref}}}
			result := workspace.Manifest{Repositories: []workspace.PreparedRepository{{ID: id, BaseCommit: commit, StartingCommit: commit}}}
			if err := validateForkWorkspace(input, preparation, result); err != nil {
				t.Fatal("independent Fork result rejected", err)
			}
			preparation.Repositories[0].RemoteURL = "https://github.com/fixture/foreign.git"
			if err := validateForkWorkspace(input, preparation, result); err == nil {
				t.Fatal("Fork rewrote original URL")
			}
			preparation.Repositories[0].RemoteURL = source.Repositories[0].RemoteURL
			preparation.Repositories[0].ForkRegistrationSource = source.PrimaryPath
			if err := validateForkWorkspace(input, preparation, result); err == nil {
				t.Fatal("Fork acquired parent Git lifetime")
			}
		})
	}
}
