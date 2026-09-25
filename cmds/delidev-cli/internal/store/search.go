package store

import (
	"strings"
	"unicode/utf8"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Stable integer keys survive VACUUM; entities' implicit rowids do not. The
// private derived text and FTS index share source publication/deletion commits.
const searchSchema = `
CREATE TABLE transcript_search (
 id INTEGER PRIMARY KEY,
 message_id TEXT NOT NULL UNIQUE REFERENCES entities(id) ON DELETE CASCADE,
 session_id TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
 text TEXT NOT NULL
);
CREATE VIRTUAL TABLE transcript_fts USING fts5(text,content='transcript_search',content_rowid='id',tokenize='trigram case_sensitive 1');
INSERT INTO transcript_fts(transcript_fts,rank) VALUES('secure-delete',1);
CREATE TRIGGER transcript_search_insert AFTER INSERT ON transcript_search BEGIN
 INSERT INTO transcript_fts(rowid,text) VALUES(new.id,new.text);
END;
CREATE TRIGGER transcript_search_delete AFTER DELETE ON transcript_search BEGIN
 INSERT INTO transcript_fts(transcript_fts,rowid,text) VALUES('delete',old.id,old.text);
END;
CREATE TRIGGER transcript_search_update AFTER UPDATE ON transcript_search BEGIN
 INSERT INTO transcript_fts(transcript_fts,rowid,text) VALUES('delete',old.id,old.text);
 INSERT INTO transcript_fts(rowid,text) VALUES(new.id,new.text);
END;
CREATE INDEX transcript_search_session ON transcript_search(session_id);
CREATE INDEX search_execution_job ON entities(json_extract(body,'$.input.execution_id'),session_id)
 WHERE kind='job' AND json_extract(body,'$.type')='execute-session';
CREATE INDEX search_epoch ON events(kind,sequence);
PRAGMA user_version=13;
`

func (t *Tx) indexMessage(r Record) error {
	if r.Kind != domain.MessageKind {
		return nil
	}
	value, err := Decode[domain.ExecutionMessage](r)
	if err != nil {
		return err
	}
	// Existing generic legacy message documents need not have native execution
	// provenance. They remain readable but cannot invent account attribution.
	if r.SessionID == "" {
		return nil
	}
	_, err = t.tx.ExecContext(t.ctx, `INSERT INTO transcript_search(message_id,session_id,text) VALUES(?,?,?)
 ON CONFLICT(message_id) DO UPDATE SET session_id=excluded.session_id,text=excluded.text`, r.ID, r.SessionID, value.SearchText())
	return storageError(err)
}

func (t *Tx) backfillSearch() error {
	after := domain.ID("")
	for {
		rows, err := t.List(Filter{Kind: domain.MessageKind, After: after, Limit: MaxPage})
		if err != nil {
			return err
		}
		for _, r := range rows {
			if err := t.indexMessage(r); err != nil {
				return err
			}
		}
		if len(rows) < MaxPage {
			return nil
		}
		after = rows[len(rows)-1].ID
	}
}

type SearchFilter struct {
	domain.SearchSelection
	Query string
	After domain.ID
	Limit int
	Epoch uint64
}

func (f SearchFilter) Validate() error {
	if err := f.SearchSelection.Validate(); err != nil {
		return err
	}
	if err := (Filter{Kind: domain.MessageKind, After: f.After, Limit: f.Limit}).validate(); err != nil {
		return err
	}
	if err := domain.Text(f.Query, "search query", 1024, true); err != nil {
		return err
	}
	if strings.TrimSpace(f.Query) == "" || strings.ContainsRune(f.Query, '\x1f') {
		return domain.Fail(domain.InvalidArgument, "The search query is empty or contains a reserved separator.", "Supply literal conversation text of at most 1024 UTF-8 bytes.")
	}
	return nil
}

// SearchMessages returns canonical records, never snippets from stale index
// content. Callers read source/session references within this same transaction.
func (t *Tx) SearchMessages(f SearchFilter) ([]Record, bool, uint64, error) {
	if err := f.Validate(); err != nil {
		return nil, false, 0, err
	}
	var epoch uint64
	if err := t.tx.QueryRowContext(t.ctx, `SELECT COALESCE(MAX(sequence),0) FROM events WHERE kind IN ('message','session','job')`).Scan(&epoch); err != nil {
		return nil, false, 0, storageError(err)
	}
	if f.After != "" && f.Epoch != epoch {
		return nil, false, 0, domain.Fail(domain.CursorExpired, "Conversation search changed during pagination.", "Restart search to use current transcript and session membership.")
	}
	query := `SELECT m.id,m.kind,m.revision,m.session_id,m.project_id,m.body,m.created_at,m.updated_at
 FROM entities m JOIN transcript_search d ON d.message_id=m.id JOIN entities s ON s.id=m.session_id AND s.kind='session'
 WHERE m.kind='message' AND m.id>?`
	args := []any{f.After}
	literal := strings.ToLower(f.Query)
	if utf8.RuneCountInString(literal) >= 3 {
		query += ` AND d.id IN (SELECT rowid FROM transcript_fts WHERE transcript_fts MATCH ?)`
		args = append(args, `"`+strings.ReplaceAll(literal, `"`, `""`)+`"`)
	} else {
		// FTS5 trigrams cannot match one/two-rune queries. Keep exact short
		// Unicode lookup, bounded by the caller's cancellable read deadline.
		query += ` AND instr(d.text,?)>0`
		args = append(args, literal)
	}
	for _, part := range []struct {
		column string
		value  string
	}{
		{"m.session_id", string(f.SessionID)}, {"s.project_id", string(f.ProjectID)},
		{"json_extract(s.body,'$.agent_id')", string(f.AgentID)},
		{"json_extract(s.body,'$.outcome')", string(f.Outcome)},
		{"json_extract(s.body,'$.archive')", string(f.Archive)},
	} {
		if part.value != "" {
			query += " AND " + part.column + "=?"
			args = append(args, part.value)
		}
	}
	if f.AccountID != "" {
		query += ` AND EXISTS(SELECT 1 FROM entities j WHERE j.kind='job' AND json_extract(j.body,'$.type')='execute-session'
 AND json_extract(j.body,'$.input.execution_id')=json_extract(m.body,'$.execution_id') AND j.session_id=m.session_id
 AND json_extract(j.body,'$.input.account_id')=?)`
		args = append(args, f.AccountID)
	}
	query += " ORDER BY m.id LIMIT ?"
	args = append(args, f.Limit+1)
	rows, more, err := t.sessionPage(f.Limit, query, args...)
	return rows, more, epoch, err
}
