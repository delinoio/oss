package core

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
)

const repositoryIdentityFile = "ach-repository-id"

func repositoryIdentity(common string, create bool) (string, error) {
	if create {
		if err := AtomicCreate(filepath.Join(common, repositoryIdentityFile), []byte(ID()+"\n"), 0600); err != nil && !os.IsExist(err) {
			return "", err
		}
	}
	data, err := ReadOwned(common, repositoryIdentityFile, 128)
	if err != nil {
		return "", E("repository-unregistered", "run ach init to explicitly trust this Git directory", 2)
	}
	id := strings.TrimSpace(string(data))
	if !ValidID(id) {
		return "", E("repository-identity-invalid", "Git directory identity is invalid; restore its local identity file", 2)
	}
	return id, nil
}

// Early v1 states keyed trust only by paths. Rebuild those two registry tables
// transactionally without changing historical IDs or their run references.
func (s *Store) migrateRegistry() error {
	if _, err := s.DB.Exec("PRAGMA foreign_keys=OFF"); err != nil {
		return err
	}
	defer s.DB.Exec("PRAGMA foreign_keys=ON")
	return s.Transaction(func(tx *sql.Tx) error {
		var count int
		if err := tx.QueryRow("SELECT count(*) FROM pragma_table_info('repositories') WHERE name='local_identity'").Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return nil
		}
		_, err := tx.Exec(`
CREATE TABLE repositories_next(id TEXT PRIMARY KEY, common_dir TEXT NOT NULL, name TEXT NOT NULL, local_identity TEXT NOT NULL, UNIQUE(common_dir,local_identity));
INSERT INTO repositories_next(id,common_dir,name,local_identity) SELECT id,common_dir,name,'' FROM repositories;
CREATE TABLE worktrees_next(id TEXT PRIMARY KEY, repository_id TEXT NOT NULL REFERENCES repositories(id), path TEXT NOT NULL, branch TEXT NOT NULL, UNIQUE(repository_id,path));
INSERT INTO worktrees_next SELECT * FROM worktrees;
DROP TABLE worktrees;
DROP TABLE repositories;
ALTER TABLE repositories_next RENAME TO repositories;
ALTER TABLE worktrees_next RENAME TO worktrees;`)
		if err != nil {
			return err
		}
		rows, err := tx.Query("PRAGMA foreign_key_check")
		if err != nil {
			return err
		}
		defer rows.Close()
		if rows.Next() {
			return E("state-corrupt", "registry migration found invalid history references", 3)
		}
		return rows.Err()
	})
}
