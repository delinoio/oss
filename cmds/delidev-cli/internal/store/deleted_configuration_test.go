package store

import (
	"bytes"
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestDeletedProjectPolicyAtomicRetentionAndRecovery(t *testing.T) {
	s, root := openTest(t)
	ctx := context.Background()
	f := newExecutionFixture(t, s)
	sessionID, inputID := f.session(t, domain.DispatchReady)
	projectID, repositoryID := domain.NewID(), domain.NewID()
	project := domain.Project{Name: "Private fixture", Repositories: []domain.ID{repositoryID}, PrimaryRepository: repositoryID, Agents: domain.Restriction{Configured: true, IDs: []domain.ID{f.agent}}, Accounts: domain.Restriction{Configured: true, IDs: []domain.ID{f.accounts[0]}}}
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.project-execution", nil, func(tx *Tx) (any, error) {
		if _, err := tx.Put(domain.ProjectKind, projectID, 0, "", "", project); err != nil {
			return nil, err
		}
		r, session, err := decodeEntity[domain.Session](tx, domain.SessionKind, sessionID)
		if err != nil {
			return nil, err
		}
		session.ProjectID, session.Workspace = projectID, domain.Worktree
		r, err = tx.Put(r.Kind, r.ID, r.Revision, r.ID, projectID, session)
		if err != nil {
			return nil, err
		}
		return tx.ClaimInitialExecution(r.ID, r.Revision, inputID, 1)
	})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := s.Get(ctx, domain.SessionKind, sessionID)
	session := readExecutionSession(t, s, sessionID)
	// Deletion preserves the latest restriction, even if it now excludes the
	// original account. The immutable snapshot is not a policy rollback source.
	project.Accounts.IDs = []domain.ID{f.accounts[1]}
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.restrict-project", nil, func(tx *Tx) (any, error) {
		return tx.Put(domain.ProjectKind, projectID, 1, "", "", project)
	})
	if err != nil {
		t.Fatal(err)
	}
	rollback := domain.Fail(domain.Canceled, "Fixture rollback after deletion.", "Keep original state.")
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.rollback-delete", nil, func(tx *Tx) (any, error) {
		if err := tx.Delete(domain.ProjectKind, projectID, 2); err != nil {
			return nil, err
		}
		return nil, rollback
	})
	assertCode(t, err, domain.Canceled)
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM deleted_project_policies").Scan(&count); err != nil || count != 0 {
		t.Fatal("failed deletion retained policy", count, err)
	}
	if _, err := s.Get(ctx, domain.ProjectKind, projectID); err != nil {
		t.Fatal("failed deletion removed project", err)
	}
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.delete", nil, func(tx *Tx) (any, error) {
		if err := tx.Delete(domain.ProjectKind, projectID, 2); err != nil {
			return nil, err
		}
		return nil, tx.Delete(domain.AgentKind, f.agent, 1)
	})
	if err != nil {
		t.Fatal(err)
	}
	after, _ := s.Get(ctx, domain.SessionKind, sessionID)
	if before.Revision != after.Revision || !bytes.Equal(before.Data, after.Data) {
		t.Fatal("configuration deletion rewrote session history or snapshot")
	}
	s.Close()
	s, err = Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Read(ctx, func(tx *Tx) error {
		policy, err := tx.ExecutionProjectPolicy(session)
		if err != nil {
			return err
		}
		if !policy.Agents.Allows(f.agent) || policy.Agents.Allows(domain.NewID()) || policy.Accounts.Allows(session.InitialExecution.InitialAccountID) || !policy.Accounts.Allows(f.accounts[1]) {
			t.Fatal("deletion widened selection restrictions")
		}
		if err := tx.RequireExecutionAgent(session, f.agent); err != nil {
			return err
		}
		unstarted := session
		unstarted.InitialExecution = nil
		_, err = tx.ExecutionProjectPolicy(unstarted)
		assertCode(t, err, domain.NotFound)
		assertCode(t, tx.RequireExecutionAgent(unstarted, f.agent), domain.NotFound)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{}`, `{"agents":{},"accounts":{}}`, `{"agents":{"ids":[]},"accounts":{"configured":false,"ids":null}}`, `{"agents":{"configured":true,"ids":[]},"accounts":{"configured":false,"ids":[]},"untrusted":true}`, `{"agents":{"configured":false,"ids":["invalid"]},"accounts":{"configured":false,"ids":[]}}`} {
		if _, err := s.db.Exec("UPDATE deleted_project_policies SET body=? WHERE project_id=?", []byte(raw), projectID); err != nil {
			t.Fatal(err)
		}
		assertCode(t, s.Read(ctx, func(tx *Tx) error { _, err := tx.ExecutionProjectPolicy(session); return err }), domain.RecoveryRequired)
	}
	if _, err := s.db.Exec("DELETE FROM deleted_project_policies WHERE project_id=?", projectID); err != nil {
		t.Fatal(err)
	}
	assertCode(t, s.Read(ctx, func(tx *Tx) error { _, err := tx.ExecutionProjectPolicy(session); return err }), domain.RecoveryRequired)
	if _, err := s.db.Exec("DELETE FROM tombstones WHERE id=?", f.agent); err != nil {
		t.Fatal(err)
	}
	assertCode(t, s.Read(ctx, func(tx *Tx) error { return tx.RequireExecutionAgent(session, f.agent) }), domain.NotFound)
}

func TestDeletedConfigurationMigrationPreservesV11Schedules(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(map[bool]string{false: "preserve", true: "schema-conflict"}[conflict], func(t *testing.T) {
			s, root := openTest(t)
			r, value := scheduleFixture(t, s)
			occurrence, schedule, _ := appendWaiting(t, s, r, value)
			if !conflict {
				if _, err := s.db.Exec("DROP TABLE deleted_project_policies"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.db.Exec("PRAGMA user_version=11"); err != nil {
				t.Fatal(err)
			}
			s.Close()
			ctx := context.Background()
			migrated, err := Open(ctx, root)
			if conflict {
				if err == nil {
					migrated.Close()
					t.Fatal("conflicting schema migrated")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				for _, before := range []Record{schedule, occurrence} {
					after, err := migrated.Get(ctx, before.Kind, before.ID)
					if err != nil || after.Revision != before.Revision || !bytes.Equal(after.Data, before.Data) {
						t.Fatal("migration changed accepted schedule state", err)
					}
				}
				migrated.Close()
			}
			backups, _ := filepath.Glob(filepath.Join(root, "backups", "*.sqlite"))
			if len(backups) != 1 {
				t.Fatal("missing pre-migration backup")
			}
			for _, path := range []string{backups[0], filepath.Join(root, "state.sqlite")} {
				db, err := sql.Open("sqlite", databaseURI(path, true))
				if err != nil {
					t.Fatal(err)
				}
				var version int
				if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
					t.Fatal(err)
				}
				db.Close()
				want := 11
				if path != backups[0] && !conflict {
					want = SchemaVersion
				}
				if version != want {
					t.Fatal("migration/backup version changed", version, want)
				}
			}
		})
	}
}
