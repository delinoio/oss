package runmoor

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
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

func OpenStore(c Config) (*Store, error) {
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
	path := filepath.Join(c.Storage.State, "state.sqlite")
	f, err := openPrivate(path, os.O_RDWR|os.O_CREATE)
	if err != nil {
		return fail(err)
	}
	f.Close()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return fail(problem(ErrState, "Cannot open SQLite state.", "Check state directory permissions and free space."))
	}
	db.SetMaxOpenConns(1)
	failed := func(p *Problem) (*Store, error) { db.Close(); return fail(p) }
	var version int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return failed(problem(ErrState, "Cannot read SQLite state version.", "Restore a compatible state backup."))
	}
	if version != 0 && version != 1 {
		return failed(problem(ErrStateVersion, "This binary supports SQLite schema version 1 only.", "Use the matching binary or restore its matching backup; do not downgrade the database."))
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
		s.state = Snapshot{SchemaVersion: 1, Installation: newID(), Pools: map[string]*PoolState{}, Runners: map[string]*Runner{}, Images: map[string]*Image{}, Generations: map[string]Config{}, Config: c}
		b, _ := json.Marshal(s.state)
		tx, e := db.Begin()
		if e == nil {
			_, e = tx.Exec("CREATE TABLE snapshot (id INTEGER PRIMARY KEY CHECK(id=1), body BLOB NOT NULL); PRAGMA user_version=1;")
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
		if err = json.Unmarshal(b, &s.state); err != nil || s.state.SchemaVersion != 1 || !validID(s.state.Installation) || s.state.Pools == nil || s.state.Runners == nil || s.state.Images == nil || s.state.Generations == nil {
			return failed(problem(ErrState, "State snapshot is invalid.", "Preserve the database for diagnosis and restore a compatible backup."))
		}
		if s.state.Config.Storage != c.Storage {
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
