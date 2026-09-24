package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestExecutionAuthorityMigrationPreservesAssignmentsAndBackup(t *testing.T) {
	s, root := openTest(t)
	ctx := context.Background()
	jobID := domain.NewID()
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.claim", nil, func(tx *Tx) (any, error) {
		return tx.PutJob(jobID, 0, "", "", domain.Job{Type: domain.HarnessDiscoveryJob, State: domain.JobClaimed, MachineID: domain.NewID(), InstanceID: domain.NewID(), Input: json.RawMessage(`{}`), AcceptedAt: time.Now().UTC()})
	})
	if err != nil {
		t.Fatal(err)
	}
	original, err := s.Get(ctx, domain.JobKind, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("DROP TABLE execution_references; DROP TABLE execution_grants; PRAGMA user_version=6;"); err != nil {
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
	backups, err := filepath.Glob(filepath.Join(root, "backups", "*.sqlite"))
	if err != nil || len(backups) != 1 {
		t.Fatal("migration did not preserve its v6 backup")
	}
	backup, err := sql.Open("sqlite", databaseURI(backups[0], true))
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	var version, grants int
	if err := backup.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 6 {
		t.Fatal("backup does not contain the original schema")
	}
	if err := backup.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE name='execution_grants'").Scan(&grants); err != nil || grants != 0 {
		t.Fatal("migration changed the backup")
	}
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != SchemaVersion {
		t.Fatal("migration did not install the execution authority schema")
	}
	if err := s.Read(ctx, func(tx *Tx) error {
		retained, err := tx.JobAssignment(jobID)
		if err == nil && (retained.ID != original.ID || retained.Revision != original.Revision || !bytes.Equal(retained.Data, original.Data)) {
			t.Fatal("execution migration changed an existing assignment")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestExecutionGrantAndNativeReferenceIsolationSurviveRestart(t *testing.T) {
	s, root := openTest(t)
	ctx := context.Background()
	digest := sha256.Sum256([]byte("private-fixture-execution-token"))
	grant := ExecutionGrant{JobID: domain.NewID(), Digest: digest[:], ExecutionID: domain.NewID(), MachineID: domain.NewID(), InstanceID: domain.NewID(), DeviceID: domain.NewID(), ServerEpoch: domain.NewID()}
	ref := ExecutionReference{SessionID: domain.NewID(), AccountID: domain.NewID(), ConnectionID: domain.NewID(), ModelID: domain.NewID(), Kind: domain.NativeResponseReference, NativeID: "resp_fixture"}
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.authority", nil, func(tx *Tx) (any, error) {
		if _, err := tx.Put(domain.SessionKind, ref.SessionID, 0, ref.SessionID, "", struct{}{}); err != nil {
			return nil, err
		}
		if _, err := tx.PutJob(grant.JobID, 0, ref.SessionID, "", domain.Job{Type: domain.ExecuteSessionJob, State: domain.JobClaimed, MachineID: grant.MachineID, InstanceID: grant.InstanceID, Input: json.RawMessage(`{}`), AcceptedAt: time.Now().UTC()}); err != nil {
			return nil, err
		}
		if err := tx.PutExecutionGrant(grant); err != nil {
			return nil, err
		}
		return struct{}{}, tx.ObserveExecutionReference(ref)
	})
	if err != nil {
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
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.repeat-authority", nil, func(tx *Tx) (any, error) {
		if err := tx.PutExecutionGrant(grant); err != nil {
			return nil, err
		}
		return struct{}{}, tx.ObserveExecutionReference(ref)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Read(ctx, func(tx *Tx) error {
		stored, err := tx.ExecutionGrant(digest[:])
		if err != nil {
			return err
		}
		if stored.JobID != grant.JobID || stored.ExecutionID != grant.ExecutionID || stored.ServerEpoch != grant.ServerEpoch {
			t.Fatal("execution binding changed across restart")
		}
		for _, field := range []string{"same", "session", "account", "connection", "model", "kind", "native"} {
			candidate := ref
			switch field {
			case "session":
				candidate.SessionID = domain.NewID()
			case "account":
				candidate.AccountID = domain.NewID()
			case "connection":
				candidate.ConnectionID = domain.NewID()
			case "model":
				candidate.ModelID = domain.NewID()
			case "kind":
				candidate.Kind = domain.NativeConversationReference
			case "native":
				candidate.NativeID = "resp_foreign"
			}
			exists, err := tx.HasExecutionReference(candidate)
			if err != nil || exists != (field == "same") {
				t.Fatalf("native reference escaped %s isolation: %v", field, err)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	changed := grant
	changed.ServerEpoch = domain.NewID()
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.replace-grant", nil, func(tx *Tx) (any, error) { return nil, tx.PutExecutionGrant(changed) })
	assertCode(t, err, domain.Conflict)
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.delete-authority", nil, func(tx *Tx) (any, error) {
		if err := tx.Delete(domain.JobKind, grant.JobID, 1); err != nil {
			return nil, err
		}
		return nil, tx.Delete(domain.SessionKind, ref.SessionID, 1)
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"execution_grants", "execution_references"} {
		var retained int
		if err := s.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&retained); err != nil || retained != 0 {
			t.Fatal("deletion retained execution authority or native references")
		}
	}
}
