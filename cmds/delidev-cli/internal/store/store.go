// Package store is the server's exclusive SQLite authority. Transaction callbacks
// perform bounded database work only; Git, providers, and filesystem jobs run outside.
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	_ "modernc.org/sqlite"
)

const SchemaVersion = 11
const applicationID = 0x444c4456
const MaxPage = 200

type Store struct {
	db       *sql.DB
	root     string
	lock     *security.Lock
	gate     sync.RWMutex
	notifyMu sync.Mutex
	notify   chan struct{}
	closed   bool
}

type Record struct {
	ID        domain.ID       `json:"id"`
	Kind      domain.Kind     `json:"kind"`
	Revision  uint64          `json:"revision"`
	SessionID domain.ID       `json:"session_id,omitempty"`
	ProjectID domain.ID       `json:"project_id,omitempty"`
	Data      json.RawMessage `json:"data"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

type Action string

const (
	Created Action = "created"
	Updated Action = "updated"
	Deleted Action = "deleted"
)

type Event struct {
	Cursor    uint64      `json:"cursor"`
	ID        domain.ID   `json:"id"`
	EntityID  domain.ID   `json:"entity_id"`
	Kind      domain.Kind `json:"kind"`
	SessionID domain.ID   `json:"session_id,omitempty"`
	Revision  uint64      `json:"revision"`
	Action    Action      `json:"action"`
	Time      time.Time   `json:"time"`
}

type Result struct {
	RequestID domain.ID       `json:"request_id"`
	Data      json.RawMessage `json:"data"`
	Replayed  bool            `json:"replayed"`
}

type Tx struct {
	tx        *sql.Tx
	ctx       context.Context
	requestID domain.ID
	now       time.Time
	touched   map[domain.ID]bool
	readOnly  bool
}

func Open(ctx context.Context, root string) (*Store, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, storageError(err)
	}
	if err := security.PrivateDir(root); err != nil {
		return nil, storageError(err)
	}
	lock, err := security.TryLock(filepath.Join(root, "server.lock"))
	if err != nil {
		return nil, storageError(err)
	}
	success := false
	defer func() {
		if !success {
			lock.Close()
		}
	}()
	for _, name := range []string{"backups", "secrets"} {
		if err := security.PrivateDir(filepath.Join(root, name)); err != nil {
			return nil, storageError(err)
		}
	}
	path := filepath.Join(root, "state.sqlite")
	created := false
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err == nil {
		created = true
		err = f.Close()
	} else if errors.Is(err, os.ErrExist) {
		err = security.RegularPrivate(path)
	}
	if err != nil {
		return nil, storageError(err)
	}
	db, err := sql.Open("sqlite", databaseURI(path, false))
	if err != nil {
		return nil, storageError(err)
	}
	db.SetMaxOpenConns(1)
	fail := func(err error) (*Store, error) { db.Close(); return nil, storageError(err) }
	if err := inspect(ctx, db, created); err != nil {
		return fail(err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL; PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000; PRAGMA secure_delete=ON;"); err != nil {
		return fail(err)
	}
	if created {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fail(err)
		}
		if _, err = tx.ExecContext(ctx, schema+workerSchema+catalogSchema+sessionSchema+jobControlSchema+assignmentSchema+executionSchema+executionMessageSchema+interactionSchema+inboxSchema+scheduleSchema); err == nil {
			err = tx.Commit()
		} else {
			tx.Rollback()
		}
		if err != nil {
			return fail(err)
		}
	}
	if !created {
		if err := migrate(ctx, db, root); err != nil {
			return fail(err)
		}
	}
	s := &Store{db: db, root: root, lock: lock, notify: make(chan struct{})}
	success = true
	return s, nil
}

func databaseURI(path string, readonly bool) string {
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	q := url.Values{"mode": {"rw"}, "_pragma": {"busy_timeout(5000)", "foreign_keys(1)"}}
	if readonly {
		q.Set("mode", "ro")
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func inspect(ctx context.Context, db *sql.DB, newlyCreated bool) error {
	var version, appID int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return corrupt()
	}
	if err := db.QueryRowContext(ctx, "PRAGMA application_id").Scan(&appID); err != nil {
		return corrupt()
	}
	if newlyCreated && version == 0 && appID == 0 {
		return nil
	}
	if appID != applicationID {
		return domain.Fail(domain.RecoveryRequired, "This is not a recognized DeliDev database.", "Preserve the original and restore a validated DeliDev backup.")
	}
	if version < 1 || version > SchemaVersion {
		return domain.Fail(domain.RecoveryRequired, "The stored schema requires a compatible DeliDev version.", "Use the matching server version; never reset or downgrade the database.")
	}
	var check string
	if err := db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&check); err != nil || check != "ok" {
		return corrupt()
	}
	rows, err := db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return corrupt()
	}
	defer rows.Close()
	if rows.Next() {
		return corrupt()
	}
	return rows.Err()
}
func corrupt() error {
	return domain.Fail(domain.RecoveryRequired, "Database integrity validation failed.", "Keep the original database and WAL files, stop writes, and restore a validated backup.")
}

func (s *Store) Close() error {
	s.gate.Lock()
	defer s.gate.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.signal()
	return errors.Join(s.db.Close(), s.lock.Close())
}
func (s *Store) Root() string { return s.root }
func (s *Store) Changed() <-chan struct{} {
	s.notifyMu.Lock()
	defer s.notifyMu.Unlock()
	return s.notify
}
func (s *Store) signal() {
	s.notifyMu.Lock()
	defer s.notifyMu.Unlock()
	close(s.notify)
	s.notify = make(chan struct{})
}

func mutationDigest(id domain.ID, operation string, input any) (string, error) {
	if err := id.Validate(); err != nil {
		return "", err
	}
	if err := domain.Text(operation, "operation", 128, true); err != nil {
		return "", err
	}
	body, err := json.Marshal(input)
	if err != nil || len(body) > 1<<20 {
		return "", domain.Fail(domain.InvalidArgument, "Invalid mutation document.", "Provide at most 1 MiB of JSON.")
	}
	hash := sha256.Sum256(append([]byte(operation+"\x00"), body...))
	return hex.EncodeToString(hash[:]), nil
}

// Replay checks an already accepted mutation without reserving an absent ID or
// performing side effects. Native/file coordinators use it before staging work;
// Mutate still rechecks the receipt and authorization at the commit boundary.
func (s *Store) Replay(ctx context.Context, id domain.ID, operation string, input any) (Result, bool, error) {
	digest, err := mutationDigest(id, operation, input)
	if err != nil {
		return Result{}, false, err
	}
	var result Result
	found := false
	err = s.Read(ctx, func(tx *Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		var savedHash string
		var saved []byte
		err := tx.tx.QueryRowContext(ctx, "SELECT digest,result FROM receipts WHERE id=?", id).Scan(&savedHash, &saved)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return storageError(err)
		}
		if savedHash != digest {
			return domain.Fail(domain.Conflict, "The request ID was already used for different input.", "Retry the original command unchanged or use a new request ID.")
		}
		found = true
		result = Result{RequestID: id, Data: saved, Replayed: true}
		return nil
	})
	return result, found, err
}

// Mutate commits state, metadata-only events, and a durable receipt together.
// Request IDs are globally unique within the data scope and bound to the exact
// canonical command. A failed transaction does not reserve its request identity.
func (s *Store) Mutate(ctx context.Context, id domain.ID, operation string, input any, apply func(*Tx) (any, error)) (Result, error) {
	digest, err := mutationDigest(id, operation, input)
	if err != nil {
		return Result{}, err
	}
	s.gate.RLock()
	defer s.gate.RUnlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Result{}, storageError(err)
	}
	defer tx.Rollback()
	permission := &Tx{tx: tx, ctx: ctx}
	if err := permission.Authorize(); err != nil {
		return Result{}, err
	}
	var savedHash string
	var saved []byte
	err = tx.QueryRowContext(ctx, "SELECT digest,result FROM receipts WHERE id=?", id).Scan(&savedHash, &saved)
	if err == nil {
		if digest != savedHash {
			return Result{}, domain.Fail(domain.Conflict, "The request ID was already used for different input.", "Retry the original command unchanged or use a new request ID.")
		}
		return Result{RequestID: id, Data: saved, Replayed: true}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Result{}, storageError(err)
	}
	t := &Tx{tx: tx, ctx: ctx, requestID: id, now: time.Now().UTC().Truncate(time.Millisecond), touched: map[domain.ID]bool{}}
	out, err := apply(t)
	if err != nil {
		return Result{}, storageError(err)
	}
	raw, err := json.Marshal(out)
	if err != nil || len(raw) > 4<<20 {
		return Result{}, domain.Fail(domain.ResourceExhausted, "The mutation result exceeds its bound.", "Split the operation into smaller batches.")
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO receipts(id,digest,result,created_at) VALUES(?,?,?,?)", id, digest, raw, t.now.UnixMilli()); err != nil {
		return Result{}, storageError(err)
	}
	for entity := range t.touched {
		if _, err = tx.ExecContext(ctx, "INSERT INTO receipt_entities(request_id,entity_id) VALUES(?,?)", id, entity); err != nil {
			return Result{}, storageError(err)
		}
	}
	if err = tx.Commit(); err != nil {
		return Result{}, storageError(err)
	}
	s.signal()
	return Result{RequestID: id, Data: raw}, nil
}

func storageError(err error) error {
	if err == nil {
		return nil
	}
	var known *domain.Error
	if errors.As(err, &known) {
		return known
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return domain.SafeError(err)
	}
	cause := "storage_io"
	var sqlite interface{ Code() int }
	if errors.As(err, &sqlite) {
		switch sqlite.Code() & 255 {
		case 5, 6:
			return &domain.Error{Code: domain.Unavailable, Message: "The database is busy.", Guidance: "Retry with the same request ID.", Cause: "sqlite_busy"}
		case 11, 26:
			return corrupt()
		case 13:
			return &domain.Error{Code: domain.ResourceExhausted, Message: "Storage is full.", Guidance: "Free disk space without removing DeliDev state, then retry.", Cause: "sqlite_full"}
		case 19:
			return &domain.Error{Code: domain.Conflict, Message: "A database invariant rejected the change.", Guidance: "Reload current state and retry with its revision.", Cause: "sqlite_constraint"}
		default:
			cause = "sqlite_failure"
		}
	}
	if errors.Is(err, os.ErrPermission) {
		return &domain.Error{Code: domain.PermissionDenied, Message: "State storage is inaccessible.", Guidance: "Check owner-only permissions and disk availability.", Cause: "filesystem_permission"}
	}
	return &domain.Error{Code: domain.Internal, Message: "State storage failed.", Guidance: "Check disk health and the correlated diagnostic; preserve the data scope.", Cause: cause}
}

const schema = `
CREATE TABLE entities (
 id TEXT PRIMARY KEY, kind TEXT NOT NULL, revision INTEGER NOT NULL CHECK(revision>0),
 session_id TEXT NOT NULL DEFAULT '', project_id TEXT NOT NULL DEFAULT '', body BLOB NOT NULL,
 created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
);
CREATE INDEX entity_kind_page ON entities(kind,id);
CREATE INDEX entity_session_page ON entities(session_id,kind,id);
CREATE INDEX entity_project_page ON entities(project_id,kind,id);
CREATE TABLE events (
 sequence INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE,
 entity_id TEXT NOT NULL, kind TEXT NOT NULL, session_id TEXT NOT NULL DEFAULT '',
 revision INTEGER NOT NULL, action TEXT NOT NULL, created_at INTEGER NOT NULL
);
CREATE INDEX event_session_cursor ON events(session_id,sequence);
CREATE TABLE receipts(id TEXT PRIMARY KEY,digest TEXT NOT NULL,result BLOB NOT NULL,created_at INTEGER NOT NULL);
CREATE TABLE receipt_entities(request_id TEXT NOT NULL REFERENCES receipts(id) ON DELETE CASCADE,entity_id TEXT NOT NULL,PRIMARY KEY(request_id,entity_id));
CREATE INDEX receipt_entity ON receipt_entities(entity_id);
CREATE TABLE tombstones(id TEXT PRIMARY KEY,kind TEXT NOT NULL,created_at INTEGER NOT NULL);
CREATE TABLE metadata(key TEXT PRIMARY KEY,value TEXT NOT NULL);
INSERT INTO metadata(key,value) VALUES('event_floor','0');
PRAGMA application_id=1145848918;
PRAGMA user_version=1;
`

func (t *Tx) Get(kind domain.Kind, id domain.ID) (Record, error) { return get(t.ctx, t.tx, kind, id) }
func (s *Store) Get(ctx context.Context, kind domain.Kind, id domain.ID) (Record, error) {
	s.gate.RLock()
	defer s.gate.RUnlock()
	return get(ctx, s.db, kind, id)
}

type queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func get(ctx context.Context, q queryer, kind domain.Kind, id domain.ID) (Record, error) {
	if err := id.Validate(); err != nil {
		return Record{}, err
	}
	if !kind.Valid() {
		return Record{}, domain.Fail(domain.InvalidArgument, "Unknown entity kind.", "Use a supported entity kind.")
	}
	r, err := scan(q.QueryRowContext(ctx, "SELECT id,kind,revision,session_id,project_id,body,created_at,updated_at FROM entities WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, domain.Fail(domain.NotFound, "The entity does not exist.", "Refresh the entity list.")
	}
	if err != nil {
		return Record{}, storageError(err)
	}
	if r.Kind != kind {
		return Record{}, domain.Fail(domain.InvalidArgument, "The ID belongs to another entity kind.", "Select an ID from the matching resource list.")
	}
	return r, nil
}

type scanner interface{ Scan(...any) error }

func scan(row scanner) (Record, error) {
	var r Record
	var created, updated int64
	err := row.Scan(&r.ID, &r.Kind, &r.Revision, &r.SessionID, &r.ProjectID, &r.Data, &created, &updated)
	r.CreatedAt = time.UnixMilli(created).UTC()
	r.UpdatedAt = time.UnixMilli(updated).UTC()
	return r, err
}

func (t *Tx) Put(kind domain.Kind, id domain.ID, expected uint64, sessionID, projectID domain.ID, value any) (Record, error) {
	if t.readOnly {
		return Record{}, domain.Fail(domain.PermissionDenied, "Read transactions cannot mutate state.", "Use a product mutation.")
	}
	if err := id.Validate(); err != nil {
		return Record{}, err
	}
	if !kind.Valid() {
		return Record{}, domain.Fail(domain.InvalidArgument, "Unknown entity kind.", "Use a supported entity kind.")
	}
	if sessionID != "" {
		if err := sessionID.Validate(); err != nil {
			return Record{}, err
		}
	}
	if projectID != "" {
		if err := projectID.Validate(); err != nil {
			return Record{}, err
		}
	}
	if expected >= 1<<63-1 {
		return Record{}, domain.Fail(domain.InvalidArgument, "Invalid expected revision.", "Reload the current entity revision.")
	}
	body, err := json.Marshal(value)
	if err != nil || len(body) > 1<<20 {
		return Record{}, domain.Fail(domain.InvalidArgument, "Invalid entity document.", "Use a validated entity no larger than 1 MiB.")
	}
	var tombstone string
	err = t.tx.QueryRowContext(t.ctx, "SELECT id FROM tombstones WHERE id=?", id).Scan(&tombstone)
	if err == nil {
		return Record{}, domain.Fail(domain.Conflict, "This entity was permanently deleted.", "Create a new entity with a new ID.")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Record{}, storageError(err)
	}
	now := t.now.UnixMilli()
	created := now
	action := Created
	if expected == 0 {
		_, err = t.tx.ExecContext(t.ctx, "INSERT INTO entities(id,kind,revision,session_id,project_id,body,created_at,updated_at) VALUES(?,?,1,?,?,?,?,?)", id, kind, sessionID, projectID, body, now, now)
	} else {
		old, e := t.Get(kind, id)
		if e != nil {
			return Record{}, e
		}
		if old.Revision != expected {
			return Record{}, domain.Fail(domain.Conflict, "The entity revision changed.", "Read its latest revision before editing.")
		}
		created = old.CreatedAt.UnixMilli()
		action = Updated
		var result sql.Result
		result, err = t.tx.ExecContext(t.ctx, "UPDATE entities SET revision=revision+1,session_id=?,project_id=?,body=?,updated_at=? WHERE id=? AND kind=? AND revision=?", sessionID, projectID, body, now, id, kind, expected)
		if err == nil {
			n, e := result.RowsAffected()
			if e != nil {
				err = e
			} else if n != 1 {
				err = domain.Fail(domain.Conflict, "The entity revision changed.", "Reload current state.")
			}
		}
	}
	if err != nil {
		return Record{}, storageError(err)
	}
	r := Record{ID: id, Kind: kind, Revision: expected + 1, SessionID: sessionID, ProjectID: projectID, Data: body, CreatedAt: time.UnixMilli(created).UTC(), UpdatedAt: t.now}
	if err = t.event(r, action); err != nil {
		return Record{}, err
	}
	t.touched[id] = true
	if sessionID != "" {
		t.touched[sessionID] = true
	}
	return r, nil
}
func (t *Tx) event(r Record, action Action) error {
	_, err := t.tx.ExecContext(t.ctx, "INSERT INTO events(id,entity_id,kind,session_id,revision,action,created_at) VALUES(?,?,?,?,?,?,?)", domain.NewID(), r.ID, r.Kind, r.SessionID, r.Revision, action, t.now.UnixMilli())
	return storageError(err)
}
func (t *Tx) Delete(kind domain.Kind, id domain.ID, expected uint64) error {
	if t.readOnly {
		return domain.Fail(domain.PermissionDenied, "Read transactions cannot mutate state.", "Use a product mutation.")
	}
	r, err := t.Get(kind, id)
	if err != nil {
		return err
	}
	if expected != r.Revision {
		return domain.Fail(domain.Conflict, "The entity revision changed.", "Reload its current revision before deletion.")
	}
	if kind == domain.ModelKind {
		model, err := Decode[domain.Model](r)
		if err != nil {
			return err
		}
		if _, err = t.tx.ExecContext(t.ctx, "INSERT OR IGNORE INTO model_suppressions(provider_id,native_id) VALUES(?,?)", model.ProviderID, model.NativeID); err != nil {
			return storageError(err)
		}
	}
	if _, err = t.tx.ExecContext(t.ctx, "INSERT INTO tombstones(id,kind,created_at) VALUES(?,?,?)", id, kind, t.now.UnixMilli()); err != nil {
		return storageError(err)
	}
	if _, err = t.tx.ExecContext(t.ctx, "DELETE FROM entities WHERE id=?", id); err != nil {
		return storageError(err)
	}
	// Historical receipt content must not resurrect a deleted entity. Preserve the
	// request identity/digest, returning a minimal non-content deletion result.
	redacted, _ := json.Marshal(map[string]any{"deleted": true, "id": id})
	if _, err = t.tx.ExecContext(t.ctx, "UPDATE receipts SET result=? WHERE id IN (SELECT request_id FROM receipt_entities WHERE entity_id=?)", redacted, id); err != nil {
		return storageError(err)
	}
	t.touched[id] = true
	return t.event(r, Deleted)
}

type Filter struct {
	Kind      domain.Kind `json:"kind"`
	SessionID domain.ID   `json:"session_id,omitempty"`
	ProjectID domain.ID   `json:"project_id,omitempty"`
	After     domain.ID   `json:"after,omitempty"`
	Limit     int         `json:"limit"`
}

func (f Filter) validate() error {
	if !f.Kind.Valid() {
		return domain.Fail(domain.InvalidArgument, "Unknown entity kind.", "Select a supported entity kind.")
	}
	if f.Limit < 1 || f.Limit > MaxPage {
		return domain.Fail(domain.InvalidArgument, "Invalid page size.", "Use a page size between 1 and 200.")
	}
	for _, id := range []domain.ID{f.SessionID, f.ProjectID, f.After} {
		if id != "" {
			if err := id.Validate(); err != nil {
				return err
			}
		}
	}
	return nil
}
func list(ctx context.Context, q queryer, f Filter) ([]Record, error) {
	if err := f.validate(); err != nil {
		return nil, err
	}
	query := "SELECT id,kind,revision,session_id,project_id,body,created_at,updated_at FROM entities WHERE kind=? AND id>?"
	args := []any{f.Kind, f.After}
	if f.SessionID != "" {
		query += " AND session_id=?"
		args = append(args, f.SessionID)
	}
	if f.ProjectID != "" {
		query += " AND project_id=?"
		args = append(args, f.ProjectID)
	}
	query += " ORDER BY id LIMIT ?"
	args = append(args, f.Limit)
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, storageError(err)
	}
	defer rows.Close()
	out := make([]Record, 0)
	for rows.Next() {
		r, err := scan(rows)
		if err != nil {
			return nil, storageError(err)
		}
		out = append(out, r)
	}
	return out, storageError(rows.Err())
}
func (t *Tx) List(f Filter) ([]Record, error) { return list(t.ctx, t.tx, f) }
func (s *Store) List(ctx context.Context, f Filter) ([]Record, error) {
	s.gate.RLock()
	defer s.gate.RUnlock()
	return list(ctx, s.db, f)
}

// Snapshot establishes the cursor and state in the same read transaction. It is
// intentionally bounded and scoped; messages are retrieved by identity/revision.
func (s *Store) Snapshot(ctx context.Context, f Filter) ([]Record, uint64, error) {
	s.gate.RLock()
	defer s.gate.RUnlock()
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, 0, storageError(err)
	}
	defer tx.Rollback()
	var cursor uint64
	if err = tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(sequence),0) FROM events").Scan(&cursor); err != nil {
		return nil, 0, storageError(err)
	}
	records, err := list(ctx, tx, f)
	if err != nil {
		return nil, 0, err
	}
	if len(records) == f.Limit {
		check := f
		check.After, check.Limit = records[len(records)-1].ID, 1
		more, err := list(ctx, tx, check)
		if err != nil {
			return nil, 0, err
		}
		if len(more) == 0 {
			return records, cursor, nil
		}
		return nil, 0, domain.Fail(domain.ResourceExhausted, "The snapshot scope exceeds a single page.", "Use a narrower session/project scope; do not treat partial state as a coherent snapshot.")
	}
	return records, cursor, nil
}
func (s *Store) Events(ctx context.Context, after uint64, session domain.ID, limit int) ([]Event, error) {
	if limit < 1 || limit > MaxPage || after >= 1<<63 {
		return nil, domain.Fail(domain.InvalidArgument, "Invalid event bounds.", "Use the returned cursor and a page size between 1 and 200.")
	}
	if session != "" {
		if err := session.Validate(); err != nil {
			return nil, err
		}
	}
	s.gate.RLock()
	defer s.gate.RUnlock()
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, storageError(err)
	}
	defer tx.Rollback()
	var floor, max uint64
	if err = tx.QueryRowContext(ctx, "SELECT CAST(value AS INTEGER) FROM metadata WHERE key='event_floor'").Scan(&floor); err != nil {
		return nil, storageError(err)
	}
	if err = tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(sequence),0) FROM events").Scan(&max); err != nil {
		return nil, storageError(err)
	}
	if after < floor || after > max {
		return nil, domain.Fail(domain.CursorExpired, "The event cursor is outside retained history.", "Fetch a coherent snapshot and reconnect strictly after its cursor.")
	}
	query := "SELECT sequence,id,entity_id,kind,session_id,revision,action,created_at FROM events WHERE sequence>?"
	args := []any{after}
	if session != "" {
		query += " AND session_id=?"
		args = append(args, session)
	}
	query += " ORDER BY sequence LIMIT ?"
	args = append(args, limit)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, storageError(err)
	}
	defer rows.Close()
	out := make([]Event, 0)
	for rows.Next() {
		var e Event
		var at int64
		if err = rows.Scan(&e.Cursor, &e.ID, &e.EntityID, &e.Kind, &e.SessionID, &e.Revision, &e.Action, &at); err != nil {
			return nil, storageError(err)
		}
		e.Time = time.UnixMilli(at).UTC()
		out = append(out, e)
	}
	return out, storageError(rows.Err())
}

func Decode[T any](r Record) (T, error) {
	var result T
	err := domain.Decode(r.Data, &result)
	return result, err
}
func (s *Store) Backup(ctx context.Context) (domain.ID, error) {
	return s.BackupID(ctx, domain.NewID())
}

func (s *Store) BackupID(ctx context.Context, id domain.ID) (domain.ID, error) {
	if err := id.Validate(); err != nil {
		return "", err
	}
	s.gate.Lock()
	defer s.gate.Unlock()
	path := filepath.Join(s.root, "backups", string(id)+".sqlite")
	if _, err := os.Lstat(path); err == nil {
		if err := ValidateBackup(ctx, path); err != nil {
			return "", err
		}
		return id, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", storageError(err)
	}
	pending := path + ".pending"
	if err := os.Remove(pending); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", storageError(err)
	}
	private, err := os.OpenFile(pending, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", storageError(err)
	}
	if err := private.Close(); err != nil {
		return "", storageError(err)
	}
	// VACUUM INTO is SQLite's consistent online backup; copying state.sqlite
	// would omit committed WAL data. Parameter binding avoids SQL path injection.
	if _, err := s.db.ExecContext(ctx, "VACUUM INTO ?", pending); err != nil {
		os.Remove(pending)
		return "", storageError(err)
	}
	if err := os.Chmod(pending, 0600); err != nil {
		return "", storageError(err)
	}
	f, err := os.OpenFile(pending, os.O_RDWR, 0)
	if err != nil {
		return "", storageError(err)
	}
	err = f.Sync()
	closeErr := f.Close()
	if err != nil {
		return "", storageError(err)
	}
	if closeErr != nil {
		return "", storageError(closeErr)
	}
	if err := ValidateBackup(ctx, pending); err != nil {
		return "", err
	}
	if err := os.Rename(pending, path); err != nil {
		return "", storageError(err)
	}
	return id, nil
}
func ValidateBackup(ctx context.Context, path string) error {
	if err := security.RegularPrivate(path); err != nil {
		return storageError(err)
	}
	db, err := sql.Open("sqlite", databaseURI(path, true))
	if err != nil {
		return storageError(err)
	}
	defer db.Close()
	return inspect(ctx, db, false)
}

// The schema declaration is kept literal for review; this check prevents the
// SQLite application marker from drifting away from its reader.
func init() {
	if !strings.Contains(schema, fmt.Sprintf("PRAGMA application_id=%d;", applicationID)) {
		panic("DeliDev SQLite application ID mismatch")
	}
}

// Read provides an internally consistent server-side view without a receipt,
// event, or write. Callbacks must not retain the transaction or perform I/O.
func (s *Store) Read(ctx context.Context, read func(*Tx) error) error {
	s.gate.RLock()
	defer s.gate.RUnlock()
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return storageError(err)
	}
	defer tx.Rollback()
	return read(&Tx{tx: tx, ctx: ctx, now: time.Now().UTC(), touched: map[domain.ID]bool{}, readOnly: true})
}

func (s *Store) ScopeIdentity(ctx context.Context) (domain.ID, error) {
	s.gate.RLock()
	defer s.gate.RUnlock()
	var id domain.ID
	err := s.db.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key='server_id'").Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", storageError(err)
	}
	if err := id.Validate(); err != nil {
		return "", corrupt()
	}
	return id, nil
}
func (s *Store) BindIdentity(ctx context.Context, id domain.ID) error {
	if err := id.Validate(); err != nil {
		return err
	}
	s.gate.Lock()
	defer s.gate.Unlock()
	_, err := s.db.ExecContext(ctx, "INSERT INTO metadata(key,value) VALUES('server_id',?) ON CONFLICT(key) DO NOTHING", id)
	if err != nil {
		return storageError(err)
	}
	var current string
	if err := s.db.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key='server_id'").Scan(&current); err != nil {
		return storageError(err)
	}
	if current != string(id) {
		return domain.Fail(domain.RecoveryRequired, "The database and owner identity disagree.", "Restore the matching owner credential and database backup; do not replace either implicitly.")
	}
	return nil
}
