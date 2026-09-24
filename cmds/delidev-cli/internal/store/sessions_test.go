package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestSessionMigrationBacksUpV3AndRejectsAmbiguousQueueOrder(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "duplicate"}[duplicate], func(t *testing.T) {
			s, root := openTest(t)
			ctx := context.Background()
			session := domain.NewID()
			_, err := s.Mutate(ctx, domain.NewID(), "fixture.session", nil, func(tx *Tx) (any, error) {
				return tx.Put(domain.SessionKind, session, 0, session, "", domain.Session{Name: "retained", Archive: domain.NotArchived})
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.db.Exec("DROP TABLE execution_messages; DROP TABLE execution_references; DROP TABLE execution_grants; DROP TABLE job_assignments; DROP TABLE job_cancellations; DROP INDEX session_visibility; DROP INDEX queue_sequence; DROP INDEX queue_pending; PRAGMA user_version=3;")
			if err != nil {
				t.Fatal(err)
			}
			if duplicate {
				for i := 0; i < 2; i++ {
					raw, _ := json.Marshal(domain.QueuedInput{Sequence: 1, Prompt: "legacy retained", Delivery: domain.InputQueued})
					if _, err := s.db.Exec("INSERT INTO entities(id,kind,revision,session_id,body,created_at,updated_at) VALUES(?,'queue',1,?,?,1,1)", domain.NewID(), session, raw); err != nil {
						t.Fatal(err)
					}
				}
			}
			s.Close()
			migrated, err := Open(ctx, root)
			if duplicate {
				if err == nil {
					migrated.Close()
					t.Fatal("ambiguous accepted order was silently rewritten")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				r, err := migrated.Get(ctx, domain.SessionKind, session)
				if err != nil || r.Revision != 1 {
					t.Fatal("migration rewrote session")
				}
				migrated.Close()
			}
			backups, err := filepath.Glob(filepath.Join(root, "backups", "*.sqlite"))
			if err != nil || len(backups) != 1 {
				t.Fatal("missing v3 backup")
			}
			for _, path := range []string{filepath.Join(root, "state.sqlite"), backups[0]} {
				db, err := sql.Open("sqlite", databaseURI(path, true))
				if err != nil {
					t.Fatal(err)
				}
				var version, receipts, indices int
				for query, out := range map[string]*int{"PRAGMA user_version": &version, "SELECT COUNT(*) FROM receipts": &receipts, "SELECT COUNT(*) FROM sqlite_master WHERE name='queue_sequence'": &indices} {
					if err := db.QueryRow(query).Scan(out); err != nil {
						t.Fatal(err)
					}
				}
				db.Close()
				want, wantIndex := 3, 0
				if path != backups[0] && !duplicate {
					want, wantIndex = SchemaVersion, 1
				}
				if version != want || receipts != 1 || indices != wantIndex {
					t.Fatalf("migration/backup mismatch: %d %d %d", version, receipts, indices)
				}
			}
		})
	}
}

func TestQueuePagesBoundBytesAndRetainAcceptanceOrder(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	session := domain.NewID()
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.queue", nil, func(tx *Tx) (any, error) {
		if _, err := tx.Put(domain.SessionKind, session, 0, session, "", domain.Session{Name: "large", Archive: domain.NotArchived}); err != nil {
			return nil, err
		}
		for i := 1; i <= 20; i++ {
			state := domain.InputQueued
			if i == 3 {
				state = domain.InputRemoved
			}
			if _, err := tx.Put(domain.QueueKind, domain.NewID(), 0, session, "", domain.QueuedInput{Sequence: uint64(i), Prompt: strings.Repeat("x", domain.MaxPromptBytes), Delivery: state}); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	var after uint64
	for {
		records, more, err := s.Queue(ctx, session, after, 200)
		if err != nil {
			t.Fatal(err)
		}
		bytes := 0
		for _, r := range records {
			bytes += len(r.Data)
			q, err := Decode[domain.QueuedInput](r)
			if err != nil {
				t.Fatal(err)
			}
			if q.Sequence <= after || q.Sequence == 3 {
				t.Fatal("queue order/removal incorrect")
			}
			after = q.Sequence
			count++
		}
		if bytes > 3<<20 {
			t.Fatal("page exceeds byte boundary")
		}
		if !more {
			break
		}
	}
	if count != 19 || after != 20 {
		t.Fatal("byte-limited pagination lost entries")
	}
}
