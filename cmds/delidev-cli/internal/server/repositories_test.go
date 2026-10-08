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
			return tx.Put(domain.MachineKind, id, 0, "", "", domain.Machine{Name: "fixture", OS: "linux", Architecture: "amd64", Version: "0.1.0", WorkerCapabilities: []domain.WorkerCapability{domain.RepositoryInspectionMetadataV1}})
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	configuration := domain.Repository{RemoteURL: "https://github.com/fixture/repo.git", Name: "repo", PreferredRemote: "origin", AutoFetch: true, Checkouts: []domain.Checkout{{MachineID: machines[0], Path: "/tmp/one/sub"}, {MachineID: machines[1], Path: "/tmp/two/sub"}}}
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

func TestRepositoryValidationOmitsSourceIdentityForLegacyWorkers(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	machine := domain.NewID()
	_, err = db.Mutate(ctx, domain.NewID(), "fixture.machine", nil, func(tx *store.Tx) (any, error) {
		return tx.Put(domain.MachineKind, machine, 0, "", "", domain.Machine{Name: "legacy", OS: "linux", Architecture: "amd64", Version: "0.1.0"})
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(domain.Repository{RemoteURL: "https://github.com/source/repo.git", Name: "repo", Checkouts: []domain.Checkout{{MachineID: machine, Path: "/tmp/repo"}}})
	result, err := SaveConfiguration(ctx, db, ConfigurationMutation{RequestID: domain.NewID(), Kind: domain.RepositoryKind, Document: raw})
	if err != nil {
		t.Fatal(err)
	}
	var parent store.Record
	if err := domain.Decode(result.Data, &parent); err != nil {
		t.Fatal(err)
	}
	var children []store.Record
	if err := db.Read(ctx, func(tx *store.Tx) error {
		var err error
		children, err = tx.Jobs("", parent.ID, "", "", store.MaxPage)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(children) != 1 {
		t.Fatalf("legacy inspection jobs: %d", len(children))
	}
	job, err := store.Decode[domain.Job](children[0])
	if err != nil {
		t.Fatal(err)
	}
	var input domain.RepositoryInspectionInput
	if err := domain.Decode(job.Input, &input); err != nil {
		t.Fatal(err)
	}
	if input.ExpectedRemoteIdentity != "" {
		t.Fatal("legacy Worker received the post-capability source identity")
	}
}

func TestRepositoryTombstonesFenceAdmissionAndSettleOriginalChild(t *testing.T) {
	for _, before := range []bool{true, false} {
		t.Run(map[bool]string{true: "before-admission", false: "after-admission"}[before], func(t *testing.T) {
			ctx := context.Background()
			db, err := store.Open(ctx, filepath.Join(t.TempDir(), "state"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			machine, id := domain.NewID(), domain.NewID()
			_, err = db.Mutate(ctx, domain.NewID(), "fixture.machine", nil, func(tx *store.Tx) (any, error) {
				return tx.Put(domain.MachineKind, machine, 0, "", "", domain.Machine{Name: "fixture", OS: "linux", Architecture: "amd64", Version: "0.1.0", WorkerCapabilities: []domain.WorkerCapability{domain.RepositoryInspectionMetadataV1}})
			})
			if err != nil {
				t.Fatal(err)
			}
			repository := domain.Repository{RemoteURL: "https://github.com/fixture/repo.git", Name: "repo", PreferredRemote: "origin", Checkouts: []domain.Checkout{{MachineID: machine, Path: "/tmp/repo"}}}
			remove := func() {
				t.Helper()
				_, err := db.Mutate(ctx, domain.NewID(), "fixture.deleted.repository", nil, func(tx *store.Tx) (any, error) {
					row, err := tx.Put(domain.RepositoryKind, id, 0, "", "", repository)
					if err != nil {
						return nil, err
					}
					return nil, tx.Delete(domain.RepositoryKind, id, row.Revision)
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			if before {
				remove()
			}
			jobsBefore, err := db.List(ctx, store.Filter{Kind: domain.JobKind, Limit: 10})
			if err != nil {
				t.Fatal(err)
			}
			eventsBefore, err := db.Events(ctx, 0, "", 99)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(repository)
			mutation := ConfigurationMutation{RequestID: domain.NewID(), ID: id, Kind: domain.RepositoryKind, Document: raw}
			otherKind := mutation
			otherKind.ID, otherKind.RequestID = machine, domain.NewID()
			if _, err := SaveConfiguration(ctx, db, otherKind); domain.SafeError(err).Code != domain.InvalidArgument {
				t.Fatal("live other-kind identity classification changed", err)
			}
			accepted, err := SaveConfiguration(ctx, db, mutation)
			if before {
				if domain.SafeError(err).Code != domain.Conflict {
					t.Fatal("tombstone admitted", err)
				}
				jobs, err := db.List(ctx, store.Filter{Kind: domain.JobKind, Limit: 10})
				if err != nil || len(jobs) != len(jobsBefore) {
					t.Fatal("rejected admission created jobs", jobs, err)
				}
				events, err := db.Events(ctx, 0, "", 99)
				if err != nil || len(events) != len(eventsBefore) {
					t.Fatal("rejected admission published events", events, err)
				}
				// Reusing the rejected request for a genuinely unused identity proves no
				// acceptance receipt was written for the permanently deleted target.
				mutation.ID = domain.NewID()
				if _, err = SaveConfiguration(ctx, db, mutation); err != nil {
					t.Fatal("rejected admission retained a receipt", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var parent store.Record
			if err = domain.Decode(accepted.Data, &parent); err != nil {
				t.Fatal(err)
			}
			var children []store.Record
			if err = db.Read(ctx, func(tx *store.Tx) error {
				var err error
				children, err = tx.Jobs("", parent.ID, "", "", 10)
				return err
			}); err != nil || len(children) != 1 {
				t.Fatal(children, err)
			}
			remove()
			_, err = db.Mutate(ctx, domain.NewID(), "fixture.original.inspection", nil, func(tx *store.Tx) (any, error) {
				child, err := store.Decode[domain.Job](children[0])
				if err != nil {
					return nil, err
				}
				child.State = domain.JobSucceeded
				now := time.Now().UTC()
				child.FinishedAt = &now
				child.Output, _ = json.Marshal(workspace.Inspection{Root: "/tmp/canonical", Name: "repo", Remotes: []string{"origin"}, DefaultRefs: map[string]string{}})
				if _, err = tx.PutJob(children[0].ID, children[0].Revision, "", "", child); err != nil {
					return nil, err
				}
				return nil, finishRepositorySave(tx, parent.ID)
			})
			if err != nil {
				t.Fatal("valid original report rolled back", err)
			}
			parentRow, err := db.Get(ctx, domain.JobKind, parent.ID)
			if err != nil {
				t.Fatal(err)
			}
			job, err := store.Decode[domain.Job](parentRow)
			if err != nil || job.State != domain.JobFailed || job.Problem == nil || job.Problem.Code != domain.Conflict {
				t.Fatal("parent not settled", job, err)
			}
			childRow, err := db.Get(ctx, domain.JobKind, children[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			child, err := store.Decode[domain.Job](childRow)
			if err != nil || child.State != domain.JobSucceeded || childRow.Revision != children[0].Revision+1 {
				t.Fatal("child success not committed", child, err)
			}
			if _, err = db.Get(ctx, domain.RepositoryKind, id); domain.SafeError(err).Code != domain.NotFound {
				t.Fatal("repository resurrected", err)
			}
			if err = db.Read(ctx, func(tx *store.Tx) error { return tx.RequireUnusedID(id) }); domain.SafeError(err).Code != domain.Conflict {
				t.Fatal("original tombstone lost", err)
			}
		})
	}
}
