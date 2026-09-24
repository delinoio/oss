package server

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

// A pending scan must not persist a receipt/event on every timer tick.
var scheduleUnchanged = domain.Fail(domain.Conflict, "The scheduled work is unchanged.", "Wait for its original readiness or completion boundary.")

type occurrenceReceipt struct {
	ScheduleID   domain.ID `json:"schedule_id"`
	OccurrenceID domain.ID `json:"occurrence_id"`
}

// Selection failures can become visible history only before the first write.
// Storage/cancellation failures must always roll back instead of advancing time.
func occurrenceSelectionFailure(err error) *domain.Error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil
	}
	problem := domain.SafeError(err)
	if problem.Cause != "" {
		return nil
	}
	switch problem.Code {
	case domain.InvalidArgument, domain.NotFound, domain.PermissionDenied, domain.MissingInput, domain.Unsupported, domain.Unavailable:
		return problem
	default:
		return nil
	}
}

func planOccurrenceSession(tx *store.Tx, id, occurrenceID domain.ID, occurrence domain.ScheduleOccurrence) (domain.Session, workspace.PrepareRequest, error) {
	value := acceptedSession(occurrence.Selection, occurrence.LocalOrigin, occurrence.InitiatedBy)
	value.Source = domain.ScheduledSession
	value.ScheduleOrigin = &domain.ScheduleOrigin{ScheduleID: occurrence.ScheduleID, OccurrenceID: occurrenceID, ConfigurationRevision: occurrence.ConfigurationRevision, Trigger: occurrence.Trigger}
	if err := validateSessionSelection(tx, occurrence.Selection); err != nil {
		return value, workspace.PrepareRequest{}, err
	}
	preparation, err := sessionWorkspaceRequest(tx, id, value)
	return value, preparation, err
}

// From the first job write onward, every error must leave the outer transaction.
func createOccurrenceSession(tx *store.Tx, id domain.ID, occurrence *domain.ScheduleOccurrence, value domain.Session, preparation workspace.PrepareRequest) error {
	if err := queueSessionWorkspace(tx, id, &value, preparation); err != nil {
		return err
	}
	if _, err := appendSessionInput(tx, id, &value, domain.SessionInput{Prompt: occurrence.Selection.Prompt, Mode: occurrence.Selection.Mode}); err != nil {
		return err
	}
	if _, err := tx.Put(domain.SessionKind, id, 0, id, value.ProjectID, value); err != nil {
		return err
	}
	occurrence.State, occurrence.SessionID = domain.OccurrenceActive, id
	return nil
}

func skipOccurrence(value *domain.ScheduleOccurrence, reason domain.OccurrenceReason, now time.Time) {
	value.State, value.Reason, value.FinishedAt = domain.OccurrenceSkipped, reason, &now
}

func failOccurrence(value *domain.ScheduleOccurrence, problem *domain.Error, now time.Time) {
	value.State, value.Reason, value.Problem, value.FinishedAt = domain.OccurrenceFailed, domain.SelectionFailedOccurrence, problem, &now
}

// Cron and Run now share acceptance. The receipt excludes the current clock so
// an exact manual retry joins its original occurrence instead of making a run.
func (s *Service) acceptScheduleOccurrence(ctx context.Context, record store.Record, trigger domain.OccurrenceTrigger, requestID domain.ID, clock func() time.Time) (store.Result, error) {
	actor, _ := domain.PrincipalFrom(ctx)
	identity := struct {
		ID       domain.ID
		Revision uint64
		Trigger  domain.OccurrenceTrigger
		Actor    domain.Principal
	}{record.ID, record.Revision, trigger, actor}
	result, err := s.Store.Mutate(ctx, requestID, "schedule.run", identity, func(tx *store.Tx) (any, error) {
		current, err := tx.Get(domain.ScheduleKind, record.ID)
		if err != nil {
			return nil, err
		}
		if current.Revision != record.Revision {
			return nil, scheduleUnchanged
		}
		value, err := store.Decode[domain.Schedule](current)
		if err != nil {
			return nil, err
		}
		if err := value.Validate(); err != nil {
			return nil, err
		}
		// Observe time after acquiring the database transaction. A heartbeat
		// committed while this request waited cannot appear to be in the future.
		now := clock().UTC()
		if now.IsZero() || (trigger != domain.CronOccurrence && trigger != domain.ManualOccurrence) {
			return nil, domain.Fail(domain.InvalidArgument, "Invalid schedule trigger boundary.", "Use the server clock and a supported trigger.")
		}
		occurrence := domain.ScheduleOccurrence{ScheduleID: current.ID, ConfigurationRevision: value.ConfigurationRevision, Sequence: value.LastOccurrence + 1, Trigger: trigger, DueAt: now, AcceptedAt: now, InitiatedBy: actor.DeviceID, Selection: value.Definition.Selection(), LocalOrigin: value.LocalOrigin, Overlap: value.Definition.Overlap}
		if trigger == domain.CronOccurrence {
			if !value.Definition.Enabled || value.NextRunAt == nil || value.NextRunAt.After(now) {
				return nil, scheduleUnchanged
			}
			occurrence.DueAt, occurrence.InitiatedBy = *value.NextRunAt, value.CreatedBy
			next, err := value.Definition.NextRun(occurrence.DueAt)
			if err != nil {
				return nil, err
			}
			value.NextRunAt = &next
			if s.Endpoint.StartedAt.IsZero() || occurrence.DueAt.Before(s.Endpoint.StartedAt) {
				skipOccurrence(&occurrence, domain.ServerOfflineOccurrence, now)
			}
		}
		id := domain.NewID()
		if !occurrence.State.Terminal() {
			if err := selectOccurrenceState(tx, id, &occurrence, now); err != nil {
				return nil, err
			}
		}
		value.LastOccurrence = occurrence.Sequence
		if _, err := tx.AppendScheduleOccurrence(id, current, value, occurrence); err != nil {
			return nil, err
		}
		return occurrenceReceipt{ScheduleID: current.ID, OccurrenceID: id}, nil
	})
	if err == nil {
		var refs occurrenceReceipt
		if err := json.Unmarshal(result.Data, &refs); err != nil {
			return result, err
		}
		s.logger.InfoContext(ctx, "schedule_occurrence_accepted", "schedule_id", record.ID, "occurrence_id", refs.OccurrenceID, "trigger", trigger, "request_id", requestID, "replayed", result.Replayed)
	}
	return result, err
}

func selectOccurrenceState(tx *store.Tx, id domain.ID, occurrence *domain.ScheduleOccurrence, now time.Time) error {
	sessionID := domain.NewID()
	session, preparation, err := planOccurrenceSession(tx, sessionID, id, *occurrence)
	if err != nil {
		if problem := occurrenceSelectionFailure(err); problem != nil {
			failOccurrence(occurrence, problem, now)
			return nil
		}
		return err
	}
	available, err := tx.WorkerAvailableAt(occurrence.Selection.MachineID, occurrence.DueAt, now)
	if err != nil {
		return err
	}
	if !available {
		skipOccurrence(occurrence, domain.WorkerOfflineOccurrence, now)
		return nil
	}
	if occurrence.Overlap != domain.ScheduleAllowOverlap {
		active, err := scheduleHasActiveSession(tx, occurrence.ScheduleID)
		if err != nil {
			return err
		}
		_, waiting, err := tx.FirstWaitingOccurrence(occurrence.ScheduleID)
		if err != nil {
			return err
		}
		if active || waiting {
			if occurrence.Overlap == domain.ScheduleSkipOverlap {
				skipOccurrence(occurrence, domain.OverlapSkippedOccurrence, now)
				return nil
			}
			count, err := tx.WaitingOccurrenceCount(occurrence.ScheduleID)
			if err != nil {
				return err
			}
			if count >= store.MaxWaitingOccurrences {
				skipOccurrence(occurrence, domain.WaitingLimitOccurrence, now)
				return nil
			}
			occurrence.State = domain.OccurrenceWaiting
			return nil
		}
	}
	return createOccurrenceSession(tx, sessionID, occurrence, session, preparation)
}

func scheduleHasActiveSession(tx *store.Tx, schedule domain.ID) (bool, error) {
	var after domain.ID
	for {
		page, more, err := tx.ScheduledSessions(schedule, after, 50)
		if err != nil {
			return false, err
		}
		for _, record := range page {
			session, err := store.Decode[domain.Session](record)
			if err != nil {
				return false, err
			}
			if session.Source != domain.ScheduledSession || session.ScheduleOrigin == nil || session.ScheduleOrigin.ScheduleID != schedule {
				return false, nativeCompletionUncertain()
			}
			state, _, err := scheduleSessionResult(tx, record, session)
			if err != nil || state == domain.OccurrenceActive {
				return true, err
			}
			after = record.ID
		}
		if !more {
			return false, nil
		}
	}
}

// Native terminal events, disconnected Workers and a paused dispatcher are not
// cleanup proof. A queued automatic follow-up also keeps the run active.
func scheduleSessionResult(tx *store.Tx, record store.Record, value domain.Session) (domain.OccurrenceState, *domain.Error, error) {
	if value.Recovery != domain.NoRecovery || value.ActiveExecutionID != "" || value.Archive == domain.ArchivePending || value.Outcome == domain.ExecutionRunning || value.PendingSteerID != "" || (value.NextExecutionIntent != "" && value.PendingInputs != 0 && value.Dispatch != domain.DispatchPaused) {
		return domain.OccurrenceActive, nil, nil
	}
	if value.Preparation == nil {
		return "", nil, nativeCompletionUncertain()
	}
	switch value.Preparation.State {
	case domain.PreparationPending, domain.PreparationStopping, domain.PreparationUncertain:
		return domain.OccurrenceActive, nil, nil
	}
	if value.InitialExecution == nil {
		if value.Outcome != domain.ExecutionNotStarted || value.CurrentExecution != nil || value.Execution != nil {
			return "", nil, nativeCompletionUncertain()
		}
		if value.Preparation.State == domain.PreparationReady && value.Dispatch != domain.DispatchPaused {
			return domain.OccurrenceActive, nil, nil
		}
		r, err := tx.Get(domain.JobKind, value.Preparation.JobID)
		if err != nil {
			return "", nil, err
		}
		job, err := store.Decode[domain.Job](r)
		if err != nil {
			return "", nil, err
		}
		expected := map[domain.PreparationState]domain.JobState{domain.PreparationFailed: domain.JobFailed, domain.PreparationCanceled: domain.JobCanceled, domain.PreparationReady: domain.JobSucceeded}[value.Preparation.State]
		if expected == "" || r.SessionID != record.ID || job.Type != domain.PrepareWorkspaceJob || job.MachineID != value.MachineID || job.State != expected || job.FinishedAt == nil {
			return "", nil, nativeCompletionUncertain()
		}
		if expected == domain.JobFailed {
			problem := job.Problem
			if problem == nil {
				problem = domain.Fail(domain.Unavailable, "Scheduled workspace preparation failed.", "Inspect the retained session and original preparation job.")
			}
			return domain.OccurrenceFailed, problem, nil
		}
		return domain.OccurrenceStopped, nil, nil
	}
	progress := value.Execution
	if progress == nil || !progress.CleanupVerified || progress.Waiting != (domain.NativeWaiting{}) || progress.UnconfirmedResponses != 0 || progress.ExecutionID != value.ExecutionSelection().ID || progress.Outcome != value.Outcome {
		return "", nil, nativeCompletionUncertain()
	}
	state := map[domain.ExecutionOutcome]domain.OccurrenceState{domain.ExecutionSucceeded: domain.OccurrenceSucceeded, domain.ExecutionFailed: domain.OccurrenceFailed, domain.ExecutionStopped: domain.OccurrenceStopped}[value.Outcome]
	if state == "" {
		return "", nil, nativeCompletionUncertain()
	}
	r, err := tx.SessionExecutionJob(record.ID, progress.ExecutionID)
	if err != nil {
		return "", nil, err
	}
	job, err := store.Decode[domain.Job](r)
	if err != nil {
		return "", nil, err
	}
	expected := map[domain.ExecutionOutcome]domain.JobState{domain.ExecutionSucceeded: domain.JobSucceeded, domain.ExecutionFailed: domain.JobFailed, domain.ExecutionStopped: domain.JobCanceled}[value.Outcome]
	if r.ID != progress.JobID || job.State != expected || job.FinishedAt == nil {
		return "", nil, nativeCompletionUncertain()
	}
	var assignment domain.ExecutionJobInput
	if domain.Decode(job.Input, &assignment) != nil || assignment.Validate() != nil || assignment.SessionID != record.ID || !value.OwnsExecution(assignment) {
		return "", nil, nativeCompletionUncertain()
	}
	if err := checkContinuationInputs(tx, record.ID, assignment, *progress); err != nil {
		return "", nil, err
	}
	bindings, err := domain.CheckedExecutionInputs(assignment.InputID, continuationDigest([]byte(assignment.Input.Prompt)), progress.AcceptedInputs)
	if err != nil {
		return "", nil, err
	}
	// A response claim can become uncertain before its summary counter changes.
	// Inspect original input, Steer and interaction records before releasing Wait.
	if err := tx.RequireSettledExecutionDeliveries(record.ID, progress.ExecutionID, len(bindings)); err != nil {
		return "", nil, err
	}
	var problem *domain.Error
	if state == domain.OccurrenceFailed {
		problem = value.Problem
		if problem == nil {
			problem = domain.Fail(domain.Unavailable, "The scheduled session failed.", "Inspect its retained transcript and native completion.")
		}
	}
	return state, problem, nil
}

func reconcileOccurrence(tx *store.Tx, record store.Record, now time.Time) (domain.ScheduleOccurrence, domain.Session, workspace.PrepareRequest, error) {
	var session domain.Session
	var preparation workspace.PrepareRequest
	value, err := store.Decode[domain.ScheduleOccurrence](record)
	if err != nil {
		return value, session, preparation, err
	}
	if err := value.Validate(); err != nil {
		return value, session, preparation, err
	}
	if now.Before(value.AcceptedAt) {
		return value, session, preparation, scheduleUnchanged
	}
	if value.State == domain.OccurrenceActive {
		r, current, err := sessionRecord(tx, value.SessionID)
		if err != nil {
			return value, session, preparation, err
		}
		state, problem, err := scheduleSessionResult(tx, r, current)
		if err != nil {
			return value, session, preparation, err
		}
		if state == domain.OccurrenceActive {
			return value, session, preparation, scheduleUnchanged
		}
		value.State, value.Problem, value.FinishedAt = state, problem, &now
		return value, session, preparation, nil
	}
	if value.State != domain.OccurrenceWaiting {
		return value, session, preparation, scheduleUnchanged
	}
	head, found, err := tx.FirstWaitingOccurrence(value.ScheduleID)
	if err != nil {
		return value, session, preparation, err
	}
	if !found || head.ID != record.ID {
		return value, session, preparation, scheduleUnchanged
	}
	active, err := scheduleHasActiveSession(tx, value.ScheduleID)
	if err != nil {
		return value, session, preparation, err
	}
	if active {
		return value, session, preparation, scheduleUnchanged
	}
	// Wait survives schedule deletion and disconnects. Resolve its retained
	// selection against current configuration only when it reaches the head.
	sessionID := domain.NewID()
	session, preparation, err = planOccurrenceSession(tx, sessionID, record.ID, value)
	if err != nil {
		if problem := occurrenceSelectionFailure(err); problem != nil {
			failOccurrence(&value, problem, now)
			return value, session, preparation, nil
		}
		return value, session, preparation, err
	}
	available, err := tx.WorkerAvailableAt(value.Selection.MachineID, now, now)
	if err != nil {
		return value, session, preparation, err
	}
	if !available {
		return value, session, preparation, scheduleUnchanged
	}
	value.SessionID, value.State = sessionID, domain.OccurrenceActive
	return value, session, preparation, nil
}

func (s *Service) dispatchScheduleOccurrence(ctx context.Context, record store.Record, now time.Time) error {
	// Read-only preflight avoids write transactions and durable receipts for
	// unchanged waiting/running entries. Commit rechecks all mutable authority.
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		_, _, _, err := reconcileOccurrence(tx, record, now)
		return err
	})
	if err != nil {
		return err
	}
	var state domain.OccurrenceState
	_, err = s.Store.Mutate(ctx, domain.NewID(), "schedule.reconcile", struct {
		ID       domain.ID
		Revision uint64
	}{record.ID, record.Revision}, func(tx *store.Tx) (any, error) {
		current, err := tx.Get(domain.OccurrenceKind, record.ID)
		if err != nil {
			return nil, err
		}
		if current.Revision != record.Revision {
			return nil, scheduleUnchanged
		}
		value, session, preparation, err := reconcileOccurrence(tx, current, now)
		if err != nil {
			return nil, err
		}
		if value.State == domain.OccurrenceActive {
			if err := createOccurrenceSession(tx, value.SessionID, &value, session, preparation); err != nil {
				return nil, err
			}
		}
		if _, err := tx.UpdateScheduleOccurrence(current, value); err != nil {
			return nil, err
		}
		state = value.State
		return occurrenceReceipt{ScheduleID: value.ScheduleID, OccurrenceID: current.ID}, nil
	})
	if err == nil {
		s.logger.InfoContext(ctx, "schedule_occurrence_changed", "occurrence_id", record.ID, "state", state)
	}
	return err
}

// Drain several missed instants per visit without letting one offline schedule
// monopolize the loop. This is skipped-history work, never offline execution
// replay; every instant still has its own atomic identity and timer advancement.
func (s *Service) advanceDueSchedule(ctx context.Context, record store.Record) error {
	for range 25 {
		if _, err := s.acceptScheduleOccurrence(ctx, record, domain.CronOccurrence, domain.NewID(), time.Now); err != nil {
			return err
		}
		var err error
		record, err = s.Store.Get(ctx, domain.ScheduleKind, record.ID)
		if err != nil {
			return err
		}
		value, err := store.Decode[domain.Schedule](record)
		if err != nil {
			return err
		}
		if !value.Definition.Enabled || value.NextRunAt == nil || value.NextRunAt.After(time.Now().UTC()) {
			return nil
		}
	}
	return nil
}

func (s *Service) runScheduleDispatch(parent context.Context) {
	ctx := domain.WithPrincipal(parent, domain.Principal{Type: domain.OwnerDevice})
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var dueAfter, pendingAfter domain.ID
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		for _, pending := range []bool{true, false} {
			var page []store.Record
			var more bool
			var err error
			if pending {
				page, more, err = s.Store.PendingOccurrences(ctx, pendingAfter, 25)
			} else {
				page, more, err = s.Store.DueSchedules(ctx, time.Now().UTC(), dueAfter, 25)
			}
			if err != nil {
				s.logScheduleError(ctx, "schedule_scan_failed", "", err)
				continue
			}
			for _, record := range page {
				if ctx.Err() != nil {
					return
				}
				bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
				if pending {
					pendingAfter = record.ID
					err = s.dispatchScheduleOccurrence(bounded, record, time.Now().UTC())
				} else {
					dueAfter = record.ID
					err = s.advanceDueSchedule(bounded, record)
				}
				cancel()
				s.logScheduleError(ctx, "schedule_dispatch_failed", record.ID, err)
			}
			if !more {
				if pending {
					pendingAfter = ""
				} else {
					dueAfter = ""
				}
			}
		}
	}
}

func (s *Service) logScheduleError(ctx context.Context, event string, id domain.ID, err error) {
	if err != nil && ctx.Err() == nil && !errors.Is(err, scheduleUnchanged) && domain.SafeError(err).Code != domain.NotFound {
		s.logger.WarnContext(ctx, event, "resource_id", id, "code", domain.SafeError(err).Code)
	}
}
