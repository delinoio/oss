package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type scheduleDispatchFixture struct {
	service                                                         *Service
	ctx                                                             context.Context
	root                                                            string
	now                                                             time.Time
	schedule, agent, machine, project, repository, device, instance domain.ID
}

func newScheduleDispatchFixture(t *testing.T, overlap domain.ScheduleOverlap, local bool) *scheduleDispatchFixture {
	t.Helper()
	f := &scheduleDispatchFixture{ctx: domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), root: filepath.Join(t.TempDir(), "state"), now: time.Date(2026, 9, 25, 10, 0, 1, 0, time.UTC), schedule: domain.NewID(), agent: domain.NewID(), machine: domain.NewID(), project: domain.NewID(), repository: domain.NewID(), device: domain.NewID(), instance: domain.NewID()}
	db, err := store.Open(f.ctx, f.root)
	if err != nil {
		t.Fatal(err)
	}
	f.service = &Service{Store: db, Endpoint: Endpoint{StartedAt: f.now.Add(-time.Hour)}, logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}
	t.Cleanup(func() { f.service.Store.Close() })
	f.mutate(t, func(tx *store.Tx) error {
		model, provider := domain.NewID(), domain.NewID()
		for _, item := range []struct {
			kind  domain.Kind
			id    domain.ID
			value any
		}{
			{domain.ProviderKind, provider, domain.Provider{Name: "Fixture", Endpoint: "http://127.0.0.1:1", Protocol: domain.OpenAIResponses, Authentication: domain.KeylessAuth}},
			{domain.ModelKind, model, domain.Model{Name: "Fixture", NativeID: "fixture", ProviderID: provider, Harnesses: []domain.Harness{domain.Codex}, MetadataSource: domain.UserDeclared}},
			{domain.AgentKind, f.agent, domain.Agent{Name: "Fixture", ModelID: model, Harness: domain.Codex, Options: domain.AgentOptions{Permission: domain.PermissionDefault}}},
			{domain.MachineKind, f.machine, domain.Machine{Name: "Fixture", OS: "linux", Architecture: "arm64"}},
			{domain.DeviceKind, f.device, domain.Device{Name: "Fixture", Type: domain.WorkerDevice, MachineID: f.machine, PairedAt: f.now.Add(-time.Hour)}},
			{domain.RepositoryKind, f.repository, domain.Repository{Name: "Fixture", Checkouts: []domain.Checkout{{MachineID: f.machine, Path: filepath.Join(t.TempDir(), "checkout")}}, Base: domain.Reference{Type: domain.LocalBranch, Name: "main"}, Starting: domain.Reference{Type: domain.LocalBranch, Name: "main"}}},
			{domain.ProjectKind, f.project, domain.Project{Name: "Fixture", Repositories: []domain.ID{f.repository}, PrimaryRepository: f.repository}},
		} {
			if _, err := tx.Put(item.kind, item.id, 0, "", "", item.value); err != nil {
				return err
			}
		}
		if err := tx.SetWorkerInstance(f.machine, f.instance, f.now.Add(-2*time.Second)); err != nil {
			return err
		}
		definition := domain.ScheduleDefinition{Name: "Fixture schedule", Enabled: true, Prompt: "original private scheduled prompt", ProjectID: f.project, AgentID: f.agent, MachineID: f.machine, Workspace: domain.Worktree, Mode: domain.ExecuteMode, Cron: "* * * * *", Timezone: "UTC", Overlap: overlap}
		var origin *domain.LocalOrigin
		if local {
			definition.Workspace = domain.Local
			origin = &domain.LocalOrigin{MachineID: f.machine, DeviceID: f.device}
		}
		due := f.now.Truncate(time.Minute)
		_, err := tx.PutSchedule(f.schedule, 0, domain.Schedule{Definition: definition, ConfigurationRevision: 1, NextRunAt: &due, LocalOrigin: origin})
		return err
	})
	return f
}

func (f *scheduleDispatchFixture) mutate(t *testing.T, apply func(*store.Tx) error) {
	t.Helper()
	_, err := f.service.Store.Mutate(f.ctx, domain.NewID(), "fixture.schedule", nil, func(tx *store.Tx) (any, error) { return nil, apply(tx) })
	if err != nil {
		t.Fatal(err)
	}
}
func (f *scheduleDispatchFixture) record(t *testing.T, kind domain.Kind, id domain.ID) store.Record {
	t.Helper()
	r, err := f.service.Store.Get(f.ctx, kind, id)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func (f *scheduleDispatchFixture) occurrence(t *testing.T, id domain.ID) (store.Record, domain.ScheduleOccurrence) {
	t.Helper()
	r := f.record(t, domain.OccurrenceKind, id)
	v, err := store.Decode[domain.ScheduleOccurrence](r)
	if err != nil {
		t.Fatal(err)
	}
	return r, v
}
func (f *scheduleDispatchFixture) clock() time.Time { return f.now }

func (f *scheduleDispatchFixture) accept(t *testing.T, trigger domain.OccurrenceTrigger) (store.Record, domain.ScheduleOccurrence) {
	t.Helper()
	result, err := f.service.acceptScheduleOccurrence(f.ctx, f.record(t, domain.ScheduleKind, f.schedule), trigger, domain.NewID(), f.clock)
	if err != nil {
		t.Fatal(err)
	}
	var refs occurrenceReceipt
	if err := json.Unmarshal(result.Data, &refs); err != nil {
		t.Fatal(err)
	}
	return f.occurrence(t, refs.OccurrenceID)
}
func (f *scheduleDispatchFixture) stop(t *testing.T, id domain.ID) {
	t.Helper()
	r := f.record(t, domain.SessionKind, id)
	_, err := f.service.ControlSession(f.ctx, connect.NewRequest(&pb.ControlSessionRequest{Mutation: &pb.Mutation{Id: string(id), ExpectedRevision: r.Revision, RequestId: string(domain.NewID())}, Action: pb.SessionAction_SESSION_ACTION_STOP}))
	if err != nil {
		t.Fatal(err)
	}
}
func (f *scheduleDispatchFixture) reconcile(t *testing.T, id domain.ID) domain.ScheduleOccurrence {
	t.Helper()
	r := f.record(t, domain.OccurrenceKind, id)
	if err := f.service.dispatchScheduleOccurrence(f.ctx, r, f.now); err != nil {
		t.Fatal(err)
	}
	_, v := f.occurrence(t, id)
	return v
}

func TestScheduleCoordinatorOverlapCreatesIndependentSessionsAndReceipts(t *testing.T) {
	for _, local := range []bool{false, true} {
		t.Run(map[bool]string{false: "worktree", true: "local"}[local], func(t *testing.T) {
			f := newScheduleDispatchFixture(t, domain.ScheduleAllowOverlap, local)
			cronRecord, cron := f.accept(t, domain.CronOccurrence)
			manualRecord, manual := f.accept(t, domain.ManualOccurrence)
			if cron.State != domain.OccurrenceActive || manual.State != domain.OccurrenceActive || cron.SessionID == manual.SessionID || cron.Sequence != 1 || manual.Sequence != 2 || cron.Trigger != domain.CronOccurrence || manual.Trigger != domain.ManualOccurrence {
				t.Fatalf("invalid independent runs: %+v %+v", cron, manual)
			}
			for _, pair := range []struct {
				r store.Record
				v domain.ScheduleOccurrence
			}{{cronRecord, cron}, {manualRecord, manual}} {
				session, err := store.Decode[domain.Session](f.record(t, domain.SessionKind, pair.v.SessionID))
				if err != nil || session.InitialExecution != nil || session.CurrentExecution != nil || session.ScheduleOrigin == nil || session.ScheduleOrigin.OccurrenceID != pair.r.ID || session.Source != domain.ScheduledSession || session.PendingInputs != 1 || session.Preparation == nil {
					t.Fatalf("invalid session: %+v %v", session, err)
				}
				job, err := store.Decode[domain.Job](f.record(t, domain.JobKind, session.Preparation.JobID))
				var preparation workspace.PrepareRequest
				if err != nil || domain.Decode(job.Input, &preparation) != nil || preparation.SessionID != pair.v.SessionID || len(preparation.Repositories) != 1 || preparation.Repositories[0].ID != f.repository {
					t.Fatalf("invalid preparation: %+v %v", preparation, err)
				}
				if local && (session.LocalOrigin == nil || preparation.OriginMachineID != f.machine || preparation.Repositories[0].AutoFetch || preparation.Repositories[0].Starting != (domain.Reference{})) {
					t.Fatal("Local origin or as-is selection lost")
				}
				inputs, err := f.service.Store.List(f.ctx, store.Filter{Kind: domain.QueueKind, SessionID: pair.v.SessionID, Limit: 10})
				if err != nil || len(inputs) != 1 {
					t.Fatal("missing input", err)
				}
				input, _ := store.Decode[domain.QueuedInput](inputs[0])
				if input.Prompt != pair.v.Selection.Prompt || input.Mode != domain.ExecuteMode || input.Delivery != domain.InputQueued {
					t.Fatal("changed queued selection")
				}
			}
		})
	}
}

func TestScheduleCoordinatorConcurrentRunNowRetryAndCronConflict(t *testing.T) {
	f := newScheduleDispatchFixture(t, domain.ScheduleAllowOverlap, false)
	record := f.record(t, domain.ScheduleKind, f.schedule)
	request := domain.NewID()
	var wg sync.WaitGroup
	results := make(chan store.Result, 12)
	errs := make(chan error, 12)
	for range 12 {
		wg.Go(func() {
			result, err := f.service.acceptScheduleOccurrence(f.ctx, record, domain.ManualOccurrence, request, f.clock)
			results <- result
			errs <- err
		})
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var original []byte
	fresh := 0
	for result := range results {
		if !result.Replayed {
			fresh++
		}
		if original == nil {
			original = result.Data
		}
		if !bytes.Equal(original, result.Data) {
			t.Fatal("retry returned another occurrence")
		}
	}
	if fresh != 1 {
		t.Fatal("duplicate acceptance", fresh)
	}
	replay, err := f.service.acceptScheduleOccurrence(f.ctx, record, domain.ManualOccurrence, request, func() time.Time { return f.now.Add(time.Minute) })
	if err != nil || !replay.Replayed || !bytes.Equal(original, replay.Data) {
		t.Fatal("clock-dependent receipt", err)
	}
	if _, err := f.service.acceptScheduleOccurrence(f.ctx, record, domain.CronOccurrence, domain.NewID(), f.clock); !errors.Is(err, scheduleUnchanged) {
		t.Fatal("stale timer accepted", err)
	}
	occurrences, err := f.service.Store.List(f.ctx, store.Filter{Kind: domain.OccurrenceKind, Limit: 20})
	if err != nil || len(occurrences) != 1 {
		t.Fatal("duplicate occurrence", err)
	}
}

func TestScheduleCoordinatorOfflineSkipsWithoutCreatingSessions(t *testing.T) {
	for _, kind := range []string{"server", "worker", "reconnected-worker"} {
		t.Run(kind, func(t *testing.T) {
			f := newScheduleDispatchFixture(t, domain.ScheduleWaitOverlap, false)
			expected := domain.WorkerOfflineOccurrence
			switch kind {
			case "server":
				f.service.Endpoint.StartedAt = f.now
				expected = domain.ServerOfflineOccurrence
			case "worker":
				f.mutate(t, func(tx *store.Tx) error { return tx.SetWorkerInstance(f.machine, f.instance, f.now.Add(-time.Minute)) })
			case "reconnected-worker":
				f.mutate(t, func(tx *store.Tx) error { return tx.SetWorkerInstance(f.machine, domain.NewID(), f.now) })
			}
			_, v := f.accept(t, domain.CronOccurrence)
			if v.State != domain.OccurrenceSkipped || v.Reason != expected || v.SessionID != "" {
				t.Fatalf("offline run accepted: %+v", v)
			}
			sessions, err := f.service.Store.List(f.ctx, store.Filter{Kind: domain.SessionKind, Limit: 10})
			if err != nil || len(sessions) != 0 {
				t.Fatal("offline run created session", err)
			}
			value, _ := store.Decode[domain.Schedule](f.record(t, domain.ScheduleKind, f.schedule))
			if value.LastOccurrence != 1 || value.NextRunAt == nil || !value.NextRunAt.Equal(f.now.Truncate(time.Minute).Add(time.Minute)) {
				t.Fatal("offline history did not advance exact timer")
			}
		})
	}
}

func TestScheduleCoordinatorWaitFIFODeletionRestartAndCurrentSelection(t *testing.T) {
	f := newScheduleDispatchFixture(t, domain.ScheduleWaitOverlap, false)
	firstRecord, first := f.accept(t, domain.ManualOccurrence)
	secondRecord, second := f.accept(t, domain.ManualOccurrence)
	thirdRecord, third := f.accept(t, domain.ManualOccurrence)
	if second.State != domain.OccurrenceWaiting || third.State != domain.OccurrenceWaiting || second.SessionID != "" {
		t.Fatal("wait not retained")
	}
	before := append([]byte(nil), secondRecord.Data...)
	if err := f.service.dispatchScheduleOccurrence(f.ctx, secondRecord, f.now); !errors.Is(err, scheduleUnchanged) {
		t.Fatal("wait released before first run ended", err)
	}
	f.mutate(t, func(tx *store.Tx) error {
		r, err := tx.Get(domain.ScheduleKind, f.schedule)
		if err != nil {
			return err
		}
		value, err := store.Decode[domain.Schedule](r)
		if err != nil {
			return err
		}
		value.Definition.Prompt = "edited future prompt"
		value.Definition.Enabled = false
		value.ConfigurationRevision++
		value.NextRunAt = nil
		r, err = tx.PutSchedule(r.ID, r.Revision, value)
		if err != nil {
			return err
		}
		return tx.Delete(domain.ScheduleKind, r.ID, r.Revision)
	})
	f.stop(t, first.SessionID)
	if v := f.reconcile(t, firstRecord.ID); v.State != domain.OccurrenceStopped {
		t.Fatal("Stop not retained")
	}
	if err := f.service.Store.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(f.ctx, f.root)
	if err != nil {
		t.Fatal(err)
	}
	f.service.Store = db
	if !bytes.Equal(before, f.record(t, domain.OccurrenceKind, secondRecord.ID).Data) {
		t.Fatal("waiting selection changed across deletion/restart")
	}
	// A disconnected accepted Wait must remain queued instead of becoming an offline skip.
	f.now = f.now.Add(time.Minute)
	if err := f.service.dispatchScheduleOccurrence(f.ctx, secondRecord, f.now); !errors.Is(err, scheduleUnchanged) {
		t.Fatal("offline wait discarded", err)
	}
	f.mutate(t, func(tx *store.Tx) error { return tx.SetWorkerInstance(f.machine, f.instance, f.now) })
	if err := f.service.dispatchScheduleOccurrence(f.ctx, thirdRecord, f.now); !errors.Is(err, scheduleUnchanged) {
		t.Fatal("FIFO overtaken", err)
	}
	second = f.reconcile(t, secondRecord.ID)
	if second.State != domain.OccurrenceActive || second.Selection.Prompt != "original private scheduled prompt" {
		t.Fatal("accepted input changed")
	}
	secondSession, _ := store.Decode[domain.Session](f.record(t, domain.SessionKind, second.SessionID))
	if secondSession.InitialExecution != nil {
		t.Fatal("account/template snapshot resolved before native dispatch")
	}
	f.stop(t, second.SessionID)
	f.reconcile(t, secondRecord.ID)
	third = f.reconcile(t, thirdRecord.ID)
	if third.State != domain.OccurrenceActive || third.SessionID == second.SessionID {
		t.Fatal("FIFO successor missing")
	}
}

func TestScheduleCoordinatorSkipAndWaitObserveResumedHistoricalSession(t *testing.T) {
	for _, policy := range []domain.ScheduleOverlap{domain.ScheduleSkipOverlap, domain.ScheduleWaitOverlap} {
		t.Run(string(policy), func(t *testing.T) {
			f := newScheduleDispatchFixture(t, policy, false)
			firstRecord, first := f.accept(t, domain.ManualOccurrence)
			_, second := f.accept(t, domain.ManualOccurrence)
			if policy == domain.ScheduleSkipOverlap && (second.State != domain.OccurrenceSkipped || second.Reason != domain.OverlapSkippedOccurrence) {
				t.Fatal("overlap not skipped")
			}
			f.stop(t, first.SessionID)
			f.reconcile(t, firstRecord.ID)
			// Public preparation retry is permitted after a canceled attempt; the
			// old occurrence stays terminal but its session is active again.
			r := f.record(t, domain.SessionKind, first.SessionID)
			_, err := f.service.PrepareSessionWorkspace(f.ctx, connect.NewRequest(&pb.PrepareSessionWorkspaceRequest{Mutation: &pb.Mutation{Id: string(r.ID), ExpectedRevision: r.Revision, RequestId: string(domain.NewID())}}))
			if err != nil {
				t.Fatal(err)
			}
			_, next := f.accept(t, domain.ManualOccurrence)
			if policy == domain.ScheduleSkipOverlap && next.State != domain.OccurrenceSkipped {
				t.Fatal("historical active session ignored")
			}
			if policy == domain.ScheduleWaitOverlap && next.State != domain.OccurrenceWaiting {
				t.Fatal("historical active session ignored")
			}
			_, retained := f.occurrence(t, firstRecord.ID)
			if retained.State != domain.OccurrenceStopped {
				t.Fatal("history rewritten by resumed session")
			}
		})
	}
}

func TestScheduleCoordinatorSelectionFailureAndRevokedLocalOrigin(t *testing.T) {
	for _, local := range []bool{false, true} {
		t.Run(map[bool]string{false: "project-deleted", true: "origin-revoked"}[local], func(t *testing.T) {
			f := newScheduleDispatchFixture(t, domain.ScheduleWaitOverlap, local)
			_, first := f.accept(t, domain.ManualOccurrence)
			waitRecord, _ := f.accept(t, domain.ManualOccurrence)
			f.stop(t, first.SessionID)
			f.mutate(t, func(tx *store.Tx) error {
				if !local {
					r, err := tx.Get(domain.ProjectKind, f.project)
					if err != nil {
						return err
					}
					return tx.Delete(r.Kind, r.ID, r.Revision)
				}
				r, err := tx.Get(domain.DeviceKind, f.device)
				if err != nil {
					return err
				}
				v, err := store.Decode[domain.Device](r)
				if err != nil {
					return err
				}
				v.Revoked = true
				_, err = tx.Put(r.Kind, r.ID, r.Revision, "", "", v)
				return err
			})
			wait := f.reconcile(t, waitRecord.ID)
			if wait.State != domain.OccurrenceFailed || wait.Problem == nil || wait.Reason != domain.SelectionFailedOccurrence || wait.SessionID != "" {
				t.Fatal("invalid wait executed")
			}
			_, fresh := f.accept(t, domain.ManualOccurrence)
			if fresh.State != domain.OccurrenceFailed || fresh.SessionID != "" {
				t.Fatal("invalid selection executed")
			}
		})
	}
}

func TestScheduleCoordinatorLoopDrainsDueHistoryAndJoinsCancellation(t *testing.T) {
	f := newScheduleDispatchFixture(t, domain.ScheduleAllowOverlap, false)
	// Use real wall time only for this lifecycle test. Every retained due time
	// precedes startup, so the loop must never queue provider/native work.
	f.mutate(t, func(tx *store.Tx) error {
		r, err := tx.Get(domain.ScheduleKind, f.schedule)
		if err != nil {
			return err
		}
		v, err := store.Decode[domain.Schedule](r)
		if err != nil {
			return err
		}
		due := time.Now().UTC().Truncate(time.Minute).Add(-time.Minute)
		v.NextRunAt = &due
		_, err = tx.PutSchedule(r.ID, r.Revision, v)
		return err
	})
	f.service.Endpoint.StartedAt = time.Now().UTC()
	ctx, cancel := context.WithCancel(f.ctx)
	done := make(chan struct{})
	go func() { defer close(done); f.service.runScheduleDispatch(ctx) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("scheduler did not join cancellation")
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		rows, err := f.service.Store.List(f.ctx, store.Filter{Kind: domain.OccurrenceKind, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) >= 2 {
			for _, r := range rows {
				v, _ := store.Decode[domain.ScheduleOccurrence](r)
				if v.State != domain.OccurrenceSkipped || v.Reason != domain.ServerOfflineOccurrence {
					t.Fatal("catch-up execution created")
				}
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("scheduler did not advance missed history")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestScheduleCoordinatorCronUniquenessRollsBackSessionAndJobs(t *testing.T) {
	f := newScheduleDispatchFixture(t, domain.ScheduleAllowOverlap, false)
	_, first := f.accept(t, domain.CronOccurrence)
	f.mutate(t, func(tx *store.Tx) error {
		r, err := tx.Get(domain.ScheduleKind, f.schedule)
		if err != nil {
			return err
		}
		value, err := store.Decode[domain.Schedule](r)
		if err != nil {
			return err
		}
		value.NextRunAt = &first.DueAt
		_, err = tx.PutSchedule(r.ID, r.Revision, value)
		return err
	})
	record := f.record(t, domain.ScheduleKind, f.schedule)
	_, err := f.service.acceptScheduleOccurrence(f.ctx, record, domain.CronOccurrence, domain.NewID(), f.clock)
	if err == nil {
		t.Fatal("duplicate cron instant accepted")
	}
	for _, kind := range []domain.Kind{domain.SessionKind, domain.JobKind, domain.QueueKind, domain.OccurrenceKind} {
		rows, err := f.service.Store.List(f.ctx, store.Filter{Kind: kind, Limit: 10})
		if err != nil || len(rows) != 1 {
			t.Fatalf("partial %s created: %d %v", kind, len(rows), err)
		}
	}
	current := f.record(t, domain.ScheduleKind, f.schedule)
	if current.Revision != record.Revision || !bytes.Equal(current.Data, record.Data) {
		t.Fatal("failed cron transaction advanced timer")
	}
}

func TestScheduleCoordinatorWaitCapacityRecordsSkippedDue(t *testing.T) {
	f := newScheduleDispatchFixture(t, domain.ScheduleWaitOverlap, false)
	f.accept(t, domain.ManualOccurrence)
	f.mutate(t, func(tx *store.Tx) error {
		for range store.MaxWaitingOccurrences {
			r, err := tx.Get(domain.ScheduleKind, f.schedule)
			if err != nil {
				return err
			}
			value, err := store.Decode[domain.Schedule](r)
			if err != nil {
				return err
			}
			value.LastOccurrence++
			occurrence := domain.ScheduleOccurrence{ScheduleID: f.schedule, ConfigurationRevision: value.ConfigurationRevision, Sequence: value.LastOccurrence, Trigger: domain.ManualOccurrence, DueAt: f.now, AcceptedAt: f.now, Selection: value.Definition.Selection(), Overlap: domain.ScheduleWaitOverlap, State: domain.OccurrenceWaiting}
			if _, err := tx.AppendScheduleOccurrence(domain.NewID(), r, value, occurrence); err != nil {
				return err
			}
		}
		return nil
	})
	_, overflow := f.accept(t, domain.CronOccurrence)
	if overflow.State != domain.OccurrenceSkipped || overflow.Reason != domain.WaitingLimitOccurrence || overflow.SessionID != "" {
		t.Fatal("capacity did not retain explicit skipped history")
	}
	if err := f.service.Store.Read(f.ctx, func(tx *store.Tx) error {
		count, err := tx.WaitingOccurrenceCount(f.schedule)
		if count != store.MaxWaitingOccurrences {
			t.Fatal("capacity evicted accepted work")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	value, _ := store.Decode[domain.Schedule](f.record(t, domain.ScheduleKind, f.schedule))
	if value.LastOccurrence != store.MaxWaitingOccurrences+2 || value.NextRunAt == nil || !value.NextRunAt.Equal(f.now.Truncate(time.Minute).Add(time.Minute)) {
		t.Fatal("capacity did not advance exact due instant")
	}
}

func TestScheduleCoordinatorConcurrentWaitReleaseCreatesOneSession(t *testing.T) {
	f := newScheduleDispatchFixture(t, domain.ScheduleWaitOverlap, false)
	_, first := f.accept(t, domain.ManualOccurrence)
	waiting, _ := f.accept(t, domain.ManualOccurrence)
	f.stop(t, first.SessionID)
	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for range 10 {
		wg.Go(func() { errs <- f.service.dispatchScheduleOccurrence(f.ctx, waiting, f.now) })
	}
	wg.Wait()
	close(errs)
	fresh := 0
	for err := range errs {
		if err == nil {
			fresh++
		} else if !errors.Is(err, scheduleUnchanged) {
			t.Fatal(err)
		}
	}
	if fresh != 1 {
		t.Fatal("wait released repeatedly", fresh)
	}
	sessions, err := f.service.Store.List(f.ctx, store.Filter{Kind: domain.SessionKind, Limit: 10})
	if err != nil || len(sessions) != 2 {
		t.Fatal("wait created duplicate session", err)
	}
}

func TestScheduleCoordinatorNativeCompletionRequiresOwnedCleanup(t *testing.T) {
	for _, scenario := range []struct {
		name    string
		outcome domain.ExecutionOutcome
		action  pb.SessionAction
	}{
		{"success", domain.ExecutionSucceeded, 0}, {"failure", domain.ExecutionFailed, 0}, {"stopped", domain.ExecutionStopped, 0},
		{"stop-before-native-success", domain.ExecutionSucceeded, pb.SessionAction_SESSION_ACTION_STOP},
		{"archive-before-native-success", domain.ExecutionSucceeded, pb.SessionAction_SESSION_ACTION_ARCHIVE},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			outcome := scenario.outcome
			f := newPublicationFixture(t)
			ctx := context.Background()
			check := func(want domain.OccurrenceState) {
				t.Helper()
				err := f.service.Store.Read(ctx, func(tx *store.Tx) error {
					r, v, err := sessionRecord(tx, f.input.SessionID)
					if err != nil {
						return err
					}
					got, problem, err := scheduleSessionResult(tx, r, v)
					if err == nil && (got != want || (got == domain.OccurrenceFailed && problem == nil)) {
						t.Fatalf("wrong schedule completion: %s %+v", got, problem)
					}
					return err
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			f.publish(t, f.event(domain.ExecutionThreadBound, 1))
			f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
			check(domain.OccurrenceActive)
			if scenario.action != 0 {
				r, err := f.service.Store.Get(ctx, domain.SessionKind, f.input.SessionID)
				if err != nil {
					t.Fatal(err)
				}
				client := delidevv1connect.NewSessionServiceClient(f.http.Client(), f.http.URL)
				_, err = client.ControlSession(ctx, ownerRequest(f.service.Identity, &pb.ControlSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, Action: scenario.action}))
				if err != nil {
					t.Fatal(err)
				}
			}
			// This protocol fixture has no workspace runner. Supply only the
			// previously prepared state; native completion still uses real RPCs.
			_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.schedule-prepared", nil, func(tx *store.Tx) (any, error) {
				r, v, err := sessionRecord(tx, f.input.SessionID)
				if err != nil {
					return nil, err
				}
				v.Preparation = &domain.SessionPreparation{JobID: domain.NewID(), State: domain.PreparationReady}
				return tx.Put(r.Kind, r.ID, r.Revision, r.ID, r.ProjectID, v)
			})
			if err != nil {
				t.Fatal(err)
			}
			event := f.event(domain.ExecutionTurnFinished, 3)
			event.Outcome = outcome
			f.publish(t, event)
			// A terminal event alone cannot free a Wait successor.
			check(domain.OccurrenceActive)
			completion := f.completion()
			completion.Outcome = outcome
			f.reportCompletion(t, completion)
			want := map[domain.ExecutionOutcome]domain.OccurrenceState{domain.ExecutionSucceeded: domain.OccurrenceSucceeded, domain.ExecutionFailed: domain.OccurrenceFailed, domain.ExecutionStopped: domain.OccurrenceStopped}[outcome]
			if scenario.action != 0 {
				want = domain.OccurrenceStopped
			}
			check(want)
			if outcome == domain.ExecutionSucceeded && scenario.action == 0 {
				_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.schedule-followup", nil, func(tx *store.Tx) (any, error) {
					r, v, err := sessionRecord(tx, f.input.SessionID)
					if err != nil {
						return nil, err
					}
					v.NextExecutionIntent = domain.ContinueAutomatically
					v.PendingInputs = 1
					v.Dispatch = domain.DispatchReady
					return tx.Put(r.Kind, r.ID, r.Revision, r.ID, r.ProjectID, v)
				})
				if err != nil {
					t.Fatal(err)
				}
				check(domain.OccurrenceActive)
			}
		})
	}
}

func TestScheduleCoordinatorUncertainAndBlockingSessionsStayActive(t *testing.T) {
	for _, scenario := range []string{"recovery", "approval", "question", "preparation-stopping", "archive-cleanup"} {
		t.Run(scenario, func(t *testing.T) {
			f := newScheduleDispatchFixture(t, domain.ScheduleSkipOverlap, false)
			firstRecord, first := f.accept(t, domain.ManualOccurrence)
			f.mutate(t, func(tx *store.Tx) error {
				r, v, err := sessionRecord(tx, first.SessionID)
				if err != nil {
					return err
				}
				switch scenario {
				case "recovery":
					v.Recovery = domain.NeedsRecovery
					v.Outcome = domain.ExecutionFailed
					v.Dispatch = domain.DispatchPaused
				case "approval", "question":
					v.ActiveExecutionID = domain.NewID()
					v.Outcome = domain.ExecutionRunning
					v.Dispatch = domain.DispatchClaimed
				case "preparation-stopping":
					v.Preparation.State = domain.PreparationStopping
					v.Dispatch = domain.DispatchPaused
				case "archive-cleanup":
					v.Archive = domain.ArchivePending
					v.Dispatch = domain.DispatchPaused
				}
				_, err = tx.Put(r.Kind, r.ID, r.Revision, r.ID, r.ProjectID, v)
				return err
			})
			if err := f.service.dispatchScheduleOccurrence(f.ctx, firstRecord, f.now); !errors.Is(err, scheduleUnchanged) {
				t.Fatal("uncertain run completed", err)
			}
			_, next := f.accept(t, domain.ManualOccurrence)
			if next.State != domain.OccurrenceSkipped || next.Reason != domain.OverlapSkippedOccurrence {
				t.Fatal("blocking run did not count as active")
			}
		})
	}
}

func TestScheduleCoordinatorUnsettledOriginalResponseBlocksCleanupSummary(t *testing.T) {
	f := newPublicationFixture(t)
	ctx := context.Background()
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	event := f.event(domain.ExecutionTurnFinished, 3)
	event.Outcome = domain.ExecutionSucceeded
	f.publish(t, event)
	f.reportCompletion(t, f.completion())
	_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.schedule-unsettled-response", nil, func(tx *store.Tx) (any, error) {
		r, v, err := sessionRecord(tx, f.input.SessionID)
		if err != nil {
			return nil, err
		}
		v.Preparation = &domain.SessionPreparation{JobID: domain.NewID(), State: domain.PreparationReady}
		if v.Execution.UnconfirmedResponses != 0 || !v.Execution.CleanupVerified {
			t.Fatal("fixture lacks zero-counter cleanup summary")
		}
		if _, err := tx.Put(r.Kind, r.ID, r.Revision, r.ID, r.ProjectID, v); err != nil {
			return nil, err
		}
		return tx.Put(domain.InteractionKind, domain.NewID(), 0, r.ID, r.ProjectID, map[string]any{"execution_id": f.input.ExecutionID, "closure": "resolved", "response": map[string]any{"state": "uncertain"}})
	})
	if err != nil {
		t.Fatal(err)
	}
	err = f.service.Store.Read(ctx, func(tx *store.Tx) error {
		r, v, err := sessionRecord(tx, f.input.SessionID)
		if err != nil {
			return err
		}
		_, _, err = scheduleSessionResult(tx, r, v)
		return err
	})
	if err == nil || domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("unsettled response released a scheduled successor", err)
	}
}

func TestScheduleCoordinatorMissedHistoryVisitIsBounded(t *testing.T) {
	f := newScheduleDispatchFixture(t, domain.ScheduleAllowOverlap, false)
	due := time.Now().UTC().Truncate(time.Minute).Add(-30 * time.Minute)
	f.mutate(t, func(tx *store.Tx) error {
		r, err := tx.Get(domain.ScheduleKind, f.schedule)
		if err != nil {
			return err
		}
		v, err := store.Decode[domain.Schedule](r)
		if err != nil {
			return err
		}
		v.NextRunAt = &due
		_, err = tx.PutSchedule(r.ID, r.Revision, v)
		return err
	})
	f.service.Endpoint.StartedAt = time.Now().UTC()
	ctx, cancel := context.WithTimeout(f.ctx, 5*time.Second)
	defer cancel()
	if err := f.service.advanceDueSchedule(ctx, f.record(t, domain.ScheduleKind, f.schedule)); err != nil {
		t.Fatal(err)
	}
	value, _ := store.Decode[domain.Schedule](f.record(t, domain.ScheduleKind, f.schedule))
	if value.LastOccurrence != 25 || value.NextRunAt == nil || !value.NextRunAt.Equal(due.Add(25*time.Minute)) {
		t.Fatal("history visit exceeded its fairness bound or skipped instants")
	}
	rows, err := f.service.Store.List(f.ctx, store.Filter{Kind: domain.OccurrenceKind, Limit: 50})
	if err != nil || len(rows) != 25 {
		t.Fatal("missed history missing", err)
	}
	for _, r := range rows {
		v, _ := store.Decode[domain.ScheduleOccurrence](r)
		if v.State != domain.OccurrenceSkipped || v.Reason != domain.ServerOfflineOccurrence || v.SessionID != "" {
			t.Fatal("offline execution backlog created")
		}
	}
}
