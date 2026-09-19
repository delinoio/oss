package core

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

type Store struct {
	DB   *sql.DB
	Root string
}

func ID() string { return uuid.Must(uuid.NewV7()).String() }
func ValidID(id string) bool {
	u, e := uuid.Parse(id)
	return e == nil && u.Version() == 7 && u.String() == id
}
func OpenStore(root string) (*Store, error) {
	if err := PrivateDir(root); err != nil {
		return nil, err
	}
	for _, d := range []string{"evidence", "workspaces", "locks", "backups"} {
		if err := PrivateDir(filepath.Join(root, d)); err != nil {
			return nil, err
		}
	}
	path := filepath.Join(root, "state.sqlite")
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, E("unsafe-state", "state database cannot be a symlink", 3)
	}
	db, err := sql.Open("sqlite", filepath.ToSlash(path)+"?_pragma=busy_timeout(15000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)&_txlock=immediate")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{DB: db, Root: root}
	fail := func(e error) (*Store, error) { _ = db.Close(); return nil, e }
	var version int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fail(err)
	}
	if version != 0 && version != 1 {
		return fail(E("unsupported-state-version", "state database requires a compatible ach version; restore a backup rather than converting it", 3))
	}
	_, err = db.Exec(`
CREATE TABLE IF NOT EXISTS repositories(id TEXT PRIMARY KEY, common_dir TEXT NOT NULL, name TEXT NOT NULL, local_identity TEXT NOT NULL, UNIQUE(common_dir,local_identity));
CREATE TABLE IF NOT EXISTS worktrees(id TEXT PRIMARY KEY, repository_id TEXT NOT NULL REFERENCES repositories(id), path TEXT NOT NULL, branch TEXT NOT NULL, UNIQUE(repository_id,path));
CREATE TABLE IF NOT EXISTS runs(seq INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT UNIQUE NOT NULL, repo TEXT NOT NULL REFERENCES repositories(id), worktree TEXT NOT NULL, commit_oid TEXT NOT NULL, fingerprint TEXT NOT NULL, branch TEXT NOT NULL, state TEXT NOT NULL, ack TEXT, automatic_key TEXT UNIQUE, record BLOB NOT NULL);
CREATE INDEX IF NOT EXISTS run_gate ON runs(repo,commit_oid,fingerprint,seq DESC);
CREATE TABLE IF NOT EXISTS checks(id TEXT PRIMARY KEY, run_id TEXT NOT NULL REFERENCES runs(id), name TEXT NOT NULL, group_key TEXT NOT NULL, policy TEXT NOT NULL, state TEXT NOT NULL, cancel TEXT NOT NULL DEFAULT '', record BLOB NOT NULL, UNIQUE(run_id,name));
CREATE INDEX IF NOT EXISTS check_group ON checks(group_key,state);
CREATE TABLE IF NOT EXISTS browsers(id TEXT PRIMARY KEY, name TEXT NOT NULL, token_hash TEXT UNIQUE NOT NULL, created TEXT NOT NULL, revoked INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS pairings(code_hash TEXT PRIMARY KEY, expires TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS components(id TEXT PRIMARY KEY, kind TEXT NOT NULL, pid INTEGER NOT NULL, birth TEXT NOT NULL, config_hash TEXT NOT NULL, stop TEXT NOT NULL DEFAULT '', created TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS installations(id TEXT PRIMARY KEY, record BLOB NOT NULL);
PRAGMA user_version=1;`)
	if err != nil {
		return fail(err)
	}
	if err = s.migrateRegistry(); err != nil {
		return fail(err)
	}
	if err = PrivateFile(path); err != nil {
		return fail(err)
	}
	return s, nil
}
func (s *Store) Close() error { return s.DB.Close() }
func (s *Store) Transaction(fn func(*sql.Tx) error) error {
	tx, err := s.DB.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) Register(common, path, branch string) (Repository, Worktree, error) {
	identity, err := repositoryIdentity(common, true)
	if err != nil {
		return Repository{}, Worktree{}, err
	}
	r := Repository{ID: ID(), CommonDir: common, Name: filepath.Base(path)}
	w := Worktree{ID: ID(), Path: path, Branch: branch, Available: true}
	err = s.Transaction(func(tx *sql.Tx) error {
		if _, err := tx.Exec("INSERT INTO repositories(id,common_dir,name,local_identity) VALUES(?,?,?,?) ON CONFLICT(common_dir,local_identity) DO NOTHING", r.ID, common, r.Name, identity); err != nil {
			return err
		}
		if err := tx.QueryRow("SELECT id,name FROM repositories WHERE common_dir=? AND local_identity=?", common, identity).Scan(&r.ID, &r.Name); err != nil {
			return err
		}
		w.RepositoryID = r.ID
		if _, err := tx.Exec("INSERT INTO worktrees(id,repository_id,path,branch) VALUES(?,?,?,?) ON CONFLICT(repository_id,path) DO UPDATE SET branch=excluded.branch", w.ID, r.ID, path, branch); err != nil {
			return err
		}
		return tx.QueryRow("SELECT id FROM worktrees WHERE path=? AND repository_id=?", path, r.ID).Scan(&w.ID)
	})
	return r, w, err
}
func (s *Store) Registered(common, path string) (Repository, Worktree, error) {
	identity, err := repositoryIdentity(common, false)
	if err != nil {
		return Repository{}, Worktree{}, err
	}
	r := Repository{CommonDir: common}
	w := Worktree{Path: path, Available: true}
	err = s.DB.QueryRow("SELECT r.id,r.name,w.id,w.branch FROM repositories r JOIN worktrees w ON w.repository_id=r.id WHERE r.common_dir=? AND r.local_identity=? AND w.path=?", common, identity, path).Scan(&r.ID, &r.Name, &w.ID, &w.Branch)
	w.RepositoryID = r.ID
	if errors.Is(err, sql.ErrNoRows) {
		return r, w, E("repository-unregistered", "run ach init in this worktree to explicitly trust its committed commands", 2)
	}
	return r, w, err
}
func (s *Store) Repositories() ([]Repository, error) {
	rows, err := s.DB.Query("SELECT r.id,r.common_dir,r.name,r.local_identity,w.id,w.path,w.branch FROM repositories r LEFT JOIN worktrees w ON w.repository_id=r.id ORDER BY r.name,w.path")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Repository{}
	index := map[string]int{}
	identities := map[string]string{}
	for rows.Next() {
		r := Repository{}
		w := Worktree{}
		var identity string
		if err = rows.Scan(&r.ID, &r.CommonDir, &r.Name, &identity, &w.ID, &w.Path, &w.Branch); err != nil {
			return nil, err
		}
		w.RepositoryID = r.ID
		identities[r.ID] = identity
		i, ok := index[r.ID]
		if !ok {
			i = len(out)
			index[r.ID] = i
			out = append(out, r)
		}
		out[i].Worktrees = append(out[i].Worktrees, w)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	for i := range out {
		for j := range out[i].Worktrees {
			w := &out[i].Worktrees[j]
			common, root, branch, discoverErr := Discover(context.Background(), w.Path)
			identity, identityErr := repositoryIdentity(common, false)
			w.Available = discoverErr == nil && identityErr == nil && identity == identities[out[i].ID] && common == out[i].CommonDir && root == w.Path
			if w.Available {
				w.Branch = branch
			}
		}
	}
	return out, nil
}
func insertRun(tx *sql.Tx, r *Run, auto string) error {
	var automatic any
	if auto != "" {
		automatic = auto
	}
	result, err := tx.Exec("INSERT INTO runs(id,repo,worktree,commit_oid,fingerprint,branch,state,automatic_key,record) VALUES(?,?,?,?,?,?,?,?,?)", r.ID, r.RepositoryID, r.WorktreeID, r.Commit, r.Fingerprint, r.Branch, r.State, automatic, Encode(r))
	if err != nil {
		return err
	}
	r.Sequence, _ = result.LastInsertId()
	for _, c := range r.Checks {
		if _, err = tx.Exec("INSERT INTO checks(id,run_id,name,group_key,policy,state,record) VALUES(?,?,?,?,?,?,?)", c.ID, r.ID, c.Name, c.Group, c.Policy, c.State, Encode(c)); err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) InsertRun(r *Run, auto string) (string, error) {
	existing := ""
	err := s.Transaction(func(tx *sql.Tx) error {
		if auto != "" {
			err := tx.QueryRow("SELECT id FROM runs WHERE automatic_key=?", auto).Scan(&existing)
			if err == nil {
				return nil
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		return insertRun(tx, r, auto)
	})
	if existing != "" {
		return existing, err
	}
	return r.ID, err
}
func (s *Store) Run(id string) (Run, error) {
	return loadRun(s.DB, id)
}

type runReader interface {
	QueryRow(string, ...any) *sql.Row
	Query(string, ...any) (*sql.Rows, error)
}

func loadRun(db runReader, id string) (Run, error) {
	r := Run{}
	if !ValidID(id) {
		return r, E("invalid-run-id", "expected canonical UUID v7", 2)
	}
	var b []byte
	var ack, source sql.NullString
	var branch string
	var state State
	var seq int64
	err := db.QueryRow("SELECT r.record,r.seq,r.state,r.ack,r.branch,w.path FROM runs r LEFT JOIN worktrees w ON w.id=r.worktree AND w.repository_id=r.repo WHERE r.id=?", id).Scan(&b, &seq, &state, &ack, &branch, &source)
	if errors.Is(err, sql.ErrNoRows) {
		return r, E("run-not-found", "run not found", 2)
	}
	if err != nil {
		return r, err
	}
	if err = json.Unmarshal(b, &r); err != nil {
		return r, Wrap("state-corrupt", err)
	}
	// JSON snapshots and responses normalize invalid UTF-8. Operational paths
	// and historical branch identity come from lossless SQLite columns instead.
	if source.Valid {
		r.Source = source.String
	}
	r.Branch = branch
	r.Sequence = seq
	r.State = state
	if ack.Valid {
		t, _ := time.Parse(time.RFC3339Nano, ack.String)
		r.AcknowledgedAt = &t
	}
	rows, err := db.Query("SELECT record,state FROM checks WHERE run_id=? ORDER BY name", id)
	if err != nil {
		return r, err
	}
	defer rows.Close()
	r.Checks = []Check{}
	failureBudget := runFailureBytes
	for rows.Next() {
		var c Check
		if err = rows.Scan(&b, &state); err != nil {
			return r, err
		}
		if err = json.Unmarshal(b, &c); err != nil {
			return r, err
		}
		c.State = state
		failureBudget -= boundCheckFailures(&c, failureBudget)
		r.Checks = append(r.Checks, c)
	}
	return r, rows.Err()
}
func saveRun(tx *sql.Tx, r Run) error {
	_, err := tx.Exec("UPDATE runs SET record=?,state=? WHERE id=?", Encode(r), r.State, r.ID)
	return err
}
func (s *Store) SaveRun(r Run) error {
	return s.Transaction(func(tx *sql.Tx) error { return saveRun(tx, r) })
}
func saveCheck(tx *sql.Tx, c Check) error {
	if len(c.Failures) > 0 {
		var used int
		if err := tx.QueryRow("SELECT COALESCE(SUM(MAX(length(CAST(json_extract(record,'$.failures') AS BLOB))-1,0)),0) FROM checks WHERE run_id=(SELECT run_id FROM checks WHERE id=?) AND id<>?", c.ID, c.ID).Scan(&used); err != nil {
			return err
		}
		boundCheckFailures(&c, max(0, runFailureBytes-used))
	}
	_, err := tx.Exec("UPDATE checks SET record=?,state=? WHERE id=?", Encode(c), c.State, c.ID)
	return err
}
func (s *Store) SaveCheck(c Check) error {
	return s.Transaction(func(tx *sql.Tx) error { return saveCheck(tx, c) })
}
func (s *Store) Cancel(id string, reason State) error {
	if reason != Cancelled && reason != Replaced {
		return E("invalid-cancellation", "invalid cancellation reason", 2)
	}
	r, err := s.Run(id)
	if err != nil {
		return err
	}
	if r.State.Terminal() {
		return nil
	}
	// Share the worker's ownership lock: a worker that has read Queued must not
	// overwrite an immediate cancellation with its subsequent Preparing write.
	owner, err := TryLock(filepath.Join(s.Root, "locks", id+".lock"))
	if err != nil {
		return err
	}
	if owner != nil {
		defer owner.Close()
		r, err = s.Run(id)
		if err != nil {
			return err
		}
	}
	return s.Transaction(func(tx *sql.Tx) error {
		if _, err := tx.Exec("UPDATE checks SET cancel=? WHERE run_id=? AND state IN ('queued','preparing','running','collecting')", reason, id); err != nil {
			return err
		}
		if owner == nil || r.State != Queued {
			return nil
		}
		for _, c := range r.Checks {
			if !c.State.Terminal() && (c.State != Queued || c.Process.PID != 0 || c.StartedAt != nil) {
				return nil
			}
		}
		now := time.Now().UTC()
		for _, c := range r.Checks {
			if c.State.Terminal() {
				continue
			}
			c.State = reason
			c.FinishedAt = &now
			if err := saveCheck(tx, c); err != nil {
				return err
			}
		}
		r.State = reason
		r.FinishedAt = &now
		return saveRun(tx, r)
	})
}
func (s *Store) Cancellation(check string) State {
	var state State
	_ = s.DB.QueryRow("SELECT cancel FROM checks WHERE id=?", check).Scan(&state)
	return state
}
func (s *Store) Ack(id string) error {
	if _, err := s.Run(id); err != nil {
		return err
	}
	return s.Transaction(func(tx *sql.Tx) error {
		var state State
		if err := tx.QueryRow("SELECT state FROM runs WHERE id=?", id).Scan(&state); err != nil {
			return err
		}
		if !state.Terminal() {
			return E("acknowledgement-premature", "wait for the execution to finish before acknowledging its result", 2)
		}
		_, err := tx.Exec("UPDATE runs SET ack=COALESCE(ack,?) WHERE id=?", time.Now().UTC().Format(time.RFC3339Nano), id)
		return err
	})
}
func (s *Store) List(repo string, inbox bool, cursor string, limit int) (Page, error) {
	return s.ListFiltered(repo, "", "", false, inbox, cursor, limit)
}
func (s *Store) ListFiltered(repo, worktree, branch string, detached, inbox bool, cursor string, limit int) (Page, error) {
	if len(branch) > 1024 || (worktree != "" && !ValidID(worktree)) || (detached && branch != "") {
		return Page{}, E("invalid-run-filter", "invalid branch or worktree", 2)
	}
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 200 {
		return Page{}, E("invalid-page-size", "page size must be 1..200", 2)
	}
	upper := int64(1<<63 - 1)
	scope := repo + "/" + strconv.FormatBool(inbox) + "/" + worktree + "/" + branch + "/" + strconv.FormatBool(detached)
	if cursor != "" {
		b, e := base64.RawURLEncoding.DecodeString(cursor)
		if e != nil {
			return Page{}, E("invalid-cursor", "invalid page cursor", 2)
		}
		parts := strings.SplitN(string(b), ":", 2)
		if len(parts) != 2 || parts[1] != scope {
			return Page{}, E("invalid-cursor", "cursor scope mismatch", 2)
		}
		upper, e = strconv.ParseInt(parts[0], 10, 64)
		if e != nil || upper < 1 {
			return Page{}, E("invalid-cursor", "invalid sequence", 2)
		}
	}
	q := "SELECT id,seq FROM runs WHERE seq<?"
	args := []any{upper}
	if repo != "" {
		q += " AND repo=?"
		args = append(args, repo)
	}
	if worktree != "" {
		q += " AND worktree=?"
		args = append(args, worktree)
	}
	if branch != "" || detached {
		q += " AND branch=?"
		args = append(args, branch)
	}
	if inbox {
		q += " AND (ack IS NULL OR state IN ('queued','preparing','running','collecting'))"
	}
	q += " ORDER BY seq DESC LIMIT ?"
	args = append(args, limit+1)
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return Page{}, err
	}
	ids := []string{}
	seqs := []int64{}
	for rows.Next() {
		var id string
		var seq int64
		if err = rows.Scan(&id, &seq); err != nil {
			rows.Close()
			return Page{}, err
		}
		ids = append(ids, id)
		seqs = append(seqs, seq)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return Page{}, err
	}
	p := Page{Runs: []Run{}}
	if len(ids) > limit {
		ids = ids[:limit]
		p.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%d:%s", seqs[limit-1], scope)))
	}
	for _, id := range ids {
		r, e := s.Run(id)
		if e != nil {
			return p, e
		}
		p.Runs = append(p.Runs, r)
	}
	return p, nil
}
func (s *Store) Pending() ([]string, error) {
	rows, err := s.DB.Query("SELECT id FROM runs WHERE state IN ('queued','preparing','running','collecting') ORDER BY seq")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
func (s *Store) EvidencePath(run, id string) (string, error) {
	if !ValidID(run) || !ValidID(id) {
		return "", E("invalid-evidence-id", "invalid evidence identity", 2)
	}
	return filepath.Join(s.Root, "evidence", run, id), nil
}
