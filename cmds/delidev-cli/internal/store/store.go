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
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	_ "modernc.org/sqlite"
)

const SchemaVersion = 31
const applicationID = 0x444c4456
const MaxPage = 200

// Compaction input is bounded at 3 MiB in the domain model but contains both
// the immutable source and fresh restore assignments. Keep a small envelope
// headroom here until those assignments can be stored by reference; this
// exception applies only to the typed compaction job and remains finite.
const maxCompactionJobEntityBytes = domain.MaxCompactionJobBytes

type Store struct {
	db                  *sql.DB
	root                string
	lock                *security.Lock
	gate                sync.RWMutex
	backupGate          sync.Mutex
	backupCreationGate  sync.Mutex
	notifyMu            sync.Mutex
	notify              chan struct{}
	closed              bool
	restoreFrozen       bool
	restoreReservations map[domain.ID]bool
	deletionFault       bool
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
	// Only migration 16 reads pre-service immutable pricing while rebuilding its
	// original budget table. Normal transactions require the current layout.
	historicalPricingV1 bool
	tx                  *sql.Tx
	ctx                 context.Context
	requestID           domain.ID
	now                 time.Time
	touched             map[domain.ID]bool
	queueTouched        map[domain.ID]bool
	readOnly            bool
}

func Open(ctx context.Context, root string) (_ *Store, returned error) {
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
	for _, name := range []string{"backups", "secrets", "backup-deletions", "backup-removals", "session-deletions"} {
		if err := security.PrivateDir(filepath.Join(root, name)); err != nil {
			return nil, storageError(err)
		}
	}
	// Reconcile replacement before SQLite can read a WAL or migrate either
	// image. server.lock remains owned for the entire recovery/publication.
	if err := recoverBackupRestore(ctx, root); err != nil {
		return nil, err
	}
	reservations, err := loadRestoreReservations(root)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(root, "state.sqlite")
	created := false
	var db *sql.DB
	var freshArtifacts []string
	defer func() {
		if success {
			return
		}
		if db != nil {
			if err := db.Close(); err != nil {
				returned = storageError(errors.Join(returned, err))
				return // Never remove files while SQLite closure is uncertain.
			}
		}
		if created {
			// Only this first-start attempt owns these files. Existing databases
			// and orphaned sidecars must remain untouched for recovery.
			for _, artifact := range append(freshArtifacts, path) {
				if err := os.Remove(artifact); err != nil && !errors.Is(err, os.ErrNotExist) {
					returned = storageError(errors.Join(returned, err))
				}
			}
			if err := security.SyncParent(path); err != nil {
				returned = storageError(errors.Join(returned, err))
			}
		}
	}()
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
	if created {
		for _, suffix := range []string{"-wal", "-shm", "-journal"} {
			if _, err := os.Lstat(path + suffix); !errors.Is(err, os.ErrNotExist) {
				return nil, corrupt()
			}
		}
		freshArtifacts = []string{path + "-wal", path + "-shm", path + "-journal"}
	}
	db, err = sql.Open("sqlite", databaseURI(path, false))
	if err != nil {
		return nil, storageError(err)
	}
	db.SetMaxOpenConns(1)
	fail := func(err error) (*Store, error) { return nil, storageError(err) }
	if err := inspect(ctx, db, created); err != nil {
		return fail(err)
	}
	if err := verifyRestoreHistory(ctx, db, root, reservations); err != nil {
		return fail(err)
	}
	if err := cleanupSettledRestoreImages(ctx, root, reservations); err != nil {
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
		if _, err = tx.ExecContext(ctx, schema); err == nil {
			err = applyMigrations(ctx, tx, 1)
		}
		if err == nil {
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
	// Persisted lease timestamps cannot prove availability in this server
	// process. Preserve instance ownership for recovery, but require a fresh
	// observation before any schedule can use its availability interval.
	if _, err := db.ExecContext(ctx, "UPDATE worker_instances SET available_since=0"); err != nil {
		return fail(err)
	}
	s := &Store{db: db, root: root, lock: lock, notify: make(chan struct{}), restoreReservations: reservations}
	success = true
	return s, nil
}

func databaseURI(path string, readonly bool) string {
	path = filepath.ToSlash(path)
	// SQLite file URIs require / before an absolute Windows drive. Without
	// it net/url emits file://C:/..., treating the drive as an authority and
	// SQLite rejects the open before any schema query can run.
	if len(path) >= 3 && path[1] == ':' && path[2] == '/' && ((path[0] >= 'A' && path[0] <= 'Z') || (path[0] >= 'a' && path[0] <= 'z')) {
		path = "/" + path
	}
	u := url.URL{Scheme: "file", Path: path}
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
	// Unmerged accounting and diagnostics branches reused schema 25. A version
	// number alone must never adopt their layout or historical records. Preserve
	// those files for explicit recovery instead of guessing a migration.
	if version >= 25 {
		var layout string
		expectedLayout := "grok-closed-input-v1"
		if version >= 26 {
			expectedLayout = "priced-native-input-v2"
		}
		if err := db.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key='native_accounting_layout'").Scan(&layout); err != nil || layout != expectedLayout {
			return domain.Fail(domain.RecoveryRequired, "The native accounting layout is unrecognized.", "Preserve the original database and use explicit recovery; never adopt an unmerged schema by version number.")
		}
	}
	if version >= 28 {
		var layout string
		if err := db.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key='subscription_notification_layout'").Scan(&layout); err != nil || layout != "account-recovery-v1" {
			return domain.Fail(domain.RecoveryRequired, "The subscription notification layout is unrecognized.", "Preserve the original database and use a matching composed server version.")
		}
		if err := db.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key='subscription_identity_layout'").Scan(&layout); err != nil || layout != "service-accounts-v2" {
			return domain.Fail(domain.RecoveryRequired, "The subscription identity layout is unrecognized.", "Preserve the original database and use a matching server version.")
		}
	}
	if version >= 29 {
		var layout string
		if err := db.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key='account_oauth_layout'").Scan(&layout); err != nil || layout != "pkce-once-v1" {
			return corrupt()
		}
	}
	if version >= 30 {
		var layout string
		if err := db.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key='provider_presets_layout'").Scan(&layout); err != nil || layout != "hosted-additions-26-v1" {
			return corrupt()
		}
	}
	if version >= 31 {
		var layout string
		if err := db.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key='account_oauth_credentials_layout'").Scan(&layout); err != nil || layout != "token-generations-v1" {
			return corrupt()
		}
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
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	return validateProjectPromptHistory(ctx, db)
}
func corrupt() error {
	return domain.Fail(domain.RecoveryRequired, "Database integrity validation failed.", "Keep the original database and WAL files, stop writes, and restore a validated backup.")
}

func (s *Store) Close() error {
	s.backupGate.Lock()
	defer s.backupGate.Unlock()
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
	limit := 1 << 20
	if operation == "worker.branches.report" {
		limit = domain.MaxRepositoryBranchesJobBytes
	}
	if err != nil || len(body) > limit {
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
		if err := s.checkRestoreRequestReservation(id); err != nil {
			return err
		}
		if err := tx.Authorize(); err != nil {
			return err
		}
		var savedHash string
		var saved []byte
		if err := tx.checkSubscriptionDeletionRequest(id, digest); err != nil {
			return err
		}
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
		if string(saved) == quarantinedReceipt {
			return restoreQuarantined()
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
	if s.deletionFault {
		return Result{}, domain.SessionDeletionPending()
	}

	if err := s.checkRestoreRequestReservation(id); err != nil {
		return Result{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Result{}, storageError(err)
	}
	defer tx.Rollback()
	permission := &Tx{tx: tx, ctx: ctx}
	if err := permission.Authorize(); err != nil {
		return Result{}, err
	}
	if err := permission.checkSubscriptionDeletionRequest(id, digest); err != nil {
		return Result{}, err
	}
	var savedHash string
	var saved []byte
	err = tx.QueryRowContext(ctx, "SELECT digest,result FROM receipts WHERE id=?", id).Scan(&savedHash, &saved)
	if err == nil {
		if digest != savedHash {
			return Result{}, domain.Fail(domain.Conflict, "The request ID was already used for different input.", "Retry the original command unchanged or use a new request ID.")
		}
		if string(saved) == quarantinedReceipt {
			return Result{}, restoreQuarantined()
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
	if err = t.validatePRRemediationClaims(); err != nil {
		return Result{}, err
	}
	raw, err := json.Marshal(out)
	resultLimit := 4 << 20
	if operation == "worker.branches.report" {
		resultLimit = 2 * domain.MaxRepositoryBranchesJobBytes
	}
	if err != nil || len(raw) > resultLimit {
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

func (t *Tx) Get(kind domain.Kind, id domain.ID) (Record, error) { return get(t.ctx, t.tx, kind, id) }
func (s *Store) Get(ctx context.Context, kind domain.Kind, id domain.ID) (Record, error) {
	s.gate.RLock()
	defer s.gate.RUnlock()
	if s.deletionFault {
		return Record{}, domain.SessionDeletionPending()
	}
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
	if kind == domain.ProjectPromptHistoryKind && (expected != 0 || sessionID != "" || projectID == "") {
		return Record{}, domain.Fail(domain.InvalidArgument, "Prompt history is immutable project-owned text.", "Use the dedicated history operations.")
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
	// An accepted external deletion obligation permanently closes admission.
	// Original in-flight observations may settle only existing records; they
	// cannot create new copies or restore dispatch while cleanup is pending.
	scope := sessionID
	if kind == domain.SessionKind {
		scope = id
	}
	if scope != "" {
		deleting, e := t.SessionDeleting(scope)
		if e != nil {
			return Record{}, e
		}
		if deleting {
			if expected == 0 {
				return Record{}, domain.SessionDeletionPending()
			}
			if kind == domain.SessionKind {
				s, ok := value.(domain.Session)
				if !ok || s.Dispatch != domain.DispatchPaused || s.NextExecutionIntent != "" {
					return Record{}, domain.SessionDeletionPending()
				}
			}
		}
	}
	if kind == domain.SessionKind {
		var err error
		value, err = t.terminalArchiveBarrier(id, value)
		if err != nil {
			return Record{}, err
		}
		// Every Archive completion also shares the socket-cleanup gate, including
		// late preparation, title and agent reports. Native process completion alone
		// cannot release a session's separately owned forward lifetimes.
		raw, err := json.Marshal(value)
		if err != nil {
			return Record{}, err
		}
		var fields map[string]json.RawMessage
		var archive domain.ArchiveState
		if json.Unmarshal(raw, &fields) == nil && json.Unmarshal(fields["archive"], &archive) == nil && archive == domain.Archived {
			pending, err := t.SessionForwardsPending(id)
			if err != nil {
				return Record{}, err
			}
			// A manual action owns native cleanup separately from the preceding
			// conversation. No other late completion may archive over its claim.
			var compaction domain.ID
			if rawClaim, present := fields["compaction_job_id"]; present && (json.Unmarshal(rawClaim, &compaction) != nil || compaction != "") {
				pending = true
			}
			if pending {
				fields["archive"], _ = json.Marshal(domain.ArchivePending)
				raw, err := json.Marshal(fields)
				if err != nil {
					return Record{}, err
				}
				value = json.RawMessage(raw)
			}
		}
	}
	body, err := json.Marshal(value)
	maxBodyBytes := 1 << 20
	if job, ok := value.(domain.Job); ok && kind == domain.JobKind && job.Type == domain.DiscoverRepositoryBranchesJob {
		maxBodyBytes = domain.MaxRepositoryBranchesJobBytes
	}
	if job, ok := value.(domain.Job); ok && kind == domain.JobKind && job.Type == domain.CompactSessionJob {
		maxBodyBytes = maxCompactionJobEntityBytes
	} else if job, ok := value.(domain.Job); ok && kind == domain.JobKind && job.Type != domain.DiscoverRepositoryBranchesJob {
		maxBodyBytes = workspace.StorageJobDocumentLimit(job)
	}
	if err != nil || len(body) > maxBodyBytes {
		return Record{}, domain.Fail(domain.InvalidArgument, "Invalid entity document.", "Use a validated bounded entity document.")
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
	if err = t.indexMessage(r); err != nil {
		return Record{}, err
	}
	if err = t.event(r, action); err != nil {
		return Record{}, err
	}
	t.touched[id] = true
	if kind == domain.QueueKind {
		if t.queueTouched == nil {
			t.queueTouched = map[domain.ID]bool{}
		}
		t.queueTouched[id] = true
	}
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
	if err := t.deleteOperationalSourceInbox(kind, id); err != nil {
		return err
	}
	if kind == domain.ProjectPromptHistoryKind {
		// Live history removal is independent of receipt/native retirement. Older
		// managed backups may restore captured text; project tombstones still fence
		// deleted projects. Do not redact source-session creation receipts here.
		if _, err := t.tx.ExecContext(t.ctx, "DELETE FROM entities WHERE id=?", id); err != nil {
			return storageError(err)
		}
		delete(t.touched, id)
		return t.event(r, Deleted)
	}
	if kind == domain.ProjectKind {
		if _, err := t.ClearProjectPromptHistory(id); err != nil {
			return err
		}
	}
	if kind == domain.AccountKind {
		if err := t.RequireBrowserProfileRemoval(id); err != nil {
			return err
		}
		if err := t.deleteAccountRecoveryInbox(id); err != nil {
			return err
		}
	}
	if kind == domain.SessionKind {
		if err := t.requireTerminalCleanup(id); err != nil {
			return err
		}
		if err := t.StopForwards(id, ""); err != nil {
			return err
		}
		if err := t.deleteSessionPRActivity(id); err != nil {
			return err
		}
	}
	if kind == domain.TerminalKind {
		terminal, err := Decode[domain.Terminal](r)
		if err != nil {
			return err
		}
		if terminal.Live() {
			return domain.Fail(domain.RecoveryRequired, "The terminal still owns native resources.", "Close and join the original terminal before deleting its record.")
		}
	}
	if kind == domain.ModelKind {
		model, err := Decode[domain.Model](r)
		if err != nil {
			return err
		}
		if model.ProviderID != "" {
			if _, err = t.tx.ExecContext(t.ctx, "INSERT OR IGNORE INTO model_suppressions(provider_id,native_id) VALUES(?,?)", model.ProviderID, model.NativeID); err != nil {
				return storageError(err)
			}
		}
	}
	if _, err = t.tx.ExecContext(t.ctx, "INSERT INTO tombstones(id,kind,created_at) VALUES(?,?,?)", id, kind, t.now.UnixMilli()); err != nil {
		return storageError(err)
	}
	if err := t.preserveDeletedProjectPolicy(r); err != nil {
		return err
	}
	if kind == domain.JobKind {
		if err := t.deleteWorkerNativeRoute(id); err != nil {
			return err
		}
	}
	if _, err = t.tx.ExecContext(t.ctx, "DELETE FROM entities WHERE id=?", id); err != nil {
		return storageError(err)
	}
	// Historical receipt content must not resurrect a deleted entity. Preserve the
	// request identity/digest, returning a minimal non-content deletion result.
	redacted, _ := json.Marshal(map[string]any{"deleted": true, "id": id})
	if _, err = t.tx.ExecContext(t.ctx, "UPDATE receipts SET result="+deletedReceiptProjection+" WHERE id IN (SELECT request_id FROM receipt_entities WHERE entity_id=?)", redacted, id); err != nil {
		return storageError(err)
	}
	t.touched[id] = true
	return t.event(r, Deleted)
}

type Filter struct {
	APIProtocol         domain.APIProtocol         `json:"api_protocol,omitempty"`
	SubscriptionService domain.SubscriptionService `json:"subscription_service,omitempty"`
	Kind                domain.Kind                `json:"kind"`
	SessionID           domain.ID                  `json:"session_id,omitempty"`
	ProjectID           domain.ID                  `json:"project_id,omitempty"`
	ProviderID          domain.ID                  `json:"provider_id,omitempty"`
	AccountType         domain.AccountType         `json:"account_type,omitempty"`
	After               domain.ID                  `json:"after,omitempty"`
	Limit               int                        `json:"limit"`
}

func (f Filter) validate() error {
	if !f.Kind.Valid() {
		return domain.Fail(domain.InvalidArgument, "Unknown entity kind.", "Select a supported entity kind.")
	}
	if f.Limit < 1 || f.Limit > MaxPage {
		return domain.Fail(domain.InvalidArgument, "Invalid page size.", "Use a page size between 1 and 200.")
	}
	for _, id := range []domain.ID{f.SessionID, f.ProjectID, f.ProviderID, f.After} {
		if id != "" {
			if err := id.Validate(); err != nil {
				return err
			}
		}
	}
	if f.ProviderID != "" || f.AccountType != "" || f.SubscriptionService != "" || f.APIProtocol != "" {
		if f.Kind != domain.AccountKind {
			return domain.Fail(domain.InvalidArgument, "Account filters require account resources.", "Select account as the resource kind.")
		}
	}
	if f.AccountType != "" && f.AccountType != domain.APIAccount && f.AccountType != domain.SubscriptionAccount {
		return domain.Fail(domain.InvalidArgument, "Unknown account type filter.", "Select api or subscription.")
	}
	if f.APIProtocol != "" && (!f.APIProtocol.API() || f.AccountType == domain.SubscriptionAccount || f.SubscriptionService != "") {
		return domain.Fail(domain.InvalidArgument, "Invalid API format filter.", "Select one API format without a subscription source.")
	}
	if f.SubscriptionService != "" && (!f.SubscriptionService.Valid() || f.ProviderID != "" || f.AccountType == domain.APIAccount) {
		return domain.Fail(domain.InvalidArgument, "Invalid subscription account filter.", "Select one subscription service without an API provider.")
	}
	return nil
}
func list(ctx context.Context, q queryer, f Filter) ([]Record, error) {
	if err := f.validate(); err != nil {
		return nil, err
	}
	return listRows(ctx, q, f, f.Limit)
}

func listRows(ctx context.Context, q queryer, f Filter, limit int) ([]Record, error) {
	query := "SELECT id,kind,revision,session_id,project_id,body,created_at,updated_at FROM entities WHERE kind=? AND id>?"
	args := []any{f.Kind, f.After}
	if f.ProviderID != "" {
		query += " AND json_extract(CAST(body AS TEXT),'$.provider_id')=?"
		args = append(args, f.ProviderID)
	}
	if f.AccountType != "" {
		query += " AND json_extract(CAST(body AS TEXT),'$.type')=?"
		args = append(args, f.AccountType)
	}
	if f.SubscriptionService != "" {
		query += " AND json_extract(body,'$.type')='subscription' AND json_extract(body,'$.subscription_service')=?"
		args = append(args, f.SubscriptionService)
	}
	if f.APIProtocol != "" {
		query += " AND json_extract(CAST(body AS TEXT),'$.type')='api' AND COALESCE(json_extract(CAST(body AS TEXT),'$.api_protocol'),(SELECT json_extract(CAST(p.body AS TEXT),'$.protocol') FROM entities p WHERE p.kind='provider' AND p.id=json_extract(CAST(entities.body AS TEXT),'$.provider_id')))=?"
		args = append(args, f.APIProtocol)
	}
	if f.SessionID != "" {
		query += " AND session_id=?"
		args = append(args, f.SessionID)
	}
	if f.ProjectID != "" {
		query += " AND project_id=?"
		args = append(args, f.ProjectID)
	}
	query += " ORDER BY id LIMIT ?"
	args = append(args, limit)
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
	if s.deletionFault {
		return nil, domain.SessionDeletionPending()
	}
	return list(ctx, s.db, f)
}

// ListPage returns one bounded page and whether an additional record exists.
// Fetching the extra row avoids a phantom empty page when the total is exactly
// divisible by the requested page size.
func (s *Store) ListPage(ctx context.Context, f Filter) ([]Record, bool, error) {
	if err := f.validate(); err != nil {
		return nil, false, err
	}
	s.gate.RLock()
	defer s.gate.RUnlock()
	if s.deletionFault {
		return nil, false, domain.SessionDeletionPending()
	}
	rows, err := listRows(ctx, s.db, f, f.Limit+1)
	if err != nil {
		return nil, false, err
	}
	more := len(rows) > f.Limit
	if more {
		rows = rows[:f.Limit]
	}
	return rows, more, nil
}

// Snapshot establishes the cursor and state in the same read transaction. It is
// intentionally bounded and scoped; messages are retrieved by identity/revision.
func (s *Store) Snapshot(ctx context.Context, f Filter) ([]Record, uint64, error) {
	s.gate.RLock()
	defer s.gate.RUnlock()
	if s.deletionFault {
		return nil, 0, domain.SessionDeletionPending()
	}
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
	if s.deletionFault {
		return nil, domain.SessionDeletionPending()
	}
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
	maxBytes := 1 << 20
	if r.Kind == domain.JobKind {
		var envelope struct {
			Type domain.JobType `json:"type"`
		}
		if len(r.Data) <= domain.MaxRepositoryBranchesJobBytes && json.Unmarshal(r.Data, &envelope) == nil {
			if envelope.Type == domain.DiscoverRepositoryBranchesJob {
				maxBytes = domain.MaxRepositoryBranchesJobBytes
			}
			if envelope.Type == domain.CompactSessionJob {
				maxBytes = maxCompactionJobEntityBytes
			} else if envelope.Type == domain.WorkspaceStorageJob {
				var job domain.Job
				if workspace.DecodeStorageJob(r.Data, &job) == nil {
					maxBytes = workspace.StorageJobDocumentLimit(job)
				}
			}
		}
	}
	err := domain.DecodeWithLimit(r.Data, &result, maxBytes)
	return result, err
}
func (s *Store) Backup(ctx context.Context) (domain.ID, error) {
	return s.BackupID(ctx, domain.NewID())
}

func (s *Store) BackupID(ctx context.Context, id domain.ID) (domain.ID, error) {
	if err := lockBackupContext(ctx, &s.backupGate); err != nil {
		return "", err
	}
	defer s.backupGate.Unlock()
	if err := s.backupNotDeleted(ctx, id); err != nil {
		return "", err
	}
	if err := id.Validate(); err != nil {
		return "", err
	}
	s.gate.Lock()
	defer s.gate.Unlock()
	// This lock also excludes revocation mutations. Check the original actor
	// after acquiring it and retain it through VACUUM/publication, so revocation
	// that committed first cannot leave a new private backup on disk.
	if s.deletionFault {
		return "", domain.SessionDeletionPending()
	}
	if err := s.readLocked(ctx, func(tx *Tx) error { return tx.Authorize() }); err != nil {
		return "", err
	}
	var owner domain.ID
	if err := s.db.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key='server_id'").Scan(&owner); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", storageError(err)
	}
	path := filepath.Join(s.root, "backups", string(id)+".sqlite")
	if _, err := os.Lstat(path); err == nil {
		if err := s.verifyBackupPublication(ctx, path, id); err != nil {
			return "", err
		}
		if err := validateBackup(ctx, path, &owner); err != nil {
			return "", err
		}
		// An earlier attempt may have renamed the file but failed to sync its
		// directory. Exact retries must finish that durability boundary too.
		if err := security.SyncParent(path); err != nil {
			return "", storageError(err)
		}
		return id, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", storageError(err)
	}
	pending := path + ".pending"
	defer os.Remove(pending)
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
	if err := validateBackup(ctx, pending, &owner); err != nil {
		return "", err
	}
	// Commit the exact private image's provenance before its public filename
	// appears. A same-server image at that known name is not a retry receipt.
	if err := s.recordBackupPublication(ctx, pending, id); err != nil {
		return "", err
	}
	if err := claimBackupImage(pending, path); err != nil {
		return "", storageError(err)
	}
	if err := security.SyncParent(path); err != nil {
		return "", storageError(err)
	}
	return id, nil
}
func ValidateBackup(ctx context.Context, path string) error { return validateBackup(ctx, path, nil) }

func validateBackup(ctx context.Context, path string, owner *domain.ID) error {
	info, err := backupInfo(path)
	if err != nil {
		return err
	}
	// Every published image, including a pre-migration image, must remain
	// inspectable and deletable through the managed backup APIs.
	if info.Size() > MaxBackupInspectionBytes {
		return domain.Fail(domain.ResourceExhausted, "The backup exceeds the 8 GiB managed image limit.", "Reduce the live database size before retrying; the original database is unchanged.")
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Lstat(path + suffix); !errors.Is(err, os.ErrNotExist) {
			return backupUnavailable()
		}
	}
	db, err := sql.Open("sqlite", databaseURI(path, true)+"&immutable=1")
	if err != nil {
		return storageError(err)
	}
	defer db.Close()
	if err := inspect(ctx, db, false); err != nil {
		return err
	}
	if owner != nil {
		var observed domain.ID
		if err := db.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key='server_id'").Scan(&observed); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return storageError(err)
		}
		if observed != *owner {
			return backupUnavailable()
		}
	}
	return nil
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
	if s.deletionFault {
		return domain.SessionDeletionPending()
	}
	return s.readLocked(ctx, read)
}

// Caller holds either gate mode for the entire transaction.
func (s *Store) readLocked(ctx context.Context, read func(*Tx) error) error {
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
