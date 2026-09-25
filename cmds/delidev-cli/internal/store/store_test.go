package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func openTest(t *testing.T) (*Store, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "state")
	s, err := Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, root
}
func assertCode(t *testing.T, err error, code domain.Code) {
	t.Helper()
	var e *domain.Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("want %s, got %v", code, err)
	}
}
func create(t *testing.T, s *Store, id domain.ID, value string) Record {
	t.Helper()
	var record Record
	_, err := s.Mutate(context.Background(), domain.NewID(), "project.create", map[string]string{"name": value}, func(tx *Tx) (any, error) {
		r, e := tx.Put(domain.ProjectKind, id, 0, "", "", map[string]string{"name": value})
		record = r
		return r, e
	})
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func TestDurableDeduplicationConcurrentRetry(t *testing.T) {
	s, root := openTest(t)
	ctx := context.Background()
	request, id := domain.NewID(), domain.NewID()
	var applied atomic.Int32
	var wg sync.WaitGroup
	results := make(chan Result, 24)
	errs := make(chan error, 24)
	call := func() (Result, error) {
		return s.Mutate(ctx, request, "project.create", map[string]string{"name": "one"}, func(tx *Tx) (any, error) {
			applied.Add(1)
			return tx.Put(domain.ProjectKind, id, 0, "", "", map[string]string{"name": "one"})
		})
	}
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); r, e := call(); results <- r; errs <- e }()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if applied.Load() != 1 {
		t.Fatalf("applied %d times", applied.Load())
	}
	fresh := 0
	var accepted string
	for result := range results {
		if !result.Replayed {
			fresh++
		}
		if accepted == "" {
			accepted = string(result.Data)
		} else if accepted != string(result.Data) {
			t.Fatal("retry changed accepted result")
		}
	}
	if fresh != 1 {
		t.Fatalf("fresh results: %d", fresh)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	result, err := call()
	if err != nil || !result.Replayed || string(result.Data) != accepted {
		t.Fatalf("restart receipt lost: %+v %v", result, err)
	}
	_, err = s.Mutate(ctx, request, "project.create", map[string]string{"name": "two"}, func(*Tx) (any, error) { t.Fatal("conflicting request executed"); return nil, nil })
	assertCode(t, err, domain.Conflict)
	events, err := s.Events(ctx, 0, "", 200)
	if err != nil || len(events) != 1 {
		t.Fatalf("events: %+v %v", events, err)
	}
}

func TestStateEventReceiptRollback(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	id, request := domain.NewID(), domain.NewID()
	_, err := s.Mutate(ctx, request, "project.create", nil, func(tx *Tx) (any, error) {
		if _, err := tx.Put(domain.ProjectKind, id, 0, "", "", map[string]string{"name": "rollback"}); err != nil {
			return nil, err
		}
		return nil, domain.Fail(domain.Conflict, "injected conflict", "")
	})
	assertCode(t, err, domain.Conflict)
	_, err = s.Get(ctx, domain.ProjectKind, id)
	assertCode(t, err, domain.NotFound)
	events, err := s.Events(ctx, 0, "", 100)
	if err != nil || len(events) != 0 {
		t.Fatalf("rolled back events remain: %+v %v", events, err)
	}
	result, err := s.Mutate(ctx, request, "project.create", nil, func(tx *Tx) (any, error) {
		return tx.Put(domain.ProjectKind, id, 0, "", "", map[string]string{"name": "accepted"})
	})
	if err != nil || result.Replayed {
		t.Fatalf("failed receipt reserved ID: %+v %v", result, err)
	}
}

func TestRevisionKindAndDeletionNeverResurrect(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	id := domain.NewID()
	r := create(t, s, id, "secret conversation text")
	_, err := s.Get(ctx, domain.AccountKind, id)
	assertCode(t, err, domain.InvalidArgument)
	_, err = s.Mutate(ctx, domain.NewID(), "account.create", nil, func(tx *Tx) (any, error) { return tx.Put(domain.AccountKind, id, 0, "", "", struct{}{}) })
	assertCode(t, err, domain.Conflict)
	_, err = s.Mutate(ctx, domain.NewID(), "project.edit", nil, func(tx *Tx) (any, error) { return tx.Put(domain.ProjectKind, id, r.Revision+1, "", "", struct{}{}) })
	assertCode(t, err, domain.Conflict)
	_, err = s.Mutate(ctx, domain.NewID(), "project.delete", nil, func(tx *Tx) (any, error) { return nil, tx.Delete(domain.ProjectKind, id, r.Revision) })
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Mutate(ctx, domain.NewID(), "account.create", nil, func(tx *Tx) (any, error) { return tx.Put(domain.AccountKind, id, 0, "", "", struct{}{}) })
	assertCode(t, err, domain.Conflict)
	var receipts int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM receipts WHERE CAST(result AS TEXT) LIKE '%secret conversation text%'").Scan(&receipts); err != nil || receipts != 0 {
		t.Fatalf("deleted receipt content retained: %d %v", receipts, err)
	}
}

func TestCoherentSnapshotAndCursorValidation(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	id := domain.NewID()
	create(t, s, id, "first")
	records, cursor, err := s.Snapshot(ctx, Filter{Kind: domain.ProjectKind, Limit: 10})
	if err != nil || len(records) != 1 || cursor != 1 {
		t.Fatalf("snapshot: %+v %d %v", records, cursor, err)
	}
	create(t, s, domain.NewID(), "second")
	events, err := s.Events(ctx, cursor, "", 10)
	if err != nil || len(events) != 1 || events[0].EntityID == id {
		t.Fatalf("bad incremental replay: %+v %v", events, err)
	}
	_, err = s.Events(ctx, 999, "", 10)
	assertCode(t, err, domain.CursorExpired)
	if _, err := s.db.Exec("UPDATE metadata SET value='2' WHERE key='event_floor'"); err != nil {
		t.Fatal(err)
	}
	_, err = s.Events(ctx, cursor, "", 10)
	assertCode(t, err, domain.CursorExpired)
	_, _, err = s.Snapshot(ctx, Filter{Kind: domain.ProjectKind, Limit: 1})
	assertCode(t, err, domain.ResourceExhausted)
}

func TestBackupIncludesCommittedWAL(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	create(t, s, domain.NewID(), "before backup")
	if _, err := os.Stat(filepath.Join(s.root, "state.sqlite-wal")); err != nil {
		t.Fatal("expected real WAL", err)
	}
	id, err := s.Backup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.root, "backups", string(id)+".sqlite")
	db, err := sql.Open("sqlite", databaseURI(path, true))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM entities").Scan(&count); err != nil || count != 1 {
		t.Fatalf("backup lost WAL state: %d %v", count, err)
	}
	create(t, s, domain.NewID(), "after backup")
	if err := db.QueryRow("SELECT COUNT(*) FROM entities").Scan(&count); err != nil || count != 1 {
		t.Fatalf("backup not independent: %d %v", count, err)
	}
}

func TestExclusiveScopeAndCorruptOrNewerDBPreserved(t *testing.T) {
	s, root := openTest(t)
	ctx := context.Background()
	_, err := Open(ctx, root)
	assertCode(t, err, domain.Conflict)
	if _, err = s.db.Exec("PRAGMA user_version=999"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	before, err := os.ReadFile(filepath.Join(root, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = Open(ctx, root)
	assertCode(t, err, domain.RecoveryRequired)
	after, err := os.ReadFile(filepath.Join(root, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("newer schema modified")
	}
	if err = os.WriteFile(filepath.Join(root, "state.sqlite"), []byte("corrupt sqlite content"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = Open(ctx, root)
	assertCode(t, err, domain.RecoveryRequired)
	after, err = os.ReadFile(filepath.Join(root, "state.sqlite"))
	if err != nil || string(after) != "corrupt sqlite content" {
		t.Fatalf("corrupt DB reset: %q %v", after, err)
	}
}

func TestCanceledFirstInitializationDoesNotWedgeFreshScope(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if db, err := Open(ctx, root); err == nil || db != nil {
		t.Fatal("canceled initialization succeeded")
	}
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		if _, err := os.Lstat(filepath.Join(root, "state.sqlite"+suffix)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("failed first startup left a database artifact", suffix, err)
		}
	}
	db, err := Open(context.Background(), root)
	if err != nil {
		t.Fatal("fresh initialization could not be retried", err)
	}
	defer db.Close()
	create(t, db, domain.NewID(), "accepted after retry")
}

func TestFreshInitializationPreservesPreexistingOrphanedSQLiteFiles(t *testing.T) {
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		t.Run(suffix, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "state")
			if err := os.Mkdir(root, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "state.sqlite"+suffix)
			if err := os.WriteFile(path, []byte("prior SQLite recovery evidence"), 0600); err != nil {
				t.Fatal(err)
			}
			_, err := Open(context.Background(), root)
			assertCode(t, err, domain.RecoveryRequired)
			raw, err := os.ReadFile(path)
			if err != nil || string(raw) != "prior SQLite recovery evidence" {
				t.Fatal("preexisting recovery file changed", err)
			}
			if _, err := os.Lstat(filepath.Join(root, "state.sqlite")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("failed first startup left its empty file")
			}
		})
	}
}

func TestErrorsAndEventsExcludeRawSecretCause(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	_, err := s.Mutate(ctx, domain.NewID(), "project.create", nil, func(*Tx) (any, error) {
		return nil, errors.New("https://user:password@example.org sk-secret user@example.org")
	})
	raw, _ := json.Marshal(err)
	if strings.Contains(string(raw), "password") || strings.Contains(string(raw), "sk-secret") {
		t.Fatal("raw cause leaked", string(raw))
	}
	id := domain.NewID()
	create(t, s, id, "content must not enter event metadata")
	events, err := s.Events(ctx, 0, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(events)
	if strings.Contains(string(raw), "content must") {
		t.Fatal("content in events")
	}
}
