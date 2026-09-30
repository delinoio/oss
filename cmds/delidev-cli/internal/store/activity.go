package store

import "github.com/delinoio/oss/cmds/delidev-cli/internal/domain"

// Activity is a metadata projection of already durable original sources. It
// cannot invent a start/completion timestamp from the session's current state.
// Execution jobs mean accepted dispatch, not native input acceptance. Terminal
// inbox sources retain observed native results independently of owned cleanup.
const activitySources = `(kind='job' AND json_extract(body,'$.type')='execute-session') OR
 (kind='inbox' AND json_extract(body,'$.source')='execution-terminal') OR kind='occurrence' OR
 (kind='problem' AND json_extract(body,'$.type') IN ('pull-request-activity','pull-request-handling-verification'))`

type ActivityFilter struct {
	SessionID domain.ID
	ProjectID domain.ID
	After     domain.ID
	Limit     int
	Epoch     uint64
}

func (f ActivityFilter) Validate() error {
	return (Filter{Kind: domain.JobKind, SessionID: f.SessionID, ProjectID: f.ProjectID, After: f.After, Limit: f.Limit}).validate()
}

// Pages are newest-first by actual source creation time, with UUID tie breaks.
// Epoch rejection preserves coherent membership under source updates/deletion.
func (t *Tx) ActivityPage(f ActivityFilter) ([]Record, bool, uint64, error) {
	if err := f.Validate(); err != nil {
		return nil, false, 0, err
	}
	var epoch uint64
	if err := t.tx.QueryRowContext(t.ctx, `SELECT COALESCE(MAX(sequence),0) FROM events WHERE kind IN ('job','inbox','occurrence','session','problem')`).Scan(&epoch); err != nil {
		return nil, false, 0, storageError(err)
	}
	if f.After != "" && f.Epoch != epoch {
		return nil, false, 0, domain.Fail(domain.CursorExpired, "Activity changed during pagination.", "Restart activity pagination to read current retained sources.")
	}
	query := "SELECT " + recordColumns + " FROM entities WHERE (" + activitySources + ")"
	args := []any{}
	// Sources whose owning session was deleted cannot expose even metadata;
	// unassigned waiting/skipped schedule occurrences remain independently owned.
	query += ` AND (session_id='' OR EXISTS(SELECT 1 FROM entities s WHERE s.id=entities.session_id AND s.kind='session'))`
	for _, part := range []struct {
		column string
		value  domain.ID
	}{{"session_id", f.SessionID}, {"project_id", f.ProjectID}} {
		if part.value != "" {
			query += " AND " + part.column + "=?"
			args = append(args, part.value)
		}
	}
	if f.After != "" {
		var exists bool
		if err := t.tx.QueryRowContext(t.ctx, "SELECT EXISTS(SELECT 1 FROM entities WHERE id=? AND ("+activitySources+"))", f.After).Scan(&exists); err != nil {
			return nil, false, 0, storageError(err)
		}
		if !exists {
			return nil, false, 0, domain.Fail(domain.CursorExpired, "The activity page source is no longer retained.", "Restart activity pagination.")
		}
		query += ` AND (created_at,id)<(SELECT created_at,id FROM entities WHERE id=?)`
		args = append(args, f.After)
	}
	query += " ORDER BY created_at DESC,id DESC LIMIT ?"
	args = append(args, f.Limit+1)
	rows, more, err := t.sessionPage(f.Limit, query, args...)
	return rows, more, epoch, err
}
