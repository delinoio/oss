package store

import (
	"context"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const sessionSchema = `
CREATE INDEX session_visibility ON entities(project_id,json_extract(body,'$.archive'),id) WHERE kind='session';
CREATE UNIQUE INDEX queue_sequence ON entities(session_id,json_extract(body,'$.sequence')) WHERE kind='queue';
CREATE INDEX queue_pending ON entities(session_id,json_extract(body,'$.delivery'),json_extract(body,'$.sequence')) WHERE kind='queue';
PRAGMA user_version=4;
`

type SessionFilter struct {
	ProjectID       domain.ID `json:"project_id,omitempty"`
	IncludeArchived bool      `json:"include_archived"`
	After           domain.ID `json:"after,omitempty"`
	Limit           int       `json:"limit"`
}

func (s *Store) Sessions(ctx context.Context, f SessionFilter) ([]Record, bool, error) {
	if err := (Filter{Kind: domain.SessionKind, ProjectID: f.ProjectID, After: f.After, Limit: f.Limit}).validate(); err != nil {
		return nil, false, err
	}
	var result []Record
	more := false
	err := s.Read(ctx, func(tx *Tx) error {
		query := "SELECT " + recordColumns + " FROM entities WHERE kind='session' AND id>?"
		args := []any{f.After}
		if f.ProjectID != "" {
			query += " AND project_id=?"
			args = append(args, f.ProjectID)
		}
		if !f.IncludeArchived {
			query += " AND json_extract(body,'$.archive')<>'archived'"
		}
		query += " ORDER BY id LIMIT ?"
		args = append(args, f.Limit+1)
		var err error
		result, more, err = tx.sessionPage(f.Limit, query, args...)
		return err
	})
	return result, more, err
}

// Queue is ordered by transaction-assigned acceptance sequence, not UUID or
// client time. Limit aggregate bytes as well as records for the Connect envelope.
func (s *Store) Queue(ctx context.Context, session domain.ID, after uint64, limit int) ([]Record, bool, error) {
	if err := (Filter{Kind: domain.QueueKind, SessionID: session, Limit: limit}).validate(); err != nil {
		return nil, false, err
	}
	if err := session.Validate(); err != nil {
		return nil, false, err
	}
	if after >= 1<<63-1 {
		return nil, false, domain.Fail(domain.InvalidArgument, "Invalid queue position.", "Use a queue cursor returned by the server.")
	}
	var result []Record
	more := false
	err := s.Read(ctx, func(tx *Tx) error {
		if _, err := tx.Get(domain.SessionKind, session); err != nil {
			return err
		}
		var err error
		result, more, err = tx.sessionPage(limit, "SELECT "+recordColumns+" FROM entities WHERE kind='queue' AND session_id=? AND json_extract(body,'$.delivery')<>'removed' AND json_extract(body,'$.sequence')>? ORDER BY json_extract(body,'$.sequence') LIMIT ?", session, after, limit+1)
		return err
	})
	return result, more, err
}

func (tx *Tx) sessionPage(limit int, query string, args ...any) ([]Record, bool, error) {
	rows, err := tx.tx.QueryContext(tx.ctx, query, args...)
	if err != nil {
		return nil, false, storageError(err)
	}
	defer rows.Close()
	result := []Record{}
	bytes := 0
	for rows.Next() {
		record, err := scan(rows)
		if err != nil {
			return nil, false, storageError(err)
		}
		if len(result) == limit || (len(result) > 0 && bytes+len(record.Data) > 3<<20) {
			return result, true, nil
		}
		result = append(result, record)
		bytes += len(record.Data)
	}
	return result, false, storageError(rows.Err())
}

// First-dispatch scans exclude paused/restored and previously claimed sessions.
// The coordinator must recheck every predicate inside its claim transaction.
func (s *Store) InitialExecutionCandidates(ctx context.Context, after domain.ID, limit int) ([]Record, bool, error) {
	return s.executionCandidates(ctx, after, limit, false)
}

// ExecutionCandidates includes authorized continuation intents, never paused or
// unresolved predecessors. Every predicate is rechecked at the claim boundary.
func (s *Store) ExecutionCandidates(ctx context.Context, after domain.ID, limit int) ([]Record, bool, error) {
	return s.executionCandidates(ctx, after, limit, true)
}

func (s *Store) executionCandidates(ctx context.Context, after domain.ID, limit int, includeContinuations bool) ([]Record, bool, error) {
	if err := (Filter{Kind: domain.SessionKind, After: after, Limit: limit}).validate(); err != nil {
		return nil, false, err
	}
	eligibility := "(json_extract(body,'$.initial_execution') IS NULL AND json_extract(body,'$.outcome')='not-started')"
	if includeContinuations {
		eligibility += " OR (json_extract(body,'$.initial_execution') IS NOT NULL AND json_extract(body,'$.execution.cleanup_verified')=1 AND json_extract(body,'$.next_execution_intent') IN ('continue-automatically','explicit-resume'))"
	}
	var result []Record
	var more bool
	err := s.Read(ctx, func(tx *Tx) error {
		var err error
		result, more, err = tx.sessionPage(limit, "SELECT "+recordColumns+" FROM entities WHERE kind='session' AND id>? AND ("+eligibility+") AND COALESCE(json_extract(body,'$.active_execution_id'),'')='' AND json_extract(body,'$.archive')='active' AND json_extract(body,'$.recovery')='none' AND json_extract(body,'$.dispatch') IN ('blocked','ready') AND json_extract(body,'$.preparation.state')='ready' AND json_extract(body,'$.pending_inputs')>0 ORDER BY id LIMIT ?", after, limit+1)
		return err
	})
	return result, more, err
}

func (tx *Tx) OldestQueuedInput(session domain.ID) (Record, error) {
	if err := session.Validate(); err != nil {
		return Record{}, err
	}
	rows, _, err := tx.sessionPage(1, "SELECT "+recordColumns+" FROM entities WHERE kind='queue' AND session_id=? AND json_extract(body,'$.delivery')='queued' ORDER BY json_extract(body,'$.sequence') LIMIT 1", session)
	if err != nil {
		return Record{}, err
	}
	if len(rows) == 0 {
		return Record{}, domain.Fail(domain.MissingInput, "The session has no queued input.", "Enqueue an input before requesting execution.")
	}
	return rows[0], nil
}

// Completion recovery must not hide an uncertain Steer merely because its
// pending pointer was retired by Worker loss or native terminal publication.
func (tx *Tx) RequireSettledExecutionDeliveries(session, execution domain.ID, acceptedInputs int) error {
	if session.Validate() != nil || execution.Validate() != nil || acceptedInputs < 1 || acceptedInputs > domain.MaxAcceptedExecutionInputs {
		return domain.ExecutionRecoveryUncertain()
	}
	var unresolved bool
	// A claimed response can become uncertain before a delivery observation
	// increments the summary counter. Inspect original records independently.
	err := tx.tx.QueryRowContext(tx.ctx, `SELECT EXISTS(
 SELECT 1 FROM entities WHERE session_id=? AND json_extract(body,'$.execution_id')=? AND (
  (kind='queue' AND json_extract(body,'$.delivery') IN ('claimed','uncertain')) OR
  (kind='steer' AND json_extract(body,'$.state') IN ('queued','claimed','uncertain')) OR
  (kind='interaction' AND (
   json_extract(body,'$.closure')='open' OR
   (json_type(body,'$.response')='object' AND COALESCE(json_extract(body,'$.response.state'),'') NOT IN ('accepted','canceled')) OR
   (json_type(body,'$.approval_response')='object' AND COALESCE(json_extract(body,'$.approval_response.state'),'') NOT IN ('accepted','canceled'))
  ))
 ))`, session, execution).Scan(&unresolved)
	if err != nil {
		return storageError(err)
	}
	if unresolved {
		return domain.ExecutionRecoveryUncertain()
	}
	var accepted int
	err = tx.tx.QueryRowContext(tx.ctx, `SELECT COUNT(*) FROM entities WHERE kind='queue' AND session_id=? AND json_extract(body,'$.execution_id')=? AND json_extract(body,'$.delivery')='accepted'`, session, execution).Scan(&accepted)
	if err != nil {
		return storageError(err)
	}
	if accepted != acceptedInputs {
		return domain.ExecutionRecoveryUncertain()
	}
	return nil
}
