// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	"path/filepath"
	"testing"
	"time"
)

func TestDeferredRepositoryNamesRecheckAtAtomicPublication(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	machine := domain.NewID()
	_, err = db.Mutate(ctx, domain.NewID(), "fixture.machine", nil, func(tx *store.Tx) (any, error) {
		return tx.Put(domain.MachineKind, machine, 0, "", "", domain.Machine{Name: "fixture", OS: "linux", Architecture: "amd64", Version: "0.1.0"})
	})
	if err != nil {
		t.Fatal(err)
	}
	parents, children := []store.Record{}, []store.Record{}
	for _, name := range []string{"Alpha", " alpha "} {
		raw, _ := json.Marshal(domain.Repository{Name: name, RemoteURL: "https://example.test/repo.git", AutoFetch: true, Checkouts: []domain.Checkout{{MachineID: machine, Path: "/fixture/source"}}})
		accepted, err := SaveConfiguration(ctx, db, ConfigurationMutation{RequestID: domain.NewID(), Kind: domain.RepositoryKind, Document: raw})
		if err != nil {
			t.Fatal(err)
		}
		var parent store.Record
		if domain.Decode(accepted.Data, &parent) != nil {
			t.Fatal("missing parent")
		}
		parents = append(parents, parent)
		if err = db.Read(ctx, func(tx *store.Tx) error {
			rows, e := tx.Jobs("", parent.ID, "", "", 10)
			if e != nil {
				return e
			}
			if len(rows) != 1 {
				t.Fatalf("unexpected inspections: %d", len(rows))
			}
			children = append(children, rows[0])
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	for i, child := range children {
		request := domain.NewID()
		finish := func(tx *store.Tx) (any, error) {
			job, e := store.Decode[domain.Job](child)
			if e != nil {
				return nil, e
			}
			now := time.Now().UTC()
			job.State, job.FinishedAt = domain.JobSucceeded, &now
			job.Output, _ = json.Marshal(workspace.Inspection{Root: "/fixture/canonical", Name: "repo", Remotes: []string{"origin"}, DefaultRefs: map[string]string{}})
			saved, e := tx.PutJob(child.ID, child.Revision, "", "", job)
			if e != nil {
				return nil, e
			}
			return saved, finishRepositorySave(tx, job.ParentID)
		}
		if _, err = db.Mutate(ctx, request, "fixture.name.report", child.ID, finish); err != nil {
			t.Fatal(err)
		}
		replay, e := db.Mutate(ctx, request, "fixture.name.report", child.ID, finish)
		if e != nil || !replay.Replayed {
			t.Fatal("original completion replay failed", e)
		}
		row, e := db.Get(ctx, domain.JobKind, parents[i].ID)
		if e != nil {
			t.Fatal(e)
		}
		job, e := store.Decode[domain.Job](row)
		if e != nil {
			t.Fatal(e)
		}
		if i == 0 && job.State != domain.JobSucceeded {
			t.Fatal("first publication failed", job.Problem)
		}
		if i == 1 && (job.State != domain.JobFailed || job.Problem == nil || job.Problem.Cause != domain.ConfigurationNameConflictCause) {
			t.Fatal("collision did not settle parent", job.Problem)
		}
		reported, e := db.Get(ctx, domain.JobKind, child.ID)
		if e != nil {
			t.Fatal(e)
		}
		reportedJob, _ := store.Decode[domain.Job](reported)
		if reportedJob.State != domain.JobSucceeded {
			t.Fatal("collision rolled back successful inspection")
		}
	}
	rows, err := db.List(ctx, store.Filter{Kind: domain.RepositoryKind, Limit: 10})
	if err != nil || len(rows) != 1 {
		t.Fatal("both names published", err)
	}
}

func TestConfigurationImportNamesRejectBundleTargetAndPostPreviewCollisions(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repository := func(name string) domain.ConfigurationEntry {
		return transferEntry(domain.RepositoryKind, domain.Repository{Name: name, RemoteURL: "https://example.test/repo.git", AutoFetch: true})
	}
	selection := func(entries ...domain.ConfigurationEntry) domain.ConfigurationImportSelection {
		return domain.ConfigurationImportSelection{Bundle: domain.ConfigurationBundle{Version: domain.ConfigurationBundleVersion, Entries: entries}}
	}
	preview := func(input domain.ConfigurationImportSelection) (domain.ConfigurationImportPlan, error) {
		var plan domain.ConfigurationImportPlan
		err := db.Read(ctx, func(tx *store.Tx) error { var e error; plan, e = buildConfigurationPlan(tx, input); return e })
		return plan, err
	}
	if _, err := preview(selection(repository("Straße"), repository("STRASSE"))); err == nil || domain.SafeError(err).Cause != domain.ConfigurationNameConflictCause {
		t.Fatal("intra-bundle collision accepted", err)
	}
	project := transferEntry(domain.ProjectKind, domain.Project{Name: "Alpha", Repositories: []domain.ID{}, PrimaryRepository: ""})
	// Project validation needs a repository; the valid cross-kind fixture below
	// names an explicitly imported repository by source ID, never by its name.
	repo := repository("Alpha")
	project.Document, _ = json.Marshal(domain.Project{Name: "Alpha", Repositories: []domain.ID{repo.ID}, PrimaryRepository: repo.ID})
	if _, err := preview(selection(repo, project)); err != nil {
		t.Fatal("cross-kind names collided", err)
	}
	template := transferEntry(domain.TemplateKind, domain.Template{Name: "Independent template", Contents: "Retained original instructions."})
	plan, err := preview(selection(repository("Alpha"), template))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Mutate(ctx, domain.NewID(), "fixture.competing-name", nil, func(tx *store.Tx) (any, error) {
		return tx.Put(domain.RepositoryKind, domain.NewID(), 0, "", "", domain.Repository{Name: " ALPHA ", RemoteURL: "https://example.test/other.git", AutoFetch: true})
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := preview(selection(repository("alpha"))); err == nil || domain.SafeError(err).Cause != domain.ConfigurationNameConflictCause {
		t.Fatal("target collision preview accepted", err)
	}
	_, err = db.Mutate(ctx, domain.NewID(), "fixture.apply-name-plan", plan, func(tx *store.Tx) (any, error) { return writeConfigurationImport(tx, plan) })
	if err == nil || domain.SafeError(err).Cause != domain.ConfigurationNameConflictCause {
		t.Fatal("application did not recheck target", err)
	}
	for _, change := range plan.Changes {
		if _, err = db.Get(ctx, change.Kind, change.ID); domain.SafeError(err).Code != domain.NotFound {
			t.Fatal("partial import published", err)
		}
	}
}

func TestDeferredConfigurationImportNameCollisionSettlesOriginalJobs(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	machine, parentID, childID, target := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
	_, err = db.Mutate(ctx, domain.NewID(), "fixture.import-name-machine", nil, func(tx *store.Tx) (any, error) {
		return tx.Put(domain.MachineKind, machine, 0, "", "", domain.Machine{Name: "target", OS: "linux", Architecture: "amd64", Version: "0.1.0"})
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(domain.Repository{Name: "Alpha", RemoteURL: "https://example.test/repo.git", AutoFetch: true, Checkouts: []domain.Checkout{{MachineID: machine, Path: "/mapped"}}})
	plan := domain.ConfigurationImportPlan{Version: domain.ConfigurationBundleVersion, Changes: []domain.ConfigurationChange{{SourceID: domain.NewID(), ID: target, Kind: domain.RepositoryKind, Action: domain.ConfigurationCreate, After: raw}}, Machines: []domain.ConfigurationTargetMachine{{ID: machine, Name: "target", OS: "linux", Architecture: "amd64"}}}
	if err = db.Read(ctx, func(tx *store.Tx) error { return validateConfigurationPlan(tx, plan) }); err != nil {
		t.Fatal("valid preview failed", err)
	}
	input, _ := json.Marshal(configurationImportJob{Actor: domain.Principal{Type: domain.OwnerDevice}, Plan: plan, Inspections: []configurationImportInspection{{ID: childID, RepositoryID: target, MachineID: machine, Path: "/mapped"}}})
	output, _ := json.Marshal(workspace.Inspection{Root: "/mapped", Name: "repo"})
	inspectionInput, _ := json.Marshal(domain.RepositoryInspectionInput{Path: "/mapped"})
	now := time.Now().UTC()
	_, err = db.Mutate(ctx, domain.NewID(), "fixture.accept-import-name", nil, func(tx *store.Tx) (any, error) {
		if _, e := tx.PutJob(parentID, 0, "", "", domain.Job{Type: domain.ImportConfigurationJob, State: domain.JobQueued, Input: input, AcceptedAt: now}); e != nil {
			return nil, e
		}
		return tx.PutJob(childID, 0, "", "", domain.Job{Type: domain.InspectRepositoryJob, State: domain.JobSucceeded, MachineID: machine, ParentID: parentID, Input: inspectionInput, Output: output, AcceptedAt: now, FinishedAt: &now})
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Mutate(ctx, domain.NewID(), "fixture.import-name-winner", nil, func(tx *store.Tx) (any, error) {
		return tx.Put(domain.RepositoryKind, domain.NewID(), 0, "", "", domain.Repository{Name: "ALPHA", RemoteURL: "https://example.test/winner.git", AutoFetch: true})
	})
	if err != nil {
		t.Fatal(err)
	}
	reportID := domain.NewID()
	finish := func(tx *store.Tx) (any, error) {
		row, e := tx.Get(domain.JobKind, parentID)
		if e != nil {
			return nil, e
		}
		job, e := store.Decode[domain.Job](row)
		if e != nil {
			return nil, e
		}
		return nil, finishConfigurationImport(tx, row, job)
	}
	if _, err = db.Mutate(ctx, reportID, "fixture.finish-import-name", parentID, finish); err != nil {
		t.Fatal("terminal name refusal rolled back report", err)
	}
	row, err := db.Get(ctx, domain.JobKind, parentID)
	if err != nil {
		t.Fatal(err)
	}
	job, _ := store.Decode[domain.Job](row)
	if job.State != domain.JobFailed || job.Problem == nil || job.Problem.Cause != domain.ConfigurationNameConflictCause {
		t.Fatal("original import not settled", job.Problem)
	}
	child, err := db.Get(ctx, domain.JobKind, childID)
	if err != nil {
		t.Fatal(err)
	}
	childJob, _ := store.Decode[domain.Job](child)
	if childJob.State != domain.JobSucceeded || child.Revision != 1 {
		t.Fatal("inspection outcome changed")
	}
	if _, err = db.Get(ctx, domain.RepositoryKind, target); domain.SafeError(err).Code != domain.NotFound {
		t.Fatal("failed import published configuration")
	}
	replay, err := db.Mutate(ctx, reportID, "fixture.finish-import-name", parentID, finish)
	if err != nil || !replay.Replayed {
		t.Fatal("settlement replay changed", err)
	}
}
