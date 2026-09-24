package store

import (
	"database/sql"
	"errors"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const interactionSchema = `
CREATE TABLE execution_interactions (
 interaction_id TEXT PRIMARY KEY REFERENCES entities(id) ON DELETE CASCADE,
 session_id TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
 execution_id TEXT NOT NULL, native_thread_id TEXT NOT NULL, native_request_key TEXT NOT NULL,
 closure TEXT NOT NULL CHECK(closure IN ('open','native-closed','turn-ended')),
 question_bytes INTEGER NOT NULL CHECK(question_bytes > 0 AND question_bytes <= 524288),
 UNIQUE(execution_id,native_request_key)
);
CREATE INDEX execution_interaction_state ON execution_interactions(execution_id,closure);
PRAGMA user_version=9;
`

// Interaction identity is independent of a tool's transcript identity: a tool
// may own requests without creating a message, and closure is not completion.
func (t *Tx) BindExecutionInteraction(session, execution, interaction domain.ID, thread string, request domain.InteractionRequestID, questionBytes int) error {
	if err := t.writeAllowed(); err != nil {
		return err
	}
	for _, id := range []domain.ID{session, execution, interaction} {
		if err := id.Validate(); err != nil {
			return err
		}
	}
	key, err := request.Key()
	if err != nil {
		return err
	}
	if domain.Text(thread, "native thread identity", 1024, true) != nil || questionBytes <= 0 || questionBytes > 512<<10 {
		return domain.Fail(domain.InvalidArgument, "Invalid interaction binding.", "Bind a bounded original native request to its exact execution.")
	}
	var count, open, size int
	if err := t.tx.QueryRowContext(t.ctx, "SELECT COUNT(*),COALESCE(SUM(closure='open'),0),COALESCE(SUM(CASE WHEN closure='open' THEN question_bytes ELSE 0 END),0) FROM execution_interactions WHERE execution_id=?", execution).Scan(&count, &open, &size); err != nil {
		return storageError(err)
	}
	if count >= domain.MaxExecutionInteractions || open >= domain.MaxOpenInteractions || questionBytes > domain.MaxOpenQuestionBytes-size {
		return domain.Fail(domain.ResourceExhausted, "Execution interaction retention reached its bound.", "Retain native state and reconcile pending requests without dropping or truncating questions.")
	}
	_, err = t.tx.ExecContext(t.ctx, "INSERT INTO execution_interactions(interaction_id,session_id,execution_id,native_thread_id,native_request_key,closure,question_bytes) VALUES(?,?,?,?,?,'open',?)", interaction, session, execution, thread, key, questionBytes)
	return storageError(err)
}

func (t *Tx) CloseExecutionInteraction(session, execution, interaction domain.ID, thread string, request domain.InteractionRequestID, closure domain.InteractionClosure) error {
	if err := t.writeAllowed(); err != nil {
		return err
	}
	key, err := request.Key()
	if err != nil {
		return err
	}
	if closure != domain.InteractionNativeClosed && closure != domain.InteractionTurnEnded {
		return domain.Fail(domain.InvalidArgument, "Invalid interaction closure.", "Retain native closure or terminal-turn evidence.")
	}
	var old domain.InteractionClosure
	err = t.tx.QueryRowContext(t.ctx, "SELECT closure FROM execution_interactions WHERE interaction_id=? AND session_id=? AND execution_id=? AND native_thread_id=? AND native_request_key=?", interaction, session, execution, thread, key).Scan(&old)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && old != domain.InteractionOpen) {
		return domain.Fail(domain.Conflict, "The original interaction is no longer open.", "Reconcile the retained request instead of replacing its lifecycle.")
	}
	if err != nil {
		return storageError(err)
	}
	_, err = t.tx.ExecContext(t.ctx, "UPDATE execution_interactions SET closure=? WHERE interaction_id=?", closure, interaction)
	return storageError(err)
}

func (t *Tx) OpenExecutionInteractions(execution domain.ID) ([]domain.ID, error) {
	if err := execution.Validate(); err != nil {
		return nil, err
	}
	rows, err := t.tx.QueryContext(t.ctx, "SELECT interaction_id FROM execution_interactions WHERE execution_id=? AND closure='open' ORDER BY interaction_id LIMIT ?", execution, domain.MaxOpenInteractions+1)
	if err != nil {
		return nil, storageError(err)
	}
	defer rows.Close()
	ids := []domain.ID{}
	for rows.Next() {
		var id domain.ID
		if err := rows.Scan(&id); err != nil {
			return nil, storageError(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, storageError(err)
	}
	if len(ids) > domain.MaxOpenInteractions {
		return nil, domain.Fail(domain.RecoveryRequired, "Pending interaction indexes exceed their bound.", "Preserve the database and reconcile interaction ownership.")
	}
	return ids, nil
}
