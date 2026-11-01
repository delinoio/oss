// Package lease coordinates complete cache resource sets across processes.
// Its durable intents survive owner death. The process supervisor must positively
// verify tree termination before Finish or Reconcile; PID death is not proof.
// Callers validate repository membership, cache ownership and protected-path
// exclusions before acquiring. This package is coordination, not a path sandbox.
package lease

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

var (
	ErrBusy           = errors.New("cache is in use; wait for its managed process tree or reconcile its durable intent")
	ErrNestedConflict = errors.New("nested cache request conflicts with another session; acquire the complete resource set before launching the outer command or run this command after it exits")
	ErrUnverified     = errors.New("process-tree ownership is unverified; retain the lease and reconcile with the native supervisor")
	// ErrRegistryBusy reports that another process held the registry database
	// lock for the whole contention budget. It says nothing about cache use: no
	// lease overlap was evaluated, so the request is safe to retry unchanged.
	ErrRegistryBusy = errors.New("lease registry is locked by another ccow process; no cache conflict was found, retry shortly")
	// ErrAlreadyReleased reports that a reconcile found its record already
	// removed after verification succeeded. It matches ErrUnverified for callers
	// that only test that class, but a concurrent recovery that proved the same
	// tree finished leaves nothing to release, so Session.Finish accepts it.
	ErrAlreadyReleased error = alreadyReleasedError{}
)

type alreadyReleasedError struct{}

func (alreadyReleasedError) Error() string {
	return "lease record was already released after the owner's completion was verified"
}
func (alreadyReleasedError) Is(target error) bool { return target == ErrUnverified }

// busyError is the internal form of ErrBusy: it also carries the committed
// records that overlap the request so a ConflictHandler can examine them.
type busyError struct{ conflicts []Record }

func (e *busyError) Error() string        { return ErrBusy.Error() }
func (e *busyError) Is(target error) bool { return target == ErrBusy }

// ConflictHandler is offered the committed records that block an Acquire. It
// runs outside registry transactions and must release only records whose owners
// it positively verified as finished (Registry.Reconcile). It returns true when
// the registry may have changed so the request should be evaluated again at
// once. It is never called for registry lock contention (ErrRegistryBusy).
type ConflictHandler func(ctx context.Context, conflicts []Record) bool

// conflictRecheckInterval spaces repeated handler calls while a request waits,
// so an owner that dies mid-wait is noticed without polling files continuously.
var conflictRecheckInterval = time.Second

const (
	// defaultRegistryContentionBudget bounds how long one registry transaction
	// retries SQLite lock contention before reporting ErrRegistryBusy.
	defaultRegistryContentionBudget = time.Second
	registryRetryInterval           = 25 * time.Millisecond
)

// Resources must contain every source and destination before preparation starts.
// This implementation takes exclusive protection for both kinds of resource.
type Resources struct{ Sources, Destinations []string }

// Record contains no command arguments or environment. Ownership is a native
// supervisor recovery identifier, allocated BEFORE spawning any child.
type Record struct {
	Version   int      `json:"version"`
	ID        string   `json:"id"`
	Ownership string   `json:"ownership"`
	Paths     []string `json:"paths"`
}

// Verify must positively establish that the owner can no longer spawn and that
// ALL owned descendants have exited. A PID lookup, session environment variable,
// missing flock, or elapsed timeout alone cannot establish this condition.
// It runs outside registry transactions. Unknown ownership must return an error.
type Verify func(context.Context, Record) error

type Registry struct {
	db  *sql.DB
	dir string
	// contentionBudget is the per-transaction retry budget for registry lock
	// contention. Tests shorten or lengthen it; production uses the default.
	contentionBudget time.Duration
}

// Open uses one stable, user-private directory shared by every worktree and
// preset. The caller must not select a per-cache registry. SQLite supplies native
// cross-process locks and atomic, fully synchronized rollback-journal commits.
func Open(ctx context.Context, dir string) (*Registry, error) {
	if err := privateDir(dir); err != nil {
		return nil, err
	}
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, err
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "leases.sqlite")
	if err := privateDatabase(path); err != nil {
		return nil, err
	}
	uriPath := filepath.ToSlash(path)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	uri := url.URL{Scheme: "file", Path: uriPath}
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, err
	}
	// Do not limit connections: a nonblocking clean must not wait for a local
	// transaction to return its connection before SQLite can report contention.
	r := &Registry{db: db, dir: dir, contentionBudget: defaultRegistryContentionBudget}
	err = r.transaction(ctx, func(c *sql.Conn) error {
		var version int
		if err := c.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
			return err
		}
		if version != 0 && version != 1 {
			return fmt.Errorf("unsupported lease registry version %d; use a compatible ccow", version)
		}
		if version == 0 {
			var count int
			if err := c.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table'").Scan(&count); err != nil {
				return err
			}
			if count != 0 {
				return errors.New("unversioned lease registry is not empty")
			}
			if _, err := c.ExecContext(ctx, "CREATE TABLE leases (id TEXT PRIMARY KEY, record TEXT NOT NULL); PRAGMA user_version=1"); err != nil {
				return err
			}
		}
		_, err := records(ctx, c)
		return err
	})
	if err != nil {
		db.Close()
		return nil, err
	}
	return r, nil
}

// OpenWait waits for registry initialization contention without acquiring cache
// resources. Open already retries brief contention; OpenWait keeps waiting after
// that budget until ctx is done. Use Open for bounded management operations.
func OpenWait(ctx context.Context, dir string) (*Registry, error) {
	for {
		r, err := Open(ctx, dir)
		if !errors.Is(err, ErrRegistryBusy) {
			return r, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// Close closes the database connection only. It never releases durable intents.
func (r *Registry) Close() error { return r.db.Close() }

func isSQLiteBusy(err error) bool {
	var code interface{ Code() int }
	return errors.As(err, &code) && (code.Code()&255 == 5 || code.Code()&255 == 6)
}

// transaction runs fn inside BEGIN IMMEDIATE. SQLite BUSY/LOCKED from the lock
// request, fn or COMMIT is registry contention, not cache use: the whole attempt
// is rolled back and repeated until the contention budget ends, then reported as
// ErrRegistryBusy. fn therefore may run more than once and MUST derive all of its
// per-attempt state inside the closure. Errors fn returns itself, including
// ErrBusy for a real resource overlap, are never retried here.
func (r *Registry) transaction(ctx context.Context, fn func(*sql.Conn) error) error {
	budget := r.contentionBudget
	if budget <= 0 {
		budget = defaultRegistryContentionBudget
	}
	start := time.Now()
	deadline := start.Add(budget)
	for attempt := 1; ; attempt++ {
		err := r.attempt(ctx, fn)
		if !errors.Is(err, ErrRegistryBusy) {
			return err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			slog.Debug("lease registry contention budget exhausted", "attempts", attempt, "budget", budget)
			return err
		}
		wait := min(registryRetryInterval, remaining)
		slog.Debug("lease registry locked; retrying", "attempt", attempt, "wait", wait, "elapsed", time.Since(start))
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (r *Registry) attempt(ctx context.Context, fn func(*sql.Conn) error) (result error) {
	defer func() {
		if isSQLiteBusy(result) {
			result = ErrRegistryBusy
		}
	}()
	c, err := r.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	// Zero busy timeout lets this package own the retry budget and honor context.
	if _, err = c.ExecContext(ctx, "PRAGMA busy_timeout=0; PRAGMA synchronous=FULL"); err != nil {
		return err
	}
	if _, err = c.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	defer c.ExecContext(context.Background(), "ROLLBACK")
	if err = fn(c); err != nil {
		return err
	}
	_, err = c.ExecContext(ctx, "COMMIT")
	return err
}

func records(ctx context.Context, c *sql.Conn) ([]Record, error) {
	var version int
	if err := c.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return nil, err
	}
	if version != 1 {
		return nil, fmt.Errorf("unsupported lease registry version %d", version)
	}
	rows, err := c.QueryContext(ctx, "SELECT id, record FROM leases")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		var v Record
		dec := json.NewDecoder(strings.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&v); err != nil {
			return nil, fmt.Errorf("malformed lease record: %w", err)
		}
		if err := dec.Decode(new(any)); err != io.EOF {
			return nil, errors.New("trailing data in lease record")
		}
		parsed, err := uuid.Parse(v.ID)
		if err != nil || parsed.Version() != 7 || v.ID != id || v.Version != 1 || v.Ownership == "" || len(v.Paths) == 0 {
			return nil, errors.New("unsupported or malformed lease record; do not remove protection")
		}
		for _, p := range v.Paths {
			if !filepath.IsAbs(p) || filepath.Clean(p) != p {
				return nil, errors.New("invalid lease resource path")
			}
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Session is an in-memory capability. It cannot be reconstructed from an ID or
// environment. Cross-process nesting must use authenticated supervisor IPC to
// invoke Acquire on the original Session; this package does not provide IPC.
type Session struct {
	mu       sync.Mutex
	registry *Registry
	record   Record
	finished bool
	conflict ConflictHandler
}

// SetConflictHandler installs the handler Acquire offers conflicting records of
// a new top-level request. Nested extensions never use it: they fail on any
// contention to avoid dynamic cycles.
func (s *Session) SetConflictHandler(h ConflictHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conflict = h
}

func (r *Registry) NewSession(ownership string) (*Session, error) {
	if ownership == "" || len(ownership) > 4096 {
		return nil, ErrUnverified
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	return &Session{registry: r, record: Record{Version: 1, ID: id.String(), Ownership: ownership}}, nil
}
func (s *Session) ID() string { s.mu.Lock(); defer s.mu.Unlock(); return s.record.ID }

// Grant reports whether the complete request was already protected. When Borrowed
// is true, the caller must skip preparation. Grants have no independent release
// once a command may use the resources: all accumulated resources remain held
// until the entire session is finished. The only exceptions are
// ReleasePreparedSources after copying and ReleaseUnstartedGrant when the request
// failed before any command started.
type Grant struct {
	Borrowed bool
	// NewPaths are newly protected resources, including sources. Preparation may
	// only target new destinations, never a destination borrowed from the parent.
	NewPaths []string
}

// Acquire commits intent before returning. Only then may preparation or spawn
// begin. wait=false is the nonblocking clean operation. New top-level sessions
// can wait; nested extensions fail on contention, preventing dynamic cycles.
func (s *Session) Acquire(ctx context.Context, resources Resources, wait bool) (Grant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished {
		return Grant{}, ErrUnverified
	}
	paths, err := normalize(resources)
	if err != nil {
		return Grant{}, err
	}
	for _, p := range paths {
		if overlap(p, s.registry.dir) {
			return Grant{}, errors.New("lease registry must remain outside every cache resource")
		}
	}
	var nextConflictCheck time.Time
	for {
		if err := ctx.Err(); err != nil {
			return Grant{}, err
		}
		var borrowed bool
		var added []string
		var next Record
		err = s.registry.transaction(ctx, func(c *sql.Conn) error {
			// The transaction may run this closure again after registry
			// contention, so every attempt starts from the committed session state.
			borrowed = len(s.record.Paths) > 0
			added = nil
			next = s.record
			next.Paths = append([]string(nil), s.record.Paths...)
			all, err := records(ctx, c)
			if err != nil {
				return err
			}
			found := false
			var conflicts []Record
			for _, v := range all {
				if v.ID == s.record.ID {
					found = true
					if !equal(v, s.record) {
						return ErrUnverified
					}
					continue
				}
				if overlapsAny(paths, v.Paths) {
					conflicts = append(conflicts, v)
				}
			}
			if len(conflicts) > 0 {
				return &busyError{conflicts: conflicts}
			}
			if len(s.record.Paths) > 0 && !found {
				return ErrUnverified
			}
			for _, p := range paths {
				covered := false
				for _, q := range s.record.Paths {
					if contains(q, p) {
						covered = true
						break
					}
				}
				if !covered {
					for _, q := range s.record.Paths {
						if overlap(p, q) {
							return ErrNestedConflict
						}
					}
					added = append(added, p)
					borrowed = false
					next.Paths = append(next.Paths, p)
				}
			}
			sort.Strings(next.Paths)
			raw, err := json.Marshal(next)
			if err != nil {
				return err
			}
			_, err = c.ExecContext(ctx, "INSERT INTO leases(id,record) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET record=excluded.record", next.ID, string(raw))
			return err
		})
		if err == nil {
			s.record = next
			slog.Debug("cache lease acquired", "session", s.record.ID, "borrowed", borrowed)
			return Grant{Borrowed: borrowed, NewPaths: added}, nil
		}
		switch {
		case errors.Is(err, ErrRegistryBusy):
			// Registry lock contention is independent of cache overlap, so it is
			// never reported as ErrBusy or ErrNestedConflict.
			if !wait {
				return Grant{}, err
			}
			slog.Debug("waiting for lease registry", "session", s.record.ID)
		case errors.Is(err, ErrBusy):
			if len(s.record.Paths) > 0 {
				return Grant{}, ErrNestedConflict
			}
			// Offer the conflicting records for recovery: always once, and while
			// waiting again every conflictRecheckInterval. Recovery needs positive
			// owner-completion evidence, so a live owner is never displaced.
			var busy *busyError
			if s.conflict != nil && errors.As(err, &busy) && !time.Now().Before(nextConflictCheck) {
				nextConflictCheck = time.Now().Add(conflictRecheckInterval)
				if !wait {
					nextConflictCheck = time.Now().Add(24 * time.Hour)
				}
				if s.conflict(ctx, busy.conflicts) {
					continue
				}
			}
			if !wait {
				return Grant{}, ErrBusy
			}
			slog.Debug("waiting for cache lease", "session", s.record.ID)
		default:
			return Grant{}, err
		}
		timer := time.NewTimer(registryRetryInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return Grant{}, ctx.Err()
		case <-timer.C:
		}
	}
}

// Finish releases only after the supervisor verifies the whole session, including
// nested invocations. A failed verification leaves every resource protected.
func (s *Session) Finish(ctx context.Context, verify Verify) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished {
		return nil
	}
	if len(s.record.Paths) == 0 {
		s.finished = true
		return nil
	}
	// A record another process already released after proving the same
	// completion needs no further release.
	if err := s.registry.reconcile(ctx, s.record, verify); err != nil && !errors.Is(err, ErrAlreadyReleased) {
		return err
	}
	s.finished = true
	return nil
}

// Records returns durable intents for diagnostics and positive native recovery.
// All records are potentially poisoned: no automatic PID-based reclamation exists.
func (r *Registry) Records(ctx context.Context) ([]Record, error) {
	var out []Record
	err := r.transaction(ctx, func(c *sql.Conn) error { var err error; out, err = records(ctx, c); return err })
	return out, err
}

func (r *Registry) Reconcile(ctx context.Context, record Record, verify Verify) error {
	return r.reconcile(ctx, record, verify)
}
func (r *Registry) reconcile(ctx context.Context, record Record, verify Verify) error {
	if verify == nil {
		return ErrUnverified
	}
	snapshot := record
	snapshot.Paths = append([]string(nil), record.Paths...)
	if err := verify(ctx, snapshot); err != nil {
		return fmt.Errorf("%w: %v", ErrUnverified, err)
	}
	return r.transaction(ctx, func(c *sql.Conn) error {
		all, err := records(ctx, c)
		if err != nil {
			return err
		}
		for _, v := range all {
			if v.ID == record.ID {
				if !equal(v, record) {
					return ErrUnverified
				}
				_, err = c.ExecContext(ctx, "DELETE FROM leases WHERE id=?", v.ID)
				return err
			}
		}
		return ErrAlreadyReleased
	})
}
func equal(a, b Record) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func normalize(r Resources) ([]string, error) {
	var out []string
	for _, p := range append(append([]string(nil), r.Sources...), r.Destinations...) {
		if p == "" {
			return nil, errors.New("empty cache path")
		}
		// Cleaning "symlink/../cache" before resolving the link can select a
		// different cache from the OS. Require the discovery layer to resolve such
		// traversal first rather than silently protecting the wrong resource.
		for _, part := range strings.FieldsFunc(p, func(r rune) bool { return r < 128 && os.IsPathSeparator(uint8(r)) }) {
			if part == ".." {
				return nil, errors.New("resolve parent traversal in cache paths before acquiring a lease")
			}
		}
		p, err := filepath.Abs(p)
		if err != nil {
			return nil, err
		}
		var suffix []string
		for {
			resolved, err := filepath.EvalSymlinks(p)
			if err == nil {
				p = resolved
				break
			}
			if !os.IsNotExist(err) {
				return nil, err
			}
			// A dangling symlink is not an absent cache: do not invent its identity.
			if info, e := os.Lstat(p); e == nil && info.Mode()&os.ModeSymlink != 0 {
				return nil, errors.New("dangling cache path alias")
			}
			parent := filepath.Dir(p)
			if parent == p {
				return nil, err
			}
			suffix = append(suffix, filepath.Base(p))
			p = parent
		}
		for i := len(suffix) - 1; i >= 0; i-- {
			p = filepath.Join(p, suffix[i])
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil, errors.New("complete cache resource set is empty")
	}
	sort.Strings(out)
	return out, nil
}

func contains(parent, child string) bool {
	if pathContains(parent, child) {
		return true
	}
	// Compare existing ancestors and their absent suffixes. This covers aliases
	// through mounted directories even before either cache leaf has been created.
	for p := parent; ; p = filepath.Dir(p) {
		pi, err := os.Stat(p)
		if err == nil {
			ps, _ := filepath.Rel(p, parent)
			for c := child; ; c = filepath.Dir(c) {
				ci, err := os.Stat(c)
				if err == nil && os.SameFile(pi, ci) {
					cs, _ := filepath.Rel(c, child)
					if pathContains(ps, cs) {
						return true
					}
				}
				if filepath.Dir(c) == c {
					break
				}
			}
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	return false
}
func overlapsAny(requested, held []string) bool {
	for _, p := range requested {
		for _, q := range held {
			if overlap(p, q) {
				return true
			}
		}
	}
	return false
}
func overlap(a, b string) bool { return contains(a, b) || contains(b, a) }

// EnsurePrivateDirectory shares the registry's ownership and ACL checks with
// settings and process journals. It never repairs an unsafe existing directory.
func EnsurePrivateDirectory(path string) error { return privateDir(path) }

// ValidatePrivateFile checks an existing record without creating or rewriting it.
func ValidatePrivateFile(path string) error {
	if _, err := os.Lstat(path); err != nil {
		return err
	}
	return privateDatabase(path)
}

// ReleasePreparedSources releases newly acquired read resources after copying,
// before any command starts. The caller must pass only source paths from this
// acquisition's Grant.NewPaths, never a destination or a borrowed parent path.
// Existing process-writable claims still require whole-tree Finish verification.
func (s *Session) ReleasePreparedSources(paths []string, grant Grant) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished {
		return ErrUnverified
	}
	allowed := map[string]bool{}
	for _, p := range grant.NewPaths {
		allowed[p] = true
	}
	remove := map[string]bool{}
	for _, p := range paths {
		if !allowed[p] {
			return ErrUnverified
		}
		remove[p] = true
	}
	if len(remove) == 0 {
		return nil
	}
	next := s.record
	next.Paths = nil
	for _, p := range s.record.Paths {
		if !remove[p] {
			next.Paths = append(next.Paths, p)
		}
	}
	if len(next.Paths) == 0 {
		return ErrUnverified
	}
	e := s.registry.transaction(context.Background(), func(c *sql.Conn) error {
		all, e := records(context.Background(), c)
		if e != nil {
			return e
		}
		for _, r := range all {
			if r.ID == s.record.ID {
				if !equal(r, s.record) {
					return ErrUnverified
				}
				b, e := json.Marshal(next)
				if e != nil {
					return e
				}
				_, e = c.ExecContext(context.Background(), "UPDATE leases SET record=? WHERE id=?", string(b), r.ID)
				return e
			}
		}
		return ErrUnverified
	})
	if e == nil {
		s.record = next
	}
	return e
}

// ReleaseUnstartedGrant undoes an acquisition whose request failed before any
// command could use the resources. It removes exactly grant.NewPaths from the
// session record and never touches paths borrowed from the parent. Nested
// extensions always start from a nonempty record, so emptying the record is
// refused. An empty grant is a no-op without a registry write.
func (s *Session) ReleaseUnstartedGrant(grant Grant) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished {
		return ErrUnverified
	}
	if len(grant.NewPaths) == 0 {
		return nil
	}
	remove := map[string]bool{}
	for _, p := range grant.NewPaths {
		remove[p] = true
	}
	next := s.record
	next.Paths = nil
	for _, p := range s.record.Paths {
		if !remove[p] {
			next.Paths = append(next.Paths, p)
		}
	}
	if len(next.Paths) == 0 {
		return ErrUnverified
	}
	e := s.registry.transaction(context.Background(), func(c *sql.Conn) error {
		all, e := records(context.Background(), c)
		if e != nil {
			return e
		}
		for _, r := range all {
			if r.ID == s.record.ID {
				if !equal(r, s.record) {
					return ErrUnverified
				}
				b, e := json.Marshal(next)
				if e != nil {
					return e
				}
				_, e = c.ExecContext(context.Background(), "UPDATE leases SET record=? WHERE id=?", string(b), r.ID)
				return e
			}
		}
		return ErrUnverified
	})
	if e == nil {
		s.record = next
		slog.Debug("unstarted cache lease grant released", "session", s.record.ID, "paths", len(remove))
	}
	return e
}
