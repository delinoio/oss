package server

import (
	"slices"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

func (f *scheduleDispatchFixture) renameRepository(t *testing.T, id domain.ID, name string) {
	t.Helper()
	f.mutate(t, func(tx *store.Tx) error {
		r, err := tx.Get(domain.RepositoryKind, id)
		if err != nil {
			return err
		}
		repo, err := store.Decode[domain.Repository](r)
		if err != nil {
			return err
		}
		repo.Name = name
		_, err = tx.Put(r.Kind, r.ID, r.Revision, "", "", repo)
		return err
	})
}

// addRepository registers another project repository and returns its identity.
func (f *scheduleDispatchFixture) addRepository(t *testing.T, name string) domain.ID {
	t.Helper()
	id := domain.NewID()
	f.mutate(t, func(tx *store.Tx) error {
		repo := domain.Repository{RemoteURL: "https://github.com/fixture/" + name + ".git", Name: name, Base: domain.Reference{Type: domain.LocalBranch, Name: "main"}, Starting: domain.Reference{Type: domain.LocalBranch, Name: "main"}}
		if _, err := tx.Put(domain.RepositoryKind, id, 0, "", "", repo); err != nil {
			return err
		}
		r, err := tx.Get(domain.ProjectKind, f.project)
		if err != nil {
			return err
		}
		project, err := store.Decode[domain.Project](r)
		if err != nil {
			return err
		}
		project.Repositories = append(project.Repositories, id)
		_, err = tx.Put(r.Kind, r.ID, r.Revision, "", "", project)
		return err
	})
	return id
}

func (f *scheduleDispatchFixture) setCapabilities(t *testing.T, capabilities ...domain.WorkerCapability) {
	t.Helper()
	f.mutate(t, func(tx *store.Tx) error {
		r, err := tx.Get(domain.MachineKind, f.machine)
		if err != nil {
			return err
		}
		machine, err := store.Decode[domain.Machine](r)
		if err != nil {
			return err
		}
		machine.WorkerCapabilities = capabilities
		_, err = tx.Put(r.Kind, r.ID, r.Revision, "", "", machine)
		return err
	})
}

// preparedNames returns repository ID to directory name from the queued job.
func (f *scheduleDispatchFixture) preparedNames(t *testing.T, sessionID domain.ID) map[domain.ID]string {
	t.Helper()
	session, err := store.Decode[domain.Session](f.record(t, domain.SessionKind, sessionID))
	if err != nil || session.Preparation == nil {
		t.Fatal("missing preparation", err)
	}
	job, err := store.Decode[domain.Job](f.record(t, domain.JobKind, session.Preparation.JobID))
	var input workspace.PrepareRequest
	if err != nil || domain.Decode(job.Input, &input) != nil {
		t.Fatal("invalid preparation job", err)
	}
	names := map[domain.ID]string{}
	for _, repo := range input.Repositories {
		names[repo.ID] = repo.DirectoryName
	}
	return names
}

func (f *scheduleDispatchFixture) restart(t *testing.T) {
	t.Helper()
	if err := f.service.Store.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(f.ctx, f.root)
	if err != nil {
		t.Fatal(err)
	}
	f.service.Store = db
}

func TestScheduleWaitOccurrenceKeepsAcceptedDirectoryNames(t *testing.T) {
	for _, scenario := range []struct {
		name    string
		restart bool
		// rename is the new display name for the first repository. The
		// collision name maps to the same portable directory as another repository.
		rename string
	}{
		{name: "rename", rename: "beta"},
		{name: "collision", rename: "Other."},
		{name: "restart", rename: "beta", restart: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			f := newScheduleDispatchFixture(t, domain.ScheduleWaitOverlap, false)
			other := f.addRepository(t, "Other")
			firstRecord, first := f.accept(t, domain.ManualOccurrence)
			secondRecord, second := f.accept(t, domain.ManualOccurrence)
			if second.State != domain.OccurrenceWaiting {
				t.Fatal("second occurrence did not wait")
			}
			want := []domain.OccurrenceDirectoryName{{RepositoryID: f.repository, Name: "Fixture"}, {RepositoryID: other, Name: "Other"}}
			if !slices.Equal(second.DirectoryNames, want) {
				t.Fatalf("accepted names were not retained: %+v", second.DirectoryNames)
			}
			f.renameRepository(t, f.repository, scenario.rename)
			f.stop(t, first.SessionID)
			f.reconcile(t, firstRecord.ID)
			if scenario.restart {
				f.restart(t)
				f.mutate(t, func(tx *store.Tx) error { return tx.SetWorkerInstance(f.machine, f.instance, f.now) })
			}
			released := f.reconcile(t, secondRecord.ID)
			if released.State != domain.OccurrenceActive || !slices.Equal(released.DirectoryNames, want) {
				t.Fatalf("release did not use accepted names: %+v", released)
			}
			names := f.preparedNames(t, released.SessionID)
			if names[f.repository] != "Fixture" || names[other] != "Other" {
				t.Fatalf("released job re-derived names: %+v", names)
			}
		})
	}
}

func TestScheduleWaitOccurrenceWithoutFrozenNamesDerivesFromRegistration(t *testing.T) {
	f := newScheduleDispatchFixture(t, domain.ScheduleWaitOverlap, false)
	firstRecord, first := f.accept(t, domain.ManualOccurrence)
	secondRecord, second := f.accept(t, domain.ManualOccurrence)
	if len(second.DirectoryNames) != 1 {
		t.Fatal("new waiting occurrence did not retain names")
	}
	// Represent an occurrence accepted before this field existed.
	f.mutate(t, func(tx *store.Tx) error {
		r, err := tx.Get(domain.OccurrenceKind, secondRecord.ID)
		if err != nil {
			return err
		}
		value, err := store.Decode[domain.ScheduleOccurrence](r)
		if err != nil {
			return err
		}
		value.DirectoryNames = nil
		_, err = tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, value)
		return err
	})
	f.renameRepository(t, f.repository, "Historical")
	f.stop(t, first.SessionID)
	f.reconcile(t, firstRecord.ID)
	released := f.reconcile(t, secondRecord.ID)
	if released.State != domain.OccurrenceActive || f.preparedNames(t, released.SessionID)[f.repository] != "Historical" {
		t.Fatalf("historical occurrence did not derive current registration name: %+v", released)
	}
}

func TestScheduleWaitOccurrenceFailsClosedForRepositoryAddedAfterAcceptance(t *testing.T) {
	f := newScheduleDispatchFixture(t, domain.ScheduleWaitOverlap, false)
	firstRecord, first := f.accept(t, domain.ManualOccurrence)
	secondRecord, _ := f.accept(t, domain.ManualOccurrence)
	f.addRepository(t, "Late")
	f.stop(t, first.SessionID)
	f.reconcile(t, firstRecord.ID)
	released := f.reconcile(t, secondRecord.ID)
	if released.State != domain.OccurrenceFailed || released.Problem == nil || released.Problem.Code != domain.InvalidArgument || released.SessionID != "" {
		t.Fatalf("late repository did not fail closed: %+v", released)
	}
}

func TestScheduleWaitRequiresNamedDirectoryCapabilityAtAcceptance(t *testing.T) {
	downgraded := []domain.WorkerCapability{domain.RemoteWorkspaceCloneV1, domain.InlineModelExecutionV1, domain.ExecutionStartupV1, domain.BranchPrefixInstructionsV1}
	t.Run("worktree is not accepted as waiting", func(t *testing.T) {
		f := newScheduleDispatchFixture(t, domain.ScheduleWaitOverlap, false)
		firstRecord, first := f.accept(t, domain.ManualOccurrence)
		f.setCapabilities(t, downgraded...)
		_, second := f.accept(t, domain.ManualOccurrence)
		if second.State != domain.OccurrenceFailed || second.Problem == nil || second.Problem.Code != domain.Unsupported || second.Reason != domain.SelectionFailedOccurrence || second.SessionID != "" || len(second.DirectoryNames) != 0 {
			t.Fatalf("unsupported Worker was accepted as waiting: %+v", second)
		}
		if _, waiting, err := waitingHead(f); err != nil || waiting {
			t.Fatal("unsupported occurrence is still waiting", err)
		}
		// Restoring support lets a new occurrence wait and release exactly once.
		f.setCapabilities(t, append([]domain.WorkerCapability{domain.NamedWorkspaceDirectoriesV1}, downgraded...)...)
		thirdRecord, third := f.accept(t, domain.ManualOccurrence)
		if third.State != domain.OccurrenceWaiting {
			t.Fatal("supported occurrence did not wait")
		}
		f.stop(t, first.SessionID)
		f.reconcile(t, firstRecord.ID)
		released := f.reconcile(t, thirdRecord.ID)
		if released.State != domain.OccurrenceActive || released.SessionID == "" {
			t.Fatalf("supported wait did not release once: %+v", released)
		}
	})
	t.Run("accepted wait keeps release-time check", func(t *testing.T) {
		f := newScheduleDispatchFixture(t, domain.ScheduleWaitOverlap, false)
		firstRecord, first := f.accept(t, domain.ManualOccurrence)
		secondRecord, second := f.accept(t, domain.ManualOccurrence)
		if second.State != domain.OccurrenceWaiting {
			t.Fatal("second occurrence did not wait")
		}
		f.setCapabilities(t, downgraded...)
		f.stop(t, first.SessionID)
		f.reconcile(t, firstRecord.ID)
		if err := f.service.dispatchScheduleOccurrence(f.ctx, f.record(t, domain.OccurrenceKind, secondRecord.ID), f.now); err == nil {
			t.Fatal("release ignored the unsupported Worker")
		}
		if _, current := f.occurrence(t, secondRecord.ID); current.State != domain.OccurrenceWaiting {
			t.Fatalf("accepted wait did not remain waiting: %+v", current)
		}
	})
	t.Run("local wait is unaffected", func(t *testing.T) {
		f := newScheduleDispatchFixture(t, domain.ScheduleWaitOverlap, true)
		f.setCapabilities(t, downgraded...)
		_, first := f.accept(t, domain.ManualOccurrence)
		_, second := f.accept(t, domain.ManualOccurrence)
		if first.State != domain.OccurrenceActive || second.State != domain.OccurrenceWaiting || len(second.DirectoryNames) != 0 {
			t.Fatalf("Local selection changed: %+v %+v", first, second)
		}
	})
	t.Run("skip overlap is unaffected", func(t *testing.T) {
		f := newScheduleDispatchFixture(t, domain.ScheduleSkipOverlap, false)
		_, first := f.accept(t, domain.ManualOccurrence)
		f.setCapabilities(t, downgraded...)
		_, second := f.accept(t, domain.ManualOccurrence)
		if first.State != domain.OccurrenceActive || second.State != domain.OccurrenceSkipped || second.Reason != domain.OverlapSkippedOccurrence {
			t.Fatalf("Skip overlap changed: %+v %+v", first, second)
		}
	})
}

func waitingHead(f *scheduleDispatchFixture) (store.Record, bool, error) {
	var (
		record store.Record
		found  bool
	)
	err := f.service.Store.Read(f.ctx, func(tx *store.Tx) error {
		var err error
		record, found, err = tx.FirstWaitingOccurrence(f.schedule)
		return err
	})
	return record, found, err
}
