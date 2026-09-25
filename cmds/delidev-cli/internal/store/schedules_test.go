package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const dropScheduleFixtureSchema = "DROP TABLE deleted_project_policies; DROP INDEX schedule_due; DROP INDEX occurrence_position; DROP INDEX occurrence_cron_due; DROP INDEX occurrence_pending; DROP INDEX session_schedule; ALTER TABLE worker_instances DROP COLUMN available_since; "

func scheduleFixture(t *testing.T, s *Store) (Record, domain.Schedule) {
	t.Helper()
	definition := domain.ScheduleDefinition{Name: "Periodic checks", Enabled: true, Prompt: "Inspect project", ProjectID: domain.NewID(), AgentID: domain.NewID(), MachineID: domain.NewID(), Workspace: domain.Worktree, Mode: domain.ExecuteMode, Overlap: domain.ScheduleWaitOverlap, Cron: "* * * * *", Timezone: "UTC"}
	due := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
	value := domain.Schedule{Definition: definition, ConfigurationRevision: 1, NextRunAt: &due}
	var r Record
	_, err := s.Mutate(context.Background(), domain.NewID(), "fixture.schedule", nil, func(tx *Tx) (any, error) {
		var err error
		r, err = tx.PutSchedule(domain.NewID(), 0, value)
		return r, err
	})
	if err != nil {
		t.Fatal(err)
	}
	return r, value
}
func appendWaiting(t *testing.T, s *Store, r Record, value domain.Schedule) (Record, Record, domain.Schedule) {
	t.Helper()
	due := *value.NextRunAt
	next, err := value.Definition.NextRun(due)
	if err != nil {
		t.Fatal(err)
	}
	occurrence := domain.ScheduleOccurrence{ScheduleID: r.ID, ConfigurationRevision: value.ConfigurationRevision, Sequence: value.LastOccurrence + 1, Trigger: domain.CronOccurrence, DueAt: due, AcceptedAt: due, Selection: value.Definition.Selection(), LocalOrigin: value.LocalOrigin, Overlap: value.Definition.Overlap, State: domain.OccurrenceWaiting}
	value.NextRunAt = &next
	value.LastOccurrence++
	var accepted Record
	_, err = s.Mutate(context.Background(), domain.NewID(), "fixture.occurrence", nil, func(tx *Tx) (any, error) {
		var err error
		accepted, err = tx.AppendScheduleOccurrence(domain.NewID(), r, value, occurrence)
		return accepted, err
	})
	if err != nil {
		t.Fatal(err)
	}
	current, err := s.Get(context.Background(), domain.ScheduleKind, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	return accepted, current, value
}

func TestScheduleOccurrenceAtomicRetryFIFOAndRetainedHistory(t *testing.T) {
	s, root := openTest(t)
	ctx := context.Background()
	schedule, value := scheduleFixture(t, s)
	first, schedule, value := appendWaiting(t, s, schedule, value)
	second, schedule, value := appendWaiting(t, s, schedule, value)
	if err := s.Read(ctx, func(tx *Tx) error {
		head, ok, err := tx.FirstWaitingOccurrence(schedule.ID)
		if err != nil || !ok || head.ID != first.ID {
			t.Fatal("FIFO head", head, err)
		}
		rows, more, err := tx.OccurrenceHistory(schedule.ID, 0, 1)
		if err != nil || !more || len(rows) != 1 || rows[0].ID != first.ID {
			t.Fatal("history order", rows, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// A failed result mutation must not publish partial timer or sequence state.
	before := schedule
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.rollback", nil, func(tx *Tx) (any, error) {
		next := value
		next.LastOccurrence++
		if _, err := tx.PutSchedule(schedule.ID, schedule.Revision, next); err != nil {
			return nil, err
		}
		return nil, domain.Fail(domain.Canceled, "Fixture rollback.", "")
	})
	if err == nil {
		t.Fatal("fixture did not fail")
	}
	current, err := s.Get(ctx, domain.ScheduleKind, schedule.ID)
	if err != nil || current.Revision != before.Revision || string(current.Data) != string(before.Data) {
		t.Fatal("failed mutation advanced schedule", err)
	}
	// Deleting configuration cannot remove already accepted Wait work/history.
	if _, err := s.Mutate(ctx, domain.NewID(), "fixture.delete", nil, func(tx *Tx) (any, error) { return nil, tx.Delete(domain.ScheduleKind, schedule.ID, schedule.Revision) }); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Read(ctx, func(tx *Tx) error {
		rows, more, err := tx.OccurrenceHistory(schedule.ID, 1, 10)
		if err != nil || more || len(rows) != 1 || rows[0].ID != second.ID {
			t.Fatal("history after deletion/restart", rows, err)
		}
		head, ok, err := tx.FirstWaitingOccurrence(schedule.ID)
		if err != nil || !ok || head.ID != first.ID {
			t.Fatal("Wait lost on restart", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestScheduleConcurrentAcceptanceAndImmutableOccurrence(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	schedule, value := scheduleFixture(t, s)
	request, id := domain.NewID(), domain.NewID()
	due := *value.NextRunAt
	occurrence := domain.ScheduleOccurrence{ScheduleID: schedule.ID, ConfigurationRevision: 1, Sequence: 1, Trigger: domain.CronOccurrence, DueAt: due, AcceptedAt: due, Selection: value.Definition.Selection(), Overlap: domain.ScheduleWaitOverlap, State: domain.OccurrenceWaiting}
	next, _ := value.Definition.NextRun(due)
	value.NextRunAt = &next
	value.LastOccurrence = 1
	var applied atomic.Int32
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Mutate(ctx, request, "fixture.accept", schedule.ID, func(tx *Tx) (any, error) {
				applied.Add(1)
				return tx.AppendScheduleOccurrence(id, schedule, value, occurrence)
			})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if applied.Load() != 1 {
		t.Fatal("duplicate occurrence acceptance", applied.Load())
	}
	record, err := s.Get(ctx, domain.OccurrenceKind, id)
	if err != nil {
		t.Fatal(err)
	}
	finished := due.Add(time.Second)
	failed := occurrence
	failed.State = domain.OccurrenceFailed
	failed.FinishedAt = &finished
	failed.Problem = domain.Fail(domain.NotFound, "Original project removed.", "Reconfigure future schedules.")
	failed.Reason = domain.SelectionFailedOccurrence
	altered := failed
	altered.Selection.Prompt = "changed after acceptance"
	if _, err := s.Mutate(ctx, domain.NewID(), "fixture.alter", nil, func(tx *Tx) (any, error) { return tx.UpdateScheduleOccurrence(record, altered) }); err == nil {
		t.Fatal("accepted occurrence selection changed")
	}
	if _, err := s.Mutate(ctx, domain.NewID(), "fixture.finish", nil, func(tx *Tx) (any, error) { return tx.UpdateScheduleOccurrence(record, failed) }); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Mutate(ctx, domain.NewID(), "fixture.repeat", nil, func(tx *Tx) (any, error) { return tx.UpdateScheduleOccurrence(record, failed) }); err == nil {
		t.Fatal("stale completion accepted")
	}
}

func TestScheduleMigrationPreservesV10InboxAndRefusesUnknownScheduleState(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		t.Run(map[bool]string{false: "preserve", true: "unrecognized"}[foreign], func(t *testing.T) {
			s, root := openTest(t)
			ctx := context.Background()
			// An existing inbox must not be re-backfilled or rejected by v10 -> v11.
			inbox := domain.NewID()
			_, err := s.Mutate(ctx, domain.NewID(), "fixture.inbox", nil, func(tx *Tx) (any, error) {
				return tx.Put(domain.InboxKind, inbox, 0, "", "", map[string]string{"retained": "v10"})
			})
			if err != nil {
				t.Fatal(err)
			}
			if foreign {
				if _, err := s.Mutate(ctx, domain.NewID(), "fixture.unknown", nil, func(tx *Tx) (any, error) {
					return tx.Put(domain.ScheduleKind, domain.NewID(), 0, "", "", map[string]string{"legacy": "unproven"})
				}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.db.Exec(dropScheduleFixtureSchema + "PRAGMA user_version=10"); err != nil {
				t.Fatal(err)
			}
			s.Close()
			migrated, err := Open(ctx, root)
			if foreign {
				if err == nil {
					migrated.Close()
					t.Fatal("unknown ownership migrated")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				retained, err := migrated.Get(ctx, domain.InboxKind, inbox)
				if err != nil || retained.Revision != 1 {
					t.Fatal("inbox changed", err)
				}
				migrated.Close()
			}
			backups, err := filepath.Glob(filepath.Join(root, "backups", "*.sqlite"))
			if err != nil || len(backups) != 1 {
				t.Fatal("missing migration backup", err)
			}
			for _, path := range []string{backups[0], filepath.Join(root, "state.sqlite")} {
				db, err := sql.Open("sqlite", databaseURI(path, true))
				if err != nil {
					t.Fatal(err)
				}
				var version, count int
				if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
					t.Fatal(err)
				}
				if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE name='occurrence_position'").Scan(&count); err != nil {
					t.Fatal(err)
				}
				db.Close()
				want, wantCount := 10, 0
				if path != backups[0] && !foreign {
					want, wantCount = SchemaVersion, 1
				}
				if version != want || count != wantCount {
					t.Fatal("migration/rollback/backup mismatch", version, count)
				}
			}
		})
	}
}

func TestWorkerScheduleAvailabilityCannotBackdateReconnectOrLeaseGap(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	machine, instance := domain.NewID(), domain.NewID()
	start := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
	set := func(at time.Time, id domain.ID) {
		t.Helper()
		_, err := s.Mutate(ctx, domain.NewID(), "fixture.lease", nil, func(tx *Tx) (any, error) {
			if _, err := tx.Get(domain.MachineKind, machine); domain.SafeError(err).Code == domain.NotFound {
				if _, err := tx.Put(domain.MachineKind, machine, 0, "", "", domain.Machine{}); err != nil {
					return nil, err
				}
			}
			return nil, tx.SetWorkerInstance(machine, id, at)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	check := func(due, now time.Time, want bool) {
		t.Helper()
		if err := s.Read(ctx, func(tx *Tx) error {
			got, err := tx.WorkerAvailableAt(machine, due, now)
			if err != nil || got != want {
				t.Fatal("availability mismatch", got, want, err)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	set(start, instance)
	check(start, start, true)
	check(start.Add(-time.Second), start, false)
	check(start.Add(time.Minute), start.Add(time.Minute), false)
	set(start.Add(30*time.Second), instance)
	check(start, start.Add(35*time.Second), true)
	// Reopening within the lease window cannot carry old availability into
	// the new server process, even when the same Worker instance reconnects.
	root := s.Root()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	var err error
	s, err = Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	check(start.Add(32*time.Second), start.Add(35*time.Second), false)
	set(start.Add(35*time.Second), instance)
	check(start.Add(32*time.Second), start.Add(35*time.Second), false)
	check(start.Add(35*time.Second), start.Add(36*time.Second), true)
	set(start.Add(2*time.Minute), instance)
	check(start.Add(time.Minute), start.Add(2*time.Minute), false)
	set(start.Add(3*time.Minute), domain.NewID())
	check(start.Add(2*time.Minute), start.Add(3*time.Minute), false)
	set(start.Add(-time.Minute), instance)
	check(start, start, false)
}

func TestScheduleCronUniquenessRollsBackRewoundTimer(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	schedule, value := scheduleFixture(t, s)
	due := *value.NextRunAt
	_, schedule, value = appendWaiting(t, s, schedule, value)
	value.NextRunAt = &due
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.rewind", nil, func(tx *Tx) (any, error) { return tx.PutSchedule(schedule.ID, schedule.Revision, value) })
	if err != nil {
		t.Fatal(err)
	}
	schedule, err = s.Get(ctx, domain.ScheduleKind, schedule.ID)
	if err != nil {
		t.Fatal(err)
	}
	before := schedule
	next, _ := value.Definition.NextRun(due)
	value.NextRunAt = &next
	value.LastOccurrence++
	occurrence := domain.ScheduleOccurrence{ScheduleID: schedule.ID, ConfigurationRevision: 1, Sequence: 2, Trigger: domain.CronOccurrence, DueAt: due, AcceptedAt: due, Selection: value.Definition.Selection(), Overlap: value.Definition.Overlap, State: domain.OccurrenceWaiting}
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.duplicate-due", nil, func(tx *Tx) (any, error) {
		return tx.AppendScheduleOccurrence(domain.NewID(), schedule, value, occurrence)
	})
	if err == nil {
		t.Fatal("same cron instant created twice")
	}
	current, err := s.Get(ctx, domain.ScheduleKind, schedule.ID)
	if err != nil || current.Revision != before.Revision || string(current.Data) != string(before.Data) {
		t.Fatal("duplicate due partially advanced timer", err)
	}
}

func TestScheduleOccurrenceBindsOnlyItsOwnDerivedSession(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	schedule, value := scheduleFixture(t, s)
	record, _, _ := appendWaiting(t, s, schedule, value)
	occurrence, err := Decode[domain.ScheduleOccurrence](record)
	if err != nil {
		t.Fatal(err)
	}
	sessionID := domain.NewID()
	activate := func(source domain.SessionSource, origin *domain.ScheduleOrigin) error {
		_, err := s.Mutate(ctx, domain.NewID(), "fixture.activate", nil, func(tx *Tx) (any, error) {
			selected := occurrence.Selection
			session := domain.Session{Name: selected.Name, ProjectID: selected.ProjectID, AgentID: selected.AgentID, MachineID: selected.MachineID, Workspace: selected.Workspace, Source: source, ScheduleOrigin: origin, Outcome: domain.ExecutionNotStarted, Archive: domain.NotArchived, Recovery: domain.NoRecovery, Dispatch: domain.DispatchBlocked}
			if _, err := tx.Put(domain.SessionKind, sessionID, 0, sessionID, selected.ProjectID, session); err != nil {
				return nil, err
			}
			active := occurrence
			active.State = domain.OccurrenceActive
			active.SessionID = sessionID
			return tx.UpdateScheduleOccurrence(record, active)
		})
		return err
	}
	origin := &domain.ScheduleOrigin{ScheduleID: schedule.ID, OccurrenceID: record.ID, ConfigurationRevision: 1, Trigger: domain.CronOccurrence}
	if err := activate(domain.ManualSession, origin); err == nil {
		t.Fatal("ordinary session adopted as scheduled")
	}
	if _, err := s.Get(ctx, domain.SessionKind, sessionID); domain.SafeError(err).Code != domain.NotFound {
		t.Fatal("rejected activation left session", err)
	}
	foreign := *origin
	foreign.OccurrenceID = domain.NewID()
	if err := activate(domain.ScheduledSession, &foreign); err == nil {
		t.Fatal("another occurrence's session adopted")
	}
	if err := activate(domain.ScheduledSession, origin); err != nil {
		t.Fatal(err)
	}
	current, err := s.Get(ctx, domain.OccurrenceKind, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	active, err := Decode[domain.ScheduleOccurrence](current)
	if err != nil || active.SessionID != sessionID || active.State != domain.OccurrenceActive {
		t.Fatal("activation lost ownership", err)
	}
}

func TestScheduleWaitLimitIsAtomicAndDoesNotDropAcceptedOccurrences(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	schedule, value := scheduleFixture(t, s)
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.fill-wait", nil, func(tx *Tx) (any, error) {
		for range MaxWaitingOccurrences {
			current, err := tx.Get(domain.ScheduleKind, schedule.ID)
			if err != nil {
				return nil, err
			}
			due := *value.NextRunAt
			next, err := value.Definition.NextRun(due)
			if err != nil {
				return nil, err
			}
			occurrence := domain.ScheduleOccurrence{ScheduleID: current.ID, ConfigurationRevision: 1, Sequence: value.LastOccurrence + 1, Trigger: domain.CronOccurrence, DueAt: due, AcceptedAt: due, Selection: value.Definition.Selection(), Overlap: value.Definition.Overlap, State: domain.OccurrenceWaiting}
			value.LastOccurrence++
			value.NextRunAt = &next
			if _, err := tx.AppendScheduleOccurrence(domain.NewID(), current, value, occurrence); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	schedule, err = s.Get(ctx, domain.ScheduleKind, schedule.ID)
	if err != nil {
		t.Fatal(err)
	}
	due := *value.NextRunAt
	next, _ := value.Definition.NextRun(due)
	overflow := domain.ScheduleOccurrence{ScheduleID: schedule.ID, ConfigurationRevision: 1, Sequence: value.LastOccurrence + 1, Trigger: domain.CronOccurrence, DueAt: due, AcceptedAt: due, Selection: value.Definition.Selection(), Overlap: value.Definition.Overlap, State: domain.OccurrenceWaiting}
	value.LastOccurrence++
	value.NextRunAt = &next
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.overflow", nil, func(tx *Tx) (any, error) {
		return tx.AppendScheduleOccurrence(domain.NewID(), schedule, value, overflow)
	})
	if domain.SafeError(err).Code != domain.ResourceExhausted {
		t.Fatal("wait overflow not reported", err)
	}
	if err := s.Read(ctx, func(tx *Tx) error {
		count, err := tx.WaitingOccurrenceCount(schedule.ID)
		if err != nil || count != MaxWaitingOccurrences {
			t.Fatal("overflow removed accepted occurrences", count, err)
		}
		retained, err := tx.Get(domain.ScheduleKind, schedule.ID)
		if err != nil || retained.Revision != schedule.Revision {
			t.Fatal("overflow advanced schedule", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
