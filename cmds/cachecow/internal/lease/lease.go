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
	r := &Registry{db: db, dir: dir}
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
// resources. Use Open for nonblocking management operations such as clean.
func OpenWait(ctx context.Context, dir string) (*Registry, error) {
	for {
		r, err := Open(ctx, dir)
		if !errors.Is(err, ErrBusy) {
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

func (r *Registry) transaction(ctx context.Context, fn func(*sql.Conn) error) (result error) {
	defer func() {
		var code interface{ Code() int }
		if errors.As(result, &code) && (code.Code()&255 == 5 || code.Code()&255 == 6) {
			result = ErrBusy
		}
	}()
	c, err := r.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	// Zero busy timeout keeps clean nonblocking and lets waiters honor context.
	if _, err = c.ExecContext(ctx, "PRAGMA busy_timeout=0; PRAGMA synchronous=FULL"); err != nil {
		return err
	}
	if _, err = c.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		var code interface{ Code() int }
		if errors.As(err, &code) && (code.Code()&255 == 5 || code.Code()&255 == 6) {
			return ErrBusy
		}
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
// is true, the caller must skip preparation. Grants have no independent release:
// all accumulated resources remain held until the entire session is finished.
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
	for {
		if err := ctx.Err(); err != nil {
			return Grant{}, err
		}
		borrowed := len(s.record.Paths) > 0
		var added []string
		next := s.record
		next.Paths = append([]string(nil), s.record.Paths...)
		err = s.registry.transaction(ctx, func(c *sql.Conn) error {
			all, err := records(ctx, c)
			if err != nil {
				return err
			}
			found := false
			for _, v := range all {
				if v.ID == s.record.ID {
					found = true
					if !equal(v, s.record) {
						return ErrUnverified
					}
					continue
				}
				for _, p := range paths {
					for _, q := range v.Paths {
						if overlap(p, q) {
							return ErrBusy
						}
					}
				}
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
		if !errors.Is(err, ErrBusy) {
			return Grant{}, err
		}
		if len(s.record.Paths) > 0 {
			return Grant{}, ErrNestedConflict
		}
		if !wait {
			return Grant{}, ErrBusy
		}
		slog.Debug("waiting for cache lease", "session", s.record.ID)
		timer := time.NewTimer(25 * time.Millisecond)
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
	if err := s.registry.reconcile(ctx, s.record, verify); err != nil {
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
		return ErrUnverified
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
func overlap(a, b string) bool { return contains(a, b) || contains(b, a) }
