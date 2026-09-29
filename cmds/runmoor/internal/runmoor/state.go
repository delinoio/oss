package runmoor

import (
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

func uuidParse(s string) (string, error) {
	v, err := uuid.Parse(s)
	if err == nil && v.Version() != 7 {
		return "", problem(ErrConfig, "Expected UUID-v7.", "Use a revision created by Runmoor.")
	}
	return v.String(), err
}

type Store struct {
	mu    sync.Mutex
	db    *sql.DB
	lock  *os.File
	state Snapshot
}

// rejectAmbiguousLegacySQLitePath protects databases opened by earlier
// Runmoor versions that passed a filesystem path to the SQLite driver as a raw
// DSN. In that form, the driver treats the first '?' as the start of DSN
// options. If the correctly addressed database is missing or empty while the
// old prefix is still present, preserve both locations and require explicit
// operator recovery. Remove this guard only with a separately specified
// recovery path that can establish ownership of the legacy database without data loss.
func rejectAmbiguousLegacySQLitePath(path string) error {
	question := strings.IndexByte(path, '?')
	if question < 0 {
		return nil
	}

	info, err := os.Stat(path)
	if err == nil && info.Size() > 0 {
		return nil
	}
	if err != nil && !os.IsNotExist(err) {
		return problem(ErrState, "Cannot inspect SQLite state.", "Check access to the private state directory and retry.")
	}

	legacyInfo, err := os.Stat(path[:question])
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return problem(ErrState, "Cannot inspect possible legacy SQLite state.", "Stop Runmoor managers and check access to the private state directory before retrying.")
	}
	if legacyInfo.Mode().IsRegular() {
		return problem(ErrState, "Possible legacy SQLite state exists at an ambiguous location.", "Stop all Runmoor managers configured for either location. Preserve complete backups of both locations before explicit recovery; do not move or delete either database.")
	}
	return nil
}

func OpenStore(c Config) (*Store, error) {
	path := filepath.Join(c.Storage.State, "state.sqlite")
	if err := rejectAmbiguousLegacySQLitePath(path); err != nil {
		return nil, err
	}
	if err := privateDir(c.Storage.State); err != nil {
		return nil, err
	}
	if err := privateDir(c.Storage.Data); err != nil {
		return nil, err
	}
	lock, err := lockState(filepath.Join(c.Storage.State, "manager.lock"))
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*Store, error) { unlockState(lock); return nil, err }
	f, err := openPrivate(path, os.O_RDWR|os.O_CREATE)
	if err != nil {
		return fail(err)
	}
	f.Close()
	uri := url.URL{Scheme: "file", Path: path}
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return fail(problem(ErrState, "Cannot open SQLite state.", "Check state directory permissions and free space."))
	}
	db.SetMaxOpenConns(1)
	failed := func(p *Problem) (*Store, error) { db.Close(); return fail(p) }
	var version int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return failed(problem(ErrState, "Cannot read SQLite state version.", "Restore a compatible state backup."))
	}
	if version != 0 && version != 1 && version != 2 && version != 3 {
		return failed(problem(ErrStateVersion, "This binary supports SQLite schema versions 1 through 3 only.", "Use the matching binary or restore its matching backup; do not downgrade the database."))
	}
	if version == 0 {
		var tables int
		if err = db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'").Scan(&tables); err != nil || tables != 0 {
			return failed(problem(ErrStateVersion, "An unversioned nonempty database cannot be adopted.", "Choose an empty state directory or restore a compatible backup."))
		}
	}
	if _, err = db.Exec("PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL; PRAGMA busy_timeout=5000;"); err != nil {
		return failed(problem(ErrState, "Cannot initialize durable SQLite storage.", "Check the filesystem and available disk space."))
	}
	s := &Store{db: db, lock: lock}
	relocated := false
	if version == 0 {
		s.state = Snapshot{SchemaVersion: 3, Requested: c, Managed: map[string]*ManagedPool{}, Artifacts: map[string]*RunnerArtifact{}, Installation: newID(), Pools: map[string]*PoolState{}, Runners: map[string]*Runner{}, Images: map[string]*Image{}, ImageTartPIDs: map[string]int{}, Generations: map[string]Config{}, Config: c}
		b, _ := json.Marshal(s.state)
		tx, e := db.Begin()
		if e == nil {
			_, e = tx.Exec("CREATE TABLE snapshot (id INTEGER PRIMARY KEY CHECK(id=1), body BLOB NOT NULL); PRAGMA user_version=3;")
			if e == nil {
				_, e = tx.Exec("INSERT INTO snapshot(id,body) VALUES(1,?)", b)
			}
			if e == nil {
				e = tx.Commit()
			} else {
				tx.Rollback()
			}
		}
		if e != nil {
			return failed(problem(ErrState, "Cannot commit initial state.", "Check free disk space and retry."))
		}
	} else {
		var b []byte
		if err = db.QueryRow("SELECT body FROM snapshot WHERE id=1").Scan(&b); err != nil {
			return failed(problem(ErrState, "State snapshot is missing.", "Restore a compatible backup."))
		}
		if err = json.Unmarshal(b, &s.state); err != nil || (s.state.SchemaVersion != version) || !validID(s.state.Installation) || s.state.Pools == nil || s.state.Runners == nil || s.state.Images == nil || s.state.Generations == nil {
			return failed(problem(ErrState, "State snapshot is invalid.", "Preserve the database for diagnosis and restore a compatible backup."))
		}
		if version == 1 {
			// Snapshot and database version move together. Existing ownership,
			// reservations and generation payloads are never reinterpreted.
			s.state.SchemaVersion = 2
			s.state.Requested = s.state.Config
			s.state.Managed = map[string]*ManagedPool{}
			s.state.Artifacts = map[string]*RunnerArtifact{}
			body, e := json.Marshal(s.state)
			if e != nil {
				return failed(problem(ErrState, "Cannot migrate state.", "Preserve a complete state backup."))
			}
			tx, e := db.Begin()
			if e == nil {
				_, e = tx.Exec("UPDATE snapshot SET body=? WHERE id=1", body)
				if e == nil {
					_, e = tx.Exec("PRAGMA user_version=2")
				}
				if e == nil {
					e = tx.Commit()
				} else {
					tx.Rollback()
				}
			}
			if e != nil {
				return failed(problem(ErrState, "Cannot atomically migrate SQLite v1 to v2.", "Restore storage access and retry with this binary."))
			}
			version = 2
		}
		if version == 2 {
			// Process-start identities are additive private state. Existing
			// numeric-only Tart records remain conservative until their PID exits.
			s.state.SchemaVersion = 3
			body, e := json.Marshal(s.state)
			if e != nil {
				return failed(problem(ErrState, "Cannot migrate state.", "Preserve a complete state backup."))
			}
			tx, e := db.Begin()
			if e == nil {
				_, e = tx.Exec("UPDATE snapshot SET body=? WHERE id=1", body)
				if e == nil {
					_, e = tx.Exec("PRAGMA user_version=3")
				}
				if e == nil {
					e = tx.Commit()
				} else {
					tx.Rollback()
				}
			}
			if e != nil {
				return failed(problem(ErrState, "Cannot atomically migrate SQLite v2 to v3.", "Restore storage access and retry with this binary."))
			}
		}
		if s.state.Managed == nil || s.state.Artifacts == nil {
			return failed(problem(ErrState, "Managed state is missing.", "Restore a compatible complete backup."))
		}
		if s.state.Config.Storage != c.Storage {
			for _, a := range s.state.Artifacts {
				if a.Reserved || a.Phase != ArtifactReady {
					return failed(problem(ErrConfig, "Storage relocation requires completed managed image preparation and cleanup.", "Finish runner updates and cleanup at the original storage locations first."))
				}
			}
			for _, r := range s.state.Runners {
				if r.Phase != Completed {
					return failed(problem(ErrConfig, "Storage relocation requires completed execution cleanup.", "Restore the original storage locations and drain before moving the complete backup."))
				}
			}
			for _, im := range s.state.Images {
				if im.Phase == ImageOpen || im.Phase == ImageRemoving {
					return failed(problem(ErrConfig, "Storage relocation requires closed images and completed removal.", "Finish image operations at the original storage locations first."))
				}
			}
			relocated = true
		}
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if _, e := os.Stat(path + suffix); e == nil {
			if e = os.Chmod(path+suffix, 0600); e != nil {
				return failed(problem(ErrPermission, "Cannot restrict SQLite files.", "Use an owner-controlled state directory."))
			}
		}
	}
	if relocated {
		if err := s.Update(func(v *Snapshot) error {
			v.Config.Storage = c.Storage
			v.Requested.Storage = c.Storage
			for id, generation := range v.Generations {
				generation.Storage = c.Storage
				v.Generations[id] = generation
			}
			return nil
		}); err != nil {
			db.Close()
			return fail(err)
		}
	}
	return s, nil
}
func cloneSnapshot(s Snapshot) Snapshot {
	b, _ := json.Marshal(s)
	var out Snapshot
	_ = json.Unmarshal(b, &out)
	return out
}
func (s *Store) View() Snapshot { s.mu.Lock(); defer s.mu.Unlock(); return cloneSnapshot(s.state) }
func (s *Store) Update(fn func(*Snapshot) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := cloneSnapshot(s.state)
	if err := fn(&next); err != nil {
		return err
	}
	b, err := json.Marshal(next)
	if err != nil {
		return problem(ErrState, "Cannot serialize state.", "Stop new work and inspect the installation.")
	}
	if _, err = s.db.Exec("UPDATE snapshot SET body=? WHERE id=1", b); err != nil {
		return problem(ErrState, "Cannot persist state transition.", "Free disk space and retry; existing resource reservations are retained.")
	}
	s.state = next
	return nil
}
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = s.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	err := s.db.Close()
	unlockState(s.lock)
	return err
}
func (s *Store) Prune(now time.Time) error {
	return s.Update(func(v *Snapshot) error {
		for id, r := range v.Runners {
			if r.Phase == Completed && !r.CompletedAt.IsZero() && now.Sub(r.CompletedAt) > 7*24*time.Hour {
				delete(v.Runners, id)
				delete(v.RunnerTartStarts, id)
			}
		}
		for id, p := range v.Pools {
			if p.Phase != Retired {
				continue
			}
			used := false
			for _, r := range v.Runners {
				if r.PoolID == id {
					used = true
					break
				}
			}
			if !used {
				delete(v.Pools, id)
			}
		}
		used := map[string]bool{v.Generation: true}
		for _, p := range v.Pools {
			used[p.Generation] = true
		}
		for _, r := range v.Runners {
			used[r.Generation] = true
		}
		for id := range v.Generations {
			if !used[id] {
				delete(v.Generations, id)
			}
		}
		return nil
	})
}

// ReadSnapshot never creates or migrates storage and is safe beside a manager.
func ReadSnapshot(c Config) (Snapshot, error) {
	var s Snapshot
	path := filepath.Join(c.Storage.State, "state.sqlite")
	if _, err := os.Stat(path); err != nil {
		return s, err
	}
	f, err := openPrivate(path, os.O_RDONLY)
	if err != nil {
		return s, err
	}
	f.Close()
	uri := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return s, problem(ErrState, "Cannot read state.", "Inspect manager status.")
	}
	defer db.Close()
	var version int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || (version != 1 && version != 2 && version != 3) {
		return s, problem(ErrStateVersion, "Unsupported state version.", "Use a compatible Runmoor binary.")
	}
	var body []byte
	if err = db.QueryRow("SELECT body FROM snapshot WHERE id=1").Scan(&body); err != nil || json.Unmarshal(body, &s) != nil || s.SchemaVersion != version {
		return s, problem(ErrState, "Cannot read the state snapshot.", "Restore a complete compatible backup.")
	}
	if version == 1 {
		s.Requested = s.Config
	}
	return s, nil
}
