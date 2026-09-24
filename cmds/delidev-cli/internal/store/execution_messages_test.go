package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestExecutionMessageMigrationPreservesExistingGrantAndBackup(t *testing.T) {
	s, root := openTest(t)
	ctx := context.Background()
	digest := sha256.Sum256([]byte("private-fixture-token"))
	grant := ExecutionGrant{JobID: domain.NewID(), Digest: digest[:], ExecutionID: domain.NewID(), MachineID: domain.NewID(), InstanceID: domain.NewID(), DeviceID: domain.NewID(), ServerEpoch: domain.NewID()}
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.v7-grant", nil, func(tx *Tx) (any, error) {
		if _, err := tx.PutJob(grant.JobID, 0, "", "", domain.Job{Type: domain.ExecuteSessionJob, State: domain.JobClaimed, MachineID: grant.MachineID, InstanceID: grant.InstanceID, Input: json.RawMessage(`{}`), AcceptedAt: time.Now().UTC()}); err != nil {
			return nil, err
		}
		return nil, tx.PutExecutionGrant(grant)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("DROP TABLE execution_messages; PRAGMA user_version=7;"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Read(ctx, func(tx *Tx) error {
		retained, err := tx.ExecutionGrant(digest[:])
		if err != nil {
			return err
		}
		if retained.JobID != grant.JobID || retained.ServerEpoch != grant.ServerEpoch {
			t.Fatal("message migration replaced existing execution authority")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	backups, err := filepath.Glob(filepath.Join(root, "backups", "*.sqlite"))
	if err != nil || len(backups) != 1 {
		t.Fatal("message migration did not retain a backup")
	}
	backup, err := sql.Open("sqlite", databaseURI(backups[0], true))
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	var version, count int
	if err := backup.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 7 {
		t.Fatal("message migration changed the pre-migration backup")
	}
	if err := backup.QueryRow("SELECT COUNT(*) FROM execution_grants").Scan(&count); err != nil || count != 1 {
		t.Fatal("message migration backup lost accepted authority")
	}
	if err := s.db.QueryRow("SELECT COUNT(*) FROM execution_messages").Scan(&count); err != nil || count != 0 {
		t.Fatal("message migration fabricated native message ownership")
	}
}
