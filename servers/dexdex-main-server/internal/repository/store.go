package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	v1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
	_ "github.com/jackc/pgx/v5/stdlib"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
	_ "modernc.org/sqlite"
)

var (
	ErrNotFound = errors.New("not found")
	ErrNoPRLink = errors.New("no pr tracking id linked to unit task")
)

type Store struct {
	db         *sql.DB
	isPostgres bool
}

func NewSQLite(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create sqlite directory: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}

	store := &Store{db: db, isPostgres: false}
	if err := store.ensureSchema(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}

	return store, nil
}

func NewPostgres(dsn string) (*Store, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres database: %w", err)
	}

	store := &Store{db: db, isPostgres: true}
	if err := store.ensureSchema(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}

	return store, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) CreateUnitTask(ctx context.Context, workspaceID string, title string) (*v1.UnitTask, error) {
	now := time.Now().UTC().UnixMilli()
	unitTask := &v1.UnitTask{
		UnitTaskId:     buildID("unit"),
		WorkspaceId:    workspaceID,
		Title:          strings.TrimSpace(title),
		Status:         v1.UnitTaskStatus_UNIT_TASK_STATUS_QUEUED,
		ActionRequired: v1.ActionType_ACTION_TYPE_UNSPECIFIED,
		CreatedAt:      timestampFromUnixMilli(now),
		UpdatedAt:      timestampFromUnixMilli(now),
	}

	if s.isPostgres {
		_, err := s.db.ExecContext(
			ctx,
			`INSERT INTO unit_tasks(unit_task_id, workspace_id, title, status, action_required, created_at_ms, updated_at_ms)
			 VALUES($1, $2, $3, $4, $5, $6, $7)`,
			unitTask.UnitTaskId,
			unitTask.WorkspaceId,
			unitTask.Title,
			int32(unitTask.Status),
			int32(unitTask.ActionRequired),
			now,
			now,
		)
		if err != nil {
			return nil, fmt.Errorf("insert unit task: %w", err)
		}
	} else {
		_, err := s.db.ExecContext(
			ctx,
			`INSERT INTO unit_tasks(unit_task_id, workspace_id, title, status, action_required, created_at_ms, updated_at_ms)
			 VALUES(?, ?, ?, ?, ?, ?, ?)`,
			unitTask.UnitTaskId,
			unitTask.WorkspaceId,
			unitTask.Title,
			int32(unitTask.Status),
			int32(unitTask.ActionRequired),
			now,
			now,
		)
		if err != nil {
			return nil, fmt.Errorf("insert unit task: %w", err)
		}
	}

	return unitTask, nil
}

func (s *Store) GetUnitTask(ctx context.Context, workspaceID string, unitTaskID string) (*v1.UnitTask, error) {
	var row *sql.Row
	if s.isPostgres {
		row = s.db.QueryRowContext(
			ctx,
			`SELECT unit_task_id, workspace_id, title, status, action_required, created_at_ms, updated_at_ms
			 FROM unit_tasks
			 WHERE workspace_id=$1 AND unit_task_id=$2`,
			workspaceID,
			unitTaskID,
		)
	} else {
		row = s.db.QueryRowContext(
			ctx,
			`SELECT unit_task_id, workspace_id, title, status, action_required, created_at_ms, updated_at_ms
			 FROM unit_tasks
			 WHERE workspace_id=? AND unit_task_id=?`,
			workspaceID,
			unitTaskID,
		)
	}

	return scanUnitTask(row)
}

func (s *Store) ListUnitTasks(ctx context.Context, workspaceID string, pageSize int32, pageToken string) ([]*v1.UnitTask, string, error) {
	limit := normalizePageSize(pageSize)
	offset := parsePageToken(pageToken)

	var rows *sql.Rows
	var err error
	if s.isPostgres {
		rows, err = s.db.QueryContext(
			ctx,
			`SELECT unit_task_id, workspace_id, title, status, action_required, created_at_ms, updated_at_ms
			 FROM unit_tasks
			 WHERE workspace_id=$1
			 ORDER BY created_at_ms DESC, unit_task_id DESC
			 LIMIT $2 OFFSET $3`,
			workspaceID,
			limit+1,
			offset,
		)
	} else {
		rows, err = s.db.QueryContext(
			ctx,
			`SELECT unit_task_id, workspace_id, title, status, action_required, created_at_ms, updated_at_ms
			 FROM unit_tasks
			 WHERE workspace_id=?
			 ORDER BY created_at_ms DESC, unit_task_id DESC
			 LIMIT ? OFFSET ?`,
			workspaceID,
			limit+1,
			offset,
		)
	}
	if err != nil {
		return nil, "", fmt.Errorf("list unit tasks: %w", err)
	}
	defer rows.Close()

	unitTasks := make([]*v1.UnitTask, 0, limit)
	for rows.Next() {
		unitTask, scanErr := scanUnitTask(rows)
		if scanErr != nil {
			return nil, "", scanErr
		}
		unitTasks = append(unitTasks, unitTask)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate unit tasks: %w", err)
	}

	nextToken := ""
	if len(unitTasks) > limit {
		unitTasks = unitTasks[:limit]
		nextToken = strconv.Itoa(offset + limit)
	}

	return unitTasks, nextToken, nil
}

func (s *Store) CreateSubTask(ctx context.Context, workspaceID string, unitTaskID string, subTaskType v1.SubTaskType, prompt string, status v1.SubTaskStatus) (*v1.SubTask, error) {
	now := time.Now().UTC().UnixMilli()
	subTask := &v1.SubTask{
		SubTaskId:   buildID("sub"),
		WorkspaceId: workspaceID,
		UnitTaskId:  unitTaskID,
		Type:        subTaskType,
		Status:      status,
		Prompt:      strings.TrimSpace(prompt),
		CreatedAt:   timestampFromUnixMilli(now),
		UpdatedAt:   timestampFromUnixMilli(now),
	}

	if s.isPostgres {
		_, err := s.db.ExecContext(
			ctx,
			`INSERT INTO sub_tasks(sub_task_id, workspace_id, unit_task_id, type, status, completion_reason, prompt, revision_note, created_at_ms, updated_at_ms)
			 VALUES($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			subTask.SubTaskId,
			subTask.WorkspaceId,
			subTask.UnitTaskId,
			int32(subTask.Type),
			int32(subTask.Status),
			int32(v1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_UNSPECIFIED),
			subTask.Prompt,
			"",
			now,
			now,
		)
		if err != nil {
			return nil, fmt.Errorf("insert sub task: %w", err)
		}
	} else {
		_, err := s.db.ExecContext(
			ctx,
			`INSERT INTO sub_tasks(sub_task_id, workspace_id, unit_task_id, type, status, completion_reason, prompt, revision_note, created_at_ms, updated_at_ms)
			 VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			subTask.SubTaskId,
			subTask.WorkspaceId,
			subTask.UnitTaskId,
			int32(subTask.Type),
			int32(subTask.Status),
			int32(v1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_UNSPECIFIED),
			subTask.Prompt,
			"",
			now,
			now,
		)
		if err != nil {
			return nil, fmt.Errorf("insert sub task: %w", err)
		}
	}

	return subTask, nil
}

func (s *Store) GetSubTask(ctx context.Context, workspaceID string, subTaskID string) (*v1.SubTask, error) {
	var row *sql.Row
	if s.isPostgres {
		row = s.db.QueryRowContext(
			ctx,
			`SELECT sub_task_id, workspace_id, unit_task_id, type, status, completion_reason, prompt, revision_note, created_at_ms, updated_at_ms
			 FROM sub_tasks
			 WHERE workspace_id=$1 AND sub_task_id=$2`,
			workspaceID,
			subTaskID,
		)
	} else {
		row = s.db.QueryRowContext(
			ctx,
			`SELECT sub_task_id, workspace_id, unit_task_id, type, status, completion_reason, prompt, revision_note, created_at_ms, updated_at_ms
			 FROM sub_tasks
			 WHERE workspace_id=? AND sub_task_id=?`,
			workspaceID,
			subTaskID,
		)
	}

	return scanSubTask(row)
}

func (s *Store) ListSubTasks(ctx context.Context, workspaceID string, unitTaskID string, pageSize int32, pageToken string) ([]*v1.SubTask, string, error) {
	limit := normalizePageSize(pageSize)
	offset := parsePageToken(pageToken)

	var rows *sql.Rows
	var err error
	if s.isPostgres {
		rows, err = s.db.QueryContext(
			ctx,
			`SELECT sub_task_id, workspace_id, unit_task_id, type, status, completion_reason, prompt, revision_note, created_at_ms, updated_at_ms
			 FROM sub_tasks
			 WHERE workspace_id=$1 AND unit_task_id=$2
			 ORDER BY created_at_ms DESC, sub_task_id DESC
			 LIMIT $3 OFFSET $4`,
			workspaceID,
			unitTaskID,
			limit+1,
			offset,
		)
	} else {
		rows, err = s.db.QueryContext(
			ctx,
			`SELECT sub_task_id, workspace_id, unit_task_id, type, status, completion_reason, prompt, revision_note, created_at_ms, updated_at_ms
			 FROM sub_tasks
			 WHERE workspace_id=? AND unit_task_id=?
			 ORDER BY created_at_ms DESC, sub_task_id DESC
			 LIMIT ? OFFSET ?`,
			workspaceID,
			unitTaskID,
			limit+1,
			offset,
		)
	}
	if err != nil {
		return nil, "", fmt.Errorf("list sub tasks: %w", err)
	}
	defer rows.Close()

	subTasks := make([]*v1.SubTask, 0, limit)
	for rows.Next() {
		subTask, scanErr := scanSubTask(rows)
		if scanErr != nil {
			return nil, "", scanErr
		}
		subTasks = append(subTasks, subTask)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate sub tasks: %w", err)
	}

	nextToken := ""
	if len(subTasks) > limit {
		subTasks = subTasks[:limit]
		nextToken = strconv.Itoa(offset + limit)
	}

	return subTasks, nextToken, nil
}

func (s *Store) SubmitPlanDecision(ctx context.Context, workspaceID string, subTaskID string, decision v1.PlanDecision, revisionNote string) (*v1.SubTask, *v1.SubTask, v1.PlanDecisionValidationErrorCode, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, v1.PlanDecisionValidationErrorCode_PLAN_DECISION_VALIDATION_ERROR_CODE_INTERNAL, fmt.Errorf("begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	subTask, err := scanSubTask(tx.QueryRowContext(ctx,
		s.queryForSubTask(),
		workspaceID,
		subTaskID,
	))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil, v1.PlanDecisionValidationErrorCode_PLAN_DECISION_VALIDATION_ERROR_CODE_SUB_TASK_NOT_FOUND, nil
		}
		return nil, nil, v1.PlanDecisionValidationErrorCode_PLAN_DECISION_VALIDATION_ERROR_CODE_INTERNAL, err
	}

	if subTask.Status != v1.SubTaskStatus_SUB_TASK_STATUS_WAITING_FOR_PLAN_APPROVAL {
		return nil, nil, v1.PlanDecisionValidationErrorCode_PLAN_DECISION_VALIDATION_ERROR_CODE_INVALID_SUB_TASK_STATUS, nil
	}

	now := time.Now().UTC().UnixMilli()
	updated := cloneSubTask(subTask)
	var created *v1.SubTask

	switch decision {
	case v1.PlanDecision_PLAN_DECISION_APPROVE:
		updated.Status = v1.SubTaskStatus_SUB_TASK_STATUS_IN_PROGRESS
		updated.UpdatedAt = timestampFromUnixMilli(now)
		if err = s.updateSubTask(ctx, tx, updated, now); err != nil {
			return nil, nil, v1.PlanDecisionValidationErrorCode_PLAN_DECISION_VALIDATION_ERROR_CODE_INTERNAL, err
		}
	case v1.PlanDecision_PLAN_DECISION_REVISE:
		note := strings.TrimSpace(revisionNote)
		if note == "" {
			return nil, nil, v1.PlanDecisionValidationErrorCode_PLAN_DECISION_VALIDATION_ERROR_CODE_REVISION_NOTE_REQUIRED, nil
		}
		updated.Status = v1.SubTaskStatus_SUB_TASK_STATUS_COMPLETED
		updated.CompletionReason = v1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_REVISED
		updated.RevisionNote = note
		updated.UpdatedAt = timestampFromUnixMilli(now)
		if err = s.updateSubTask(ctx, tx, updated, now); err != nil {
			return nil, nil, v1.PlanDecisionValidationErrorCode_PLAN_DECISION_VALIDATION_ERROR_CODE_INTERNAL, err
		}
		created = &v1.SubTask{
			SubTaskId:   buildID("sub"),
			WorkspaceId: workspaceID,
			UnitTaskId:  subTask.UnitTaskId,
			Type:        v1.SubTaskType_SUB_TASK_TYPE_REQUEST_CHANGES,
			Status:      v1.SubTaskStatus_SUB_TASK_STATUS_QUEUED,
			Prompt:      note,
			CreatedAt:   timestampFromUnixMilli(now),
			UpdatedAt:   timestampFromUnixMilli(now),
		}
		if err = s.insertSubTask(ctx, tx, created, now); err != nil {
			return nil, nil, v1.PlanDecisionValidationErrorCode_PLAN_DECISION_VALIDATION_ERROR_CODE_INTERNAL, err
		}
	case v1.PlanDecision_PLAN_DECISION_REJECT:
		updated.Status = v1.SubTaskStatus_SUB_TASK_STATUS_CANCELLED
		updated.CompletionReason = v1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_PLAN_REJECTED
		updated.UpdatedAt = timestampFromUnixMilli(now)
		if err = s.updateSubTask(ctx, tx, updated, now); err != nil {
			return nil, nil, v1.PlanDecisionValidationErrorCode_PLAN_DECISION_VALIDATION_ERROR_CODE_INTERNAL, err
		}
	default:
		return nil, nil, v1.PlanDecisionValidationErrorCode_PLAN_DECISION_VALIDATION_ERROR_CODE_INVALID_SUB_TASK_STATUS, nil
	}

	if err = tx.Commit(); err != nil {
		return nil, nil, v1.PlanDecisionValidationErrorCode_PLAN_DECISION_VALIDATION_ERROR_CODE_INTERNAL, fmt.Errorf("commit tx: %w", err)
	}
	committed = true

	return updated, created, v1.PlanDecisionValidationErrorCode_PLAN_DECISION_VALIDATION_ERROR_CODE_UNSPECIFIED, nil
}

func (s *Store) ResolveUnitTaskPRTrackingID(ctx context.Context, workspaceID string, unitTaskID string) (string, error) {
	unitTask, err := s.GetUnitTask(ctx, workspaceID, unitTaskID)
	if err != nil {
		return "", err
	}

	prTrackingID := extractPRTrackingID(unitTask.Title)
	if prTrackingID == "" {
		return "", ErrNoPRLink
	}

	return prTrackingID, nil
}

func (s *Store) AppendWorkspaceEvent(ctx context.Context, workspaceID string, event *v1.StreamWorkspaceEventsResponse) (*v1.StreamWorkspaceEventsResponse, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	sequence, err := s.nextWorkspaceSequence(ctx, tx, workspaceID)
	if err != nil {
		return nil, err
	}
	event.Sequence = sequence
	event.WorkspaceId = workspaceID
	if event.OccurredAt == nil {
		event.OccurredAt = timestampFromUnixMilli(time.Now().UTC().UnixMilli())
	}

	payloadBytes, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(event)
	if err != nil {
		return nil, fmt.Errorf("marshal event: %w", err)
	}

	if s.isPostgres {
		_, err = tx.ExecContext(
			ctx,
			`INSERT INTO workspace_events(workspace_id, sequence, event_type, occurred_at_ms, payload_json)
			 VALUES($1, $2, $3, $4, $5)`,
			workspaceID,
			sequence,
			int32(event.EventType),
			event.OccurredAt.AsTime().UTC().UnixMilli(),
			string(payloadBytes),
		)
	} else {
		_, err = tx.ExecContext(
			ctx,
			`INSERT INTO workspace_events(workspace_id, sequence, event_type, occurred_at_ms, payload_json)
			 VALUES(?, ?, ?, ?, ?)`,
			workspaceID,
			sequence,
			int32(event.EventType),
			event.OccurredAt.AsTime().UTC().UnixMilli(),
			string(payloadBytes),
		)
	}
	if err != nil {
		return nil, fmt.Errorf("insert workspace event: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit workspace event: %w", err)
	}

	return event, nil
}

func (s *Store) ListWorkspaceEvents(ctx context.Context, workspaceID string, fromSequence uint64, limit int) ([]*v1.StreamWorkspaceEventsResponse, uint64, error) {
	if limit <= 0 {
		limit = 200
	}

	var earliest uint64
	var earliestRow *sql.Row
	if s.isPostgres {
		earliestRow = s.db.QueryRowContext(ctx, `SELECT COALESCE(MIN(sequence), 0) FROM workspace_events WHERE workspace_id=$1`, workspaceID)
	} else {
		earliestRow = s.db.QueryRowContext(ctx, `SELECT COALESCE(MIN(sequence), 0) FROM workspace_events WHERE workspace_id=?`, workspaceID)
	}
	if err := earliestRow.Scan(&earliest); err != nil {
		return nil, 0, fmt.Errorf("get earliest sequence: %w", err)
	}

	var rows *sql.Rows
	var err error
	if s.isPostgres {
		rows, err = s.db.QueryContext(
			ctx,
			`SELECT payload_json FROM workspace_events WHERE workspace_id=$1 AND sequence>$2 ORDER BY sequence ASC LIMIT $3`,
			workspaceID,
			fromSequence,
			limit,
		)
	} else {
		rows, err = s.db.QueryContext(
			ctx,
			`SELECT payload_json FROM workspace_events WHERE workspace_id=? AND sequence>? ORDER BY sequence ASC LIMIT ?`,
			workspaceID,
			fromSequence,
			limit,
		)
	}
	if err != nil {
		return nil, earliest, fmt.Errorf("list workspace events: %w", err)
	}
	defer rows.Close()

	events := make([]*v1.StreamWorkspaceEventsResponse, 0, limit)
	for rows.Next() {
		var payloadJSON string
		if err := rows.Scan(&payloadJSON); err != nil {
			return nil, earliest, fmt.Errorf("scan workspace event: %w", err)
		}
		event := &v1.StreamWorkspaceEventsResponse{}
		if err := protojson.Unmarshal([]byte(payloadJSON), event); err != nil {
			return nil, earliest, fmt.Errorf("decode workspace event: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, earliest, fmt.Errorf("iterate workspace events: %w", err)
	}

	return events, earliest, nil
}

func (s *Store) AppendSessionOutput(ctx context.Context, workspaceID string, sessionID string, kind v1.SessionOutputKind, body string) (*v1.SessionOutputEvent, uint64, error) {
	now := time.Now().UTC().UnixMilli()
	offset, err := s.nextSessionOffset(ctx, workspaceID, sessionID)
	if err != nil {
		return nil, 0, err
	}

	if s.isPostgres {
		_, err = s.db.ExecContext(
			ctx,
			`INSERT INTO session_outputs(workspace_id, session_id, output_offset, kind, body, occurred_at_ms)
			 VALUES($1, $2, $3, $4, $5, $6)`,
			workspaceID,
			sessionID,
			offset,
			int32(kind),
			body,
			now,
		)
	} else {
		_, err = s.db.ExecContext(
			ctx,
			`INSERT INTO session_outputs(workspace_id, session_id, output_offset, kind, body, occurred_at_ms)
			 VALUES(?, ?, ?, ?, ?, ?)`,
			workspaceID,
			sessionID,
			offset,
			int32(kind),
			body,
			now,
		)
	}
	if err != nil {
		return nil, 0, fmt.Errorf("insert session output: %w", err)
	}

	event := &v1.SessionOutputEvent{
		SessionId:  sessionID,
		Kind:       kind,
		Body:       body,
		OccurredAt: timestampFromUnixMilli(now),
	}
	return event, offset + 1, nil
}

func (s *Store) GetSessionOutputs(ctx context.Context, workspaceID string, sessionID string) ([]*v1.SessionOutputEvent, error) {
	var rows *sql.Rows
	var err error
	if s.isPostgres {
		rows, err = s.db.QueryContext(
			ctx,
			`SELECT kind, body, occurred_at_ms FROM session_outputs
			 WHERE workspace_id=$1 AND session_id=$2
			 ORDER BY output_offset ASC`,
			workspaceID,
			sessionID,
		)
	} else {
		rows, err = s.db.QueryContext(
			ctx,
			`SELECT kind, body, occurred_at_ms FROM session_outputs
			 WHERE workspace_id=? AND session_id=?
			 ORDER BY output_offset ASC`,
			workspaceID,
			sessionID,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("query session outputs: %w", err)
	}
	defer rows.Close()

	events := make([]*v1.SessionOutputEvent, 0, 64)
	for rows.Next() {
		var kind int32
		var body string
		var occurredAt int64
		if err := rows.Scan(&kind, &body, &occurredAt); err != nil {
			return nil, fmt.Errorf("scan session output: %w", err)
		}
		events = append(events, &v1.SessionOutputEvent{
			SessionId:  sessionID,
			Kind:       v1.SessionOutputKind(kind),
			Body:       body,
			OccurredAt: timestampFromUnixMilli(occurredAt),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate session outputs: %w", err)
	}

	return events, nil
}

func (s *Store) queryForSubTask() string {
	if s.isPostgres {
		return `SELECT sub_task_id, workspace_id, unit_task_id, type, status, completion_reason, prompt, revision_note, created_at_ms, updated_at_ms
			FROM sub_tasks
			WHERE workspace_id=$1 AND sub_task_id=$2`
	}

	return `SELECT sub_task_id, workspace_id, unit_task_id, type, status, completion_reason, prompt, revision_note, created_at_ms, updated_at_ms
		FROM sub_tasks
		WHERE workspace_id=? AND sub_task_id=?`
}

func (s *Store) insertSubTask(ctx context.Context, tx *sql.Tx, subTask *v1.SubTask, now int64) error {
	if s.isPostgres {
		_, err := tx.ExecContext(
			ctx,
			`INSERT INTO sub_tasks(sub_task_id, workspace_id, unit_task_id, type, status, completion_reason, prompt, revision_note, created_at_ms, updated_at_ms)
			 VALUES($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			subTask.SubTaskId,
			subTask.WorkspaceId,
			subTask.UnitTaskId,
			int32(subTask.Type),
			int32(subTask.Status),
			int32(subTask.CompletionReason),
			subTask.Prompt,
			subTask.RevisionNote,
			now,
			now,
		)
		if err != nil {
			return fmt.Errorf("insert follow-up sub task: %w", err)
		}
		return nil
	}

	_, err := tx.ExecContext(
		ctx,
		`INSERT INTO sub_tasks(sub_task_id, workspace_id, unit_task_id, type, status, completion_reason, prompt, revision_note, created_at_ms, updated_at_ms)
		 VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		subTask.SubTaskId,
		subTask.WorkspaceId,
		subTask.UnitTaskId,
		int32(subTask.Type),
		int32(subTask.Status),
		int32(subTask.CompletionReason),
		subTask.Prompt,
		subTask.RevisionNote,
		now,
		now,
	)
	if err != nil {
		return fmt.Errorf("insert follow-up sub task: %w", err)
	}

	return nil
}

func (s *Store) updateSubTask(ctx context.Context, tx *sql.Tx, subTask *v1.SubTask, now int64) error {
	if s.isPostgres {
		_, err := tx.ExecContext(
			ctx,
			`UPDATE sub_tasks
			 SET status=$1, completion_reason=$2, revision_note=$3, updated_at_ms=$4
			 WHERE workspace_id=$5 AND sub_task_id=$6`,
			int32(subTask.Status),
			int32(subTask.CompletionReason),
			subTask.RevisionNote,
			now,
			subTask.WorkspaceId,
			subTask.SubTaskId,
		)
		if err != nil {
			return fmt.Errorf("update sub task: %w", err)
		}
		return nil
	}

	_, err := tx.ExecContext(
		ctx,
		`UPDATE sub_tasks
		 SET status=?, completion_reason=?, revision_note=?, updated_at_ms=?
		 WHERE workspace_id=? AND sub_task_id=?`,
		int32(subTask.Status),
		int32(subTask.CompletionReason),
		subTask.RevisionNote,
		now,
		subTask.WorkspaceId,
		subTask.SubTaskId,
	)
	if err != nil {
		return fmt.Errorf("update sub task: %w", err)
	}
	return nil
}

func (s *Store) nextWorkspaceSequence(ctx context.Context, tx *sql.Tx, workspaceID string) (uint64, error) {
	var current sql.NullInt64
	if s.isPostgres {
		if err := tx.QueryRowContext(ctx, `SELECT MAX(sequence) FROM workspace_events WHERE workspace_id=$1`, workspaceID).Scan(&current); err != nil {
			return 0, fmt.Errorf("query max workspace sequence: %w", err)
		}
	} else {
		if err := tx.QueryRowContext(ctx, `SELECT MAX(sequence) FROM workspace_events WHERE workspace_id=?`, workspaceID).Scan(&current); err != nil {
			return 0, fmt.Errorf("query max workspace sequence: %w", err)
		}
	}
	if !current.Valid {
		return 1, nil
	}
	return uint64(current.Int64 + 1), nil
}

func (s *Store) nextSessionOffset(ctx context.Context, workspaceID string, sessionID string) (uint64, error) {
	var maxOffset sql.NullInt64
	if s.isPostgres {
		if err := s.db.QueryRowContext(ctx, `SELECT MAX(output_offset) FROM session_outputs WHERE workspace_id=$1 AND session_id=$2`, workspaceID, sessionID).Scan(&maxOffset); err != nil {
			return 0, fmt.Errorf("query max output offset: %w", err)
		}
	} else {
		if err := s.db.QueryRowContext(ctx, `SELECT MAX(output_offset) FROM session_outputs WHERE workspace_id=? AND session_id=?`, workspaceID, sessionID).Scan(&maxOffset); err != nil {
			return 0, fmt.Errorf("query max output offset: %w", err)
		}
	}

	if !maxOffset.Valid {
		return 0, nil
	}
	return uint64(maxOffset.Int64 + 1), nil
}

func (s *Store) ensureSchema(ctx context.Context) error {
	if s.isPostgres {
		for _, statement := range postgresSchemaStatements {
			if _, err := s.db.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("apply postgres schema: %w", err)
			}
		}
		return nil
	}

	for _, statement := range sqliteSchemaStatements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply sqlite schema: %w", err)
		}
	}
	return nil
}

func normalizePageSize(pageSize int32) int {
	if pageSize <= 0 {
		return 20
	}
	if pageSize > 100 {
		return 100
	}
	return int(pageSize)
}

func parsePageToken(pageToken string) int {
	token := strings.TrimSpace(pageToken)
	if token == "" {
		return 0
	}
	offset, err := strconv.Atoi(token)
	if err != nil || offset < 0 {
		return 0
	}
	return offset
}

func timestampFromUnixMilli(unixMilli int64) *timestamppb.Timestamp {
	seconds := unixMilli / 1000
	nanos := int64(unixMilli%1000) * int64(time.Millisecond)
	return &timestamppb.Timestamp{
		Seconds: seconds,
		Nanos:   int32(nanos),
	}
}

func buildID(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UTC().UnixNano())
}

func scanUnitTask(scanner interface{ Scan(dest ...any) error }) (*v1.UnitTask, error) {
	var (
		unitTaskID     string
		workspaceID    string
		title          string
		status         int32
		actionRequired int32
		createdAtMS    int64
		updatedAtMS    int64
	)
	if err := scanner.Scan(&unitTaskID, &workspaceID, &title, &status, &actionRequired, &createdAtMS, &updatedAtMS); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scan unit task: %w", err)
	}

	return &v1.UnitTask{
		UnitTaskId:     unitTaskID,
		WorkspaceId:    workspaceID,
		Title:          title,
		Status:         v1.UnitTaskStatus(status),
		ActionRequired: v1.ActionType(actionRequired),
		CreatedAt:      timestampFromUnixMilli(createdAtMS),
		UpdatedAt:      timestampFromUnixMilli(updatedAtMS),
	}, nil
}

func scanSubTask(scanner interface{ Scan(dest ...any) error }) (*v1.SubTask, error) {
	var (
		subTaskID        string
		workspaceID      string
		unitTaskID       string
		subTaskType      int32
		subTaskStatus    int32
		completionReason int32
		prompt           string
		revisionNote     string
		createdAtMS      int64
		updatedAtMS      int64
	)
	if err := scanner.Scan(&subTaskID, &workspaceID, &unitTaskID, &subTaskType, &subTaskStatus, &completionReason, &prompt, &revisionNote, &createdAtMS, &updatedAtMS); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scan sub task: %w", err)
	}

	return &v1.SubTask{
		SubTaskId:        subTaskID,
		WorkspaceId:      workspaceID,
		UnitTaskId:       unitTaskID,
		Type:             v1.SubTaskType(subTaskType),
		Status:           v1.SubTaskStatus(subTaskStatus),
		CompletionReason: v1.SubTaskCompletionReason(completionReason),
		Prompt:           prompt,
		RevisionNote:     revisionNote,
		CreatedAt:        timestampFromUnixMilli(createdAtMS),
		UpdatedAt:        timestampFromUnixMilli(updatedAtMS),
	}, nil
}

func cloneSubTask(subTask *v1.SubTask) *v1.SubTask {
	payload, _ := json.Marshal(subTask)
	cloned := &v1.SubTask{}
	_ = json.Unmarshal(payload, cloned)
	return cloned
}

var prTrackingIDPattern = regexp.MustCompile(`([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+#[0-9]+)`)

func extractPRTrackingID(value string) string {
	matches := prTrackingIDPattern.FindStringSubmatch(strings.TrimSpace(value))
	if len(matches) < 2 {
		return ""
	}
	return matches[1]
}

var sqliteSchemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS unit_tasks (
		unit_task_id TEXT PRIMARY KEY,
		workspace_id TEXT NOT NULL,
		title TEXT NOT NULL,
		status INTEGER NOT NULL,
		action_required INTEGER NOT NULL,
		created_at_ms INTEGER NOT NULL,
		updated_at_ms INTEGER NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_unit_tasks_workspace ON unit_tasks(workspace_id, created_at_ms DESC)`,
	`CREATE TABLE IF NOT EXISTS sub_tasks (
		sub_task_id TEXT PRIMARY KEY,
		workspace_id TEXT NOT NULL,
		unit_task_id TEXT NOT NULL,
		type INTEGER NOT NULL,
		status INTEGER NOT NULL,
		completion_reason INTEGER NOT NULL,
		prompt TEXT NOT NULL,
		revision_note TEXT NOT NULL,
		created_at_ms INTEGER NOT NULL,
		updated_at_ms INTEGER NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_sub_tasks_workspace_unit ON sub_tasks(workspace_id, unit_task_id, created_at_ms DESC)`,
	`CREATE TABLE IF NOT EXISTS workspace_events (
		workspace_id TEXT NOT NULL,
		sequence INTEGER NOT NULL,
		event_type INTEGER NOT NULL,
		occurred_at_ms INTEGER NOT NULL,
		payload_json TEXT NOT NULL,
		PRIMARY KEY(workspace_id, sequence)
	)`,
	`CREATE TABLE IF NOT EXISTS session_outputs (
		workspace_id TEXT NOT NULL,
		session_id TEXT NOT NULL,
		output_offset INTEGER NOT NULL,
		kind INTEGER NOT NULL,
		body TEXT NOT NULL,
		occurred_at_ms INTEGER NOT NULL,
		PRIMARY KEY(workspace_id, session_id, output_offset)
	)`,
}

var postgresSchemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS unit_tasks (
		unit_task_id TEXT PRIMARY KEY,
		workspace_id TEXT NOT NULL,
		title TEXT NOT NULL,
		status INTEGER NOT NULL,
		action_required INTEGER NOT NULL,
		created_at_ms BIGINT NOT NULL,
		updated_at_ms BIGINT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_unit_tasks_workspace ON unit_tasks(workspace_id, created_at_ms DESC)`,
	`CREATE TABLE IF NOT EXISTS sub_tasks (
		sub_task_id TEXT PRIMARY KEY,
		workspace_id TEXT NOT NULL,
		unit_task_id TEXT NOT NULL,
		type INTEGER NOT NULL,
		status INTEGER NOT NULL,
		completion_reason INTEGER NOT NULL,
		prompt TEXT NOT NULL,
		revision_note TEXT NOT NULL,
		created_at_ms BIGINT NOT NULL,
		updated_at_ms BIGINT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_sub_tasks_workspace_unit ON sub_tasks(workspace_id, unit_task_id, created_at_ms DESC)`,
	`CREATE TABLE IF NOT EXISTS workspace_events (
		workspace_id TEXT NOT NULL,
		sequence BIGINT NOT NULL,
		event_type INTEGER NOT NULL,
		occurred_at_ms BIGINT NOT NULL,
		payload_json JSONB NOT NULL,
		PRIMARY KEY(workspace_id, sequence)
	)`,
	`CREATE TABLE IF NOT EXISTS session_outputs (
		workspace_id TEXT NOT NULL,
		session_id TEXT NOT NULL,
		output_offset BIGINT NOT NULL,
		kind INTEGER NOT NULL,
		body TEXT NOT NULL,
		occurred_at_ms BIGINT NOT NULL,
		PRIMARY KEY(workspace_id, session_id, output_offset)
	)`,
}
