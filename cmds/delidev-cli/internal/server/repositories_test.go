package server

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

func TestRepositoryValidationIsAtomicAcrossWorkersAndRevisions(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	machines := []domain.ID{domain.NewID(), domain.NewID()}
	for _, id := range machines {
		_, err := db.Mutate(ctx, domain.NewID(), "fixture.machine", nil, func(tx *store.Tx) (any, error) {
			return tx.Put(domain.MachineKind, id, 0, "", "", domain.Machine{Name: "fixture", OS: "linux", Architecture: "amd64", Version: "0.1.0"})
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	configuration := domain.Repository{Name: "repo", PreferredRemote: "origin", AutoFetch: true, Checkouts: []domain.Checkout{{MachineID: machines[0], Path: "/tmp/one/sub"}, {MachineID: machines[1], Path: "/tmp/two/sub"}}}
	raw, _ := json.Marshal(configuration)
	accepted := func(id domain.ID, revision uint64) (store.Record, []store.Record) {
		t.Helper()
		result, err := SaveConfiguration(ctx, db, ConfigurationMutation{RequestID: domain.NewID(), ID: id, ExpectedRevision: revision, Kind: domain.RepositoryKind, Document: raw})
		if err != nil {
			t.Fatal(err)
		}
		var parent store.Record
		if err := domain.Decode(result.Data, &parent); err != nil {
			t.Fatal(err)
		}
		var children []store.Record
		err = db.Read(ctx, func(tx *store.Tx) error {
			var err error
			children, err = tx.Jobs("", parent.ID, "", "", 10)
			return err
		})
		if err != nil || len(children) != 2 {
			t.Fatalf("child jobs: %v %v", children, err)
		}
		return parent, children
	}
	finish := func(record store.Record, problem *domain.Error) {
		t.Helper()
		_, err := db.Mutate(ctx, domain.NewID(), "fixture.finish", nil, func(tx *store.Tx) (any, error) {
			job, err := store.Decode[domain.Job](record)
			if err != nil {
				return nil, err
			}
			now := time.Now().UTC()
			job.FinishedAt = &now
			if problem != nil {
				job.State, job.Problem = domain.JobFailed, problem
			} else {
				job.State = domain.JobSucceeded
				job.Output, _ = json.Marshal(workspace.Inspection{Root: "/tmp/canonical", Name: "repo", Remotes: []string{"origin"}, DefaultRefs: map[string]string{}})
			}
			saved, err := tx.PutJob(record.ID, record.Revision, "", "", job)
			if err != nil {
				return nil, err
			}
			if err := finishRepositorySave(tx, job.ParentID); err != nil {
				return nil, err
			}
			return saved, nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	assertCount := func(count int) {
		t.Helper()
		records, err := db.List(ctx, store.Filter{Kind: domain.RepositoryKind, Limit: 10})
		if err != nil || len(records) != count {
			t.Fatalf("repository count want %d: %d %v", count, len(records), err)
		}
	}
	parent, children := accepted("", 0)
	finish(children[0], nil)
	assertCount(0)
	finish(children[1], domain.Fail(domain.InvalidArgument, "Remote disappeared.", "Reinspect."))
	assertCount(0)
	failed, err := db.Get(ctx, domain.JobKind, parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	failure, _ := store.Decode[domain.Job](failed)
	if failure.State != domain.JobFailed {
		t.Fatal("parent did not retain validation failure")
	}
	parent, children = accepted("", 0)
	finish(children[0], nil)
	assertCount(0)
	finish(children[1], nil)
	assertCount(1)
	completed, err := db.Get(ctx, domain.JobKind, parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	job, _ := store.Decode[domain.Job](completed)
	var output repositorySaveOutput
	if err := domain.Decode(job.Output, &output); err != nil {
		t.Fatal(err)
	}
	if job.State != domain.JobSucceeded || output.Revision != 1 {
		t.Fatal("parent/repository did not commit together")
	}
	edit, children := accepted(output.ID, 1)
	_, err = db.Mutate(ctx, domain.NewID(), "fixture.concurrent-edit", nil, func(tx *store.Tx) (any, error) {
		configuration.Name = "newer"
		return tx.Put(domain.RepositoryKind, output.ID, 1, "", "", configuration)
	})
	if err != nil {
		t.Fatal(err)
	}
	finish(children[0], nil)
	finish(children[1], nil)
	record, err := db.Get(ctx, domain.RepositoryKind, output.ID)
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := store.Decode[domain.Repository](record)
	if record.Revision != 2 || stored.Name != "newer" {
		t.Fatal("late validation overwrote a concurrent edit")
	}
	record, err = db.Get(ctx, domain.JobKind, edit.ID)
	if err != nil {
		t.Fatal(err)
	}
	job, _ = store.Decode[domain.Job](record)
	if job.Problem == nil || job.Problem.Code != domain.Conflict {
		t.Fatal("stale asynchronous revision was not surfaced")
	}
}
