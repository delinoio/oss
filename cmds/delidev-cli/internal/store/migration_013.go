// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
)

func migration013(ctx context.Context, tx *sql.Tx, original int) error {
	if _, err := tx.ExecContext(ctx, searchSchema); err != nil {
		return storageError(err)
	}
	if err := (&Tx{tx: tx, ctx: ctx}).backfillSearch(); err != nil {
		return err
	}
	return nil
}

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
