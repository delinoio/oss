package store

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestAssignmentMigrationAndUncertainResultPreserveOriginalEnvelope(t *testing.T) {
	s, root := openTest(t)
	ctx := context.Background()
	id := domain.NewID()
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.assignment", nil, func(tx *Tx) (any, error) {
		return tx.PutJob(id, 0, domain.NewID(), "", domain.Job{Type: domain.PrepareWorkspaceJob, State: domain.JobClaimed, MachineID: domain.NewID(), InstanceID: domain.NewID(), Input: json.RawMessage(`{}`), AcceptedAt: time.Now().UTC()})
	})
	if err != nil {
		t.Fatal(err)
	}
	original, err := s.Get(ctx, domain.JobKind, id)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.before-migration-uncertainty", nil, func(tx *Tx) (any, error) {
		job, err := Decode[domain.Job](original)
		if err != nil {
			return nil, err
		}
		job.State = domain.JobUncertain
		return tx.PutJob(id, original.Revision, original.SessionID, "", job)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("DROP INDEX inbox_source; DROP INDEX inbox_read; DROP INDEX inbox_session_read; DROP TABLE execution_interactions; DROP TABLE execution_messages; DROP TABLE execution_references; DROP TABLE execution_grants; DROP TABLE job_assignments; PRAGMA user_version=5;"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	backups, _ := filepath.Glob(filepath.Join(root, "backups", "*.sqlite"))
	if len(backups) != 1 {
		t.Fatal("missing v5 backup")
	}
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.uncertain", nil, func(tx *Tx) (any, error) {
		job, err := Decode[domain.Job](original)
		if err != nil {
			return nil, err
		}
		job.State = domain.JobUncertain
		job.Problem = domain.Fail(domain.RecoveryRequired, "fixture", "")
		return tx.PutJob(id, original.Revision+1, original.SessionID, "", job)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Read(ctx, func(tx *Tx) error {
		r, err := tx.JobAssignment(id)
		if err != nil {
			return err
		}
		if r.Revision != original.Revision || r.SessionID != original.SessionID || !bytes.Equal(r.Data, original.Data) {
			t.Fatal("uncertainty changed the original assignment")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.delete", nil, func(tx *Tx) (any, error) { return nil, tx.Delete(domain.JobKind, id, original.Revision+2) })
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM job_assignments").Scan(&count); err != nil || count != 0 {
		t.Fatal("job deletion retained assignment contents")
	}
}
