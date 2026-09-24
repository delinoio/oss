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
