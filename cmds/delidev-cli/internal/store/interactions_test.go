package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestInteractionMigrationPreservesV8MessagesAndBackup(t *testing.T) {
	s, root := openTest(t)
	ctx := context.Background()
	session, message, execution := domain.NewID(), domain.NewID(), domain.NewID()
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.v8-message", nil, func(tx *Tx) (any, error) {
		if _, err := tx.Put(domain.SessionKind, session, 0, session, "", domain.Session{}); err != nil {
			return nil, err
		}
		value := domain.ExecutionMessage{ExecutionID: execution, NativeThreadID: "thread", NativeTurnID: "turn", NativeID: "message", Role: domain.AssistantMessage, State: domain.MessageComplete, Text: "Existing transcript"}
		if _, err := tx.Put(domain.MessageKind, message, 0, session, "", value); err != nil {
			return nil, err
		}
		return nil, tx.BindExecutionMessage(session, execution, message, "thread", "turn", "message", domain.MessageComplete)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(dropScheduleFixtureSchema + "DROP INDEX inbox_source; DROP INDEX inbox_read; DROP INDEX inbox_session_read; DROP TABLE execution_interactions; PRAGMA user_version=8"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r, err := s.Get(ctx, domain.MessageKind, message)
	if err != nil {
		t.Fatal(err)
	}
	m, err := Decode[domain.ExecutionMessage](r)
	if err != nil || m.Text != "Existing transcript" || r.Revision != 1 {
		t.Fatal("interaction migration rewrote message history")
	}
	var messages, interactions, version int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM execution_messages").Scan(&messages); err != nil || messages != 1 {
		t.Fatal("interaction migration lost message index")
	}
	if err := s.db.QueryRow("SELECT COUNT(*) FROM execution_interactions").Scan(&interactions); err != nil || interactions != 0 {
		t.Fatal("migration invented pending interactions")
	}
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != SchemaVersion {
		t.Fatal("interaction migration did not advance schema")
	}
	backups, err := filepath.Glob(filepath.Join(root, "backups", "*.sqlite"))
	if err != nil || len(backups) != 1 {
		t.Fatal("interaction migration did not preserve original backup")
	}
	backup, err := sql.Open("sqlite", databaseURI(backups[0], true))
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	if err := backup.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 8 {
		t.Fatal("pre-migration backup was changed")
	}
	if err := backup.QueryRow("SELECT COUNT(*) FROM execution_messages").Scan(&messages); err != nil || messages != 1 {
		t.Fatal("pre-migration message ownership was lost")
	}
}
