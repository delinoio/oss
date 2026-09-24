package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const scheduleSchema = `
CREATE INDEX schedule_due ON entities(json_extract(body,'$.definition.enabled'),json_extract(body,'$.next_run_at'),id) WHERE kind='schedule';
CREATE UNIQUE INDEX occurrence_position ON entities(json_extract(body,'$.schedule_id'),json_extract(body,'$.sequence')) WHERE kind='occurrence';
CREATE UNIQUE INDEX occurrence_cron_due ON entities(json_extract(body,'$.schedule_id'),json_extract(body,'$.due_at')) WHERE kind='occurrence' AND json_extract(body,'$.trigger')='cron';
CREATE INDEX occurrence_pending ON entities(json_extract(body,'$.state'),json_extract(body,'$.schedule_id'),json_extract(body,'$.sequence'),id) WHERE kind='occurrence';
CREATE INDEX session_schedule ON entities(json_extract(body,'$.schedule_origin.schedule_id'),id) WHERE kind='session';
ALTER TABLE worker_instances ADD COLUMN available_since INTEGER NOT NULL DEFAULT 0;
UPDATE worker_instances SET available_since=last_seen;
PRAGMA user_version=11;
`

const MaxWaitingOccurrences = 1000

func scheduleConflict() error {
	return domain.Fail(domain.Conflict, "The schedule or occurrence ownership changed.", "Reload the original schedule and retained occurrence; do not recreate accepted work.")
}

func (t *Tx) PutSchedule(id domain.ID, revision uint64, value domain.Schedule) (Record, error) {
	if err := value.Validate(); err != nil {
		return Record{}, err
	}
	if revision != 0 {
		old, err := t.Get(domain.ScheduleKind, id)
		if err != nil {
			return Record{}, err
		}
		previous, err := Decode[domain.Schedule](old)
		if err != nil {
			return Record{}, err
		}
		if old.Revision != revision || value.ConfigurationRevision < previous.ConfigurationRevision || value.ConfigurationRevision > previous.ConfigurationRevision+1 || value.LastOccurrence < previous.LastOccurrence || value.LastOccurrence > previous.LastOccurrence+1 || value.CreatedBy != previous.CreatedBy {
			return Record{}, scheduleConflict()
		}
		if value.ConfigurationRevision == previous.ConfigurationRevision {
			// Timer progress cannot smuggle configuration or Local-origin edits.
			before, _ := json.Marshal(previous.Definition)
			after, _ := json.Marshal(value.Definition)
			priorOrigin, _ := json.Marshal(previous.LocalOrigin)
			nextOrigin, _ := json.Marshal(value.LocalOrigin)
			priorProblem, _ := json.Marshal(previous.Problem)
			nextProblem, _ := json.Marshal(value.Problem)
			if string(before) != string(after) || string(priorOrigin) != string(nextOrigin) || string(priorProblem) != string(nextProblem) {
				return Record{}, scheduleConflict()
			}
		}
	} else if value.ConfigurationRevision != 1 || value.LastOccurrence != 0 {
		return Record{}, scheduleConflict()
	}
	return t.Put(domain.ScheduleKind, id, revision, "", value.Definition.ProjectID, value)
}

// Only one transaction can allocate this next sequence. The cron-time unique
// index independently prevents a second occurrence after an interrupted retry.
func (t *Tx) AppendScheduleOccurrence(id domain.ID, schedule Record, next domain.Schedule, occurrence domain.ScheduleOccurrence) (Record, error) {
	if err := id.Validate(); err != nil {
		return Record{}, err
	}
	if err := occurrence.Validate(); err != nil {
		return Record{}, err
	}
	current, err := t.Get(domain.ScheduleKind, schedule.ID)
	if err != nil {
		return Record{}, err
	}
	original, err := Decode[domain.Schedule](current)
	if err != nil {
		return Record{}, err
	}
	if current.Revision != schedule.Revision || occurrence.ScheduleID != schedule.ID || occurrence.ConfigurationRevision != original.ConfigurationRevision || occurrence.Sequence != original.LastOccurrence+1 || next.LastOccurrence != occurrence.Sequence || next.ConfigurationRevision != original.ConfigurationRevision {
		return Record{}, scheduleConflict()
	}
	selection, _ := json.Marshal(original.Definition.Selection())
	accepted, _ := json.Marshal(occurrence.Selection)
	origin, _ := json.Marshal(original.LocalOrigin)
	acceptedOrigin, _ := json.Marshal(occurrence.LocalOrigin)
	if string(selection) != string(accepted) || string(origin) != string(acceptedOrigin) || occurrence.Overlap != original.Definition.Overlap {
		return Record{}, scheduleConflict()
	}
	if occurrence.Trigger == domain.CronOccurrence && (!original.Definition.Enabled || original.NextRunAt == nil || !occurrence.DueAt.Equal(*original.NextRunAt)) {
		return Record{}, scheduleConflict()
	}
	if err := t.validateOccurrenceSession(id, occurrence); err != nil {
		return Record{}, err
	}
	if occurrence.Trigger == domain.CronOccurrence {
		expected, err := original.Definition.NextRun(occurrence.DueAt)
		if err != nil {
			return Record{}, err
		}
		if next.NextRunAt == nil || !next.NextRunAt.Equal(expected) {
			return Record{}, scheduleConflict()
		}
	} else {
		before, _ := json.Marshal(original.NextRunAt)
		after, _ := json.Marshal(next.NextRunAt)
		if string(before) != string(after) {
			return Record{}, scheduleConflict()
		}
	}
	if occurrence.State == domain.OccurrenceWaiting {
		count, err := t.WaitingOccurrenceCount(schedule.ID)
		if err != nil {
			return Record{}, err
		}
		if count >= MaxWaitingOccurrences {
			return Record{}, domain.Fail(domain.ResourceExhausted, "The accepted schedule wait queue is full.", "Inspect its preceding sessions and retained wait occurrences before accepting more.")
		}
	}
	if _, err := t.PutSchedule(schedule.ID, schedule.Revision, next); err != nil {
		return Record{}, err
	}
	return t.Put(domain.OccurrenceKind, id, 0, occurrence.SessionID, occurrence.Selection.ProjectID, occurrence)
}

func (t *Tx) UpdateScheduleOccurrence(record Record, value domain.ScheduleOccurrence) (Record, error) {
	if record.Kind != domain.OccurrenceKind || value.Validate() != nil {
		return Record{}, scheduleConflict()
	}
	current, err := t.Get(domain.OccurrenceKind, record.ID)
	if err != nil {
		return Record{}, err
	}
	old, err := Decode[domain.ScheduleOccurrence](current)
	if err != nil {
		return Record{}, err
	}
	if current.Revision != record.Revision || old.State.Terminal() {
		return Record{}, scheduleConflict()
	}
	if old.State == domain.OccurrenceWaiting {
		if value.State != domain.OccurrenceActive && value.State != domain.OccurrenceFailed {
			return Record{}, scheduleConflict()
		}
	} else if old.State == domain.OccurrenceActive {
		if !value.State.Terminal() || value.State == domain.OccurrenceSkipped || old.SessionID != value.SessionID {
			return Record{}, scheduleConflict()
		}
	} else {
		return Record{}, scheduleConflict()
	}
	// State/result publication cannot rewrite the immutable accepted selection.
	comparison := value
	comparison.State, comparison.SessionID, comparison.Reason, comparison.Problem, comparison.FinishedAt = old.State, old.SessionID, old.Reason, old.Problem, old.FinishedAt
	before, _ := json.Marshal(old)
	after, _ := json.Marshal(comparison)
	if string(before) != string(after) {
		return Record{}, scheduleConflict()
	}
	if err := t.validateOccurrenceSession(record.ID, value); err != nil {
		return Record{}, err
	}
	return t.Put(domain.OccurrenceKind, record.ID, record.Revision, value.SessionID, value.Selection.ProjectID, value)
}

func (t *Tx) WaitingOccurrenceCount(schedule domain.ID) (int, error) {
	if err := schedule.Validate(); err != nil {
		return 0, err
	}
	var count int
	err := t.tx.QueryRowContext(t.ctx, "SELECT COUNT(*) FROM entities WHERE kind='occurrence' AND json_extract(body,'$.schedule_id')=? AND json_extract(body,'$.state')='waiting'", schedule).Scan(&count)
	return count, storageError(err)
}

func (t *Tx) FirstWaitingOccurrence(schedule domain.ID) (Record, bool, error) {
	if err := schedule.Validate(); err != nil {
		return Record{}, false, err
	}
	r, err := scan(t.tx.QueryRowContext(t.ctx, "SELECT "+recordColumns+" FROM entities WHERE kind='occurrence' AND json_extract(body,'$.schedule_id')=? AND json_extract(body,'$.state')='waiting' ORDER BY json_extract(body,'$.sequence') LIMIT 1", schedule))
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, false, nil
	}
	return r, err == nil, storageError(err)
}

// Due scans and maintenance are paged independently; callers recheck each
// accepted revision in a bounded mutation and perform no native work here.
func (s *Store) DueSchedules(ctx context.Context, now time.Time, after domain.ID, limit int) ([]Record, bool, error) {
	if err := (Filter{Kind: domain.ScheduleKind, After: after, Limit: limit}).validate(); err != nil {
		return nil, false, err
	}
	var rows []Record
	var more bool
	err := s.Read(ctx, func(t *Tx) error {
		var err error
		rows, more, err = t.sessionPage(limit, "SELECT "+recordColumns+" FROM entities WHERE kind='schedule' AND id>? AND json_extract(body,'$.definition.enabled')=1 AND json_extract(body,'$.next_run_at')<=? ORDER BY id LIMIT ?", after, now.UTC().Truncate(time.Second).Format(time.RFC3339), limit+1)
		return err
	})
	return rows, more, err
}

func (s *Store) PendingOccurrences(ctx context.Context, after domain.ID, limit int) ([]Record, bool, error) {
	if err := (Filter{Kind: domain.OccurrenceKind, After: after, Limit: limit}).validate(); err != nil {
		return nil, false, err
	}
	var rows []Record
	var more bool
	err := s.Read(ctx, func(t *Tx) error {
		var err error
		rows, more, err = t.sessionPage(limit, "SELECT "+recordColumns+" FROM entities WHERE kind='occurrence' AND id>? AND json_extract(body,'$.state') IN ('waiting','active') ORDER BY id LIMIT ?", after, limit+1)
		return err
	})
	return rows, more, err
}

func (t *Tx) OccurrenceHistory(schedule domain.ID, after uint64, limit int) ([]Record, bool, error) {
	if err := schedule.Validate(); err != nil {
		return nil, false, err
	}
	if limit < 1 || limit > MaxPage || after >= 1<<63-1 {
		return nil, false, domain.Fail(domain.InvalidArgument, "Invalid occurrence history page.", "Use a bounded page and the server's retained occurrence cursor.")
	}
	// History remains readable after schedule configuration deletion.
	return t.sessionPage(limit, "SELECT "+recordColumns+" FROM entities WHERE kind='occurrence' AND json_extract(body,'$.schedule_id')=? AND json_extract(body,'$.sequence')>? ORDER BY json_extract(body,'$.sequence') LIMIT ?", schedule, after, limit+1)
}

// WorkerAvailableAt proves the due instant is covered by the current observed
// lease, not merely by a reconnect after it. Lease gaps and clock rollback reset
// available_since; imported pre-v11 leases begin at their last observed beat.
func (t *Tx) WorkerAvailableAt(machine domain.ID, due, now time.Time) (bool, error) {
	if err := machine.Validate(); err != nil {
		return false, err
	}
	if due.IsZero() || now.IsZero() || due.After(now) {
		return false, domain.Fail(domain.InvalidArgument, "Invalid Worker availability boundary.", "Compare the due instant with the current server time.")
	}
	var since, seen int64
	err := t.tx.QueryRowContext(t.ctx, "SELECT available_since,last_seen FROM worker_instances WHERE machine_id=?", machine).Scan(&since, &seen)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, storageError(err)
	}
	return since > 0 && since <= due.UnixMilli() && seen >= since && seen <= now.UnixMilli() && now.UnixMilli()-seen <= 45000 && due.UnixMilli() <= seen+45000, nil
}

func (t *Tx) validateOccurrenceSession(id domain.ID, value domain.ScheduleOccurrence) error {
	if value.SessionID == "" {
		return nil
	}
	r, err := t.Get(domain.SessionKind, value.SessionID)
	if err != nil {
		return err
	}
	session, err := Decode[domain.Session](r)
	if err != nil {
		return err
	}
	expected := domain.ScheduleOrigin{ScheduleID: value.ScheduleID, OccurrenceID: id, ConfigurationRevision: value.ConfigurationRevision, Trigger: value.Trigger}
	if r.ProjectID != value.Selection.ProjectID || session.Source != domain.ScheduledSession || session.ScheduleOrigin == nil || *session.ScheduleOrigin != expected || session.ProjectID != value.Selection.ProjectID || session.MachineID != value.Selection.MachineID || session.Workspace != value.Selection.Workspace || session.AgentID != value.Selection.AgentID {
		return scheduleConflict()
	}
	return nil
}
