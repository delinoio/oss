package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestCancellationMigratesV4AndPreservesClaimedEnvelopeAcrossRestart(t *testing.T) {
	s, root := openTest(t)
	ctx := context.Background()
	id := domain.NewID()
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.claimed", nil, func(tx *Tx) (any, error) {
		return tx.PutJob(id, 0, "", "", domain.Job{Type: domain.PrepareWorkspaceJob, State: domain.JobClaimed, MachineID: domain.NewID(), InstanceID: domain.NewID(), Input: json.RawMessage(`{}`), AcceptedAt: time.Now().UTC()})
	})
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.Get(ctx, domain.JobKind, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("DROP TABLE job_assignments; DROP TABLE job_cancellations; PRAGMA user_version=4;"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	backups, err := filepath.Glob(filepath.Join(root, "backups", "*.sqlite"))
	if err != nil || len(backups) != 1 {
		t.Fatal("missing v4 backup")
	}
	backup, err := sql.Open("sqlite", databaseURI(backups[0], true))
	if err != nil {
		t.Fatal(err)
	}
	var version int
	if err := backup.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	backup.Close()
	if version != 4 {
		t.Fatal("backup captured migrated schema")
	}
	changed := s.Changed()
	for i := 0; i < 2; i++ {
		_, err = s.Mutate(ctx, domain.NewID(), "fixture.cancel", nil, func(tx *Tx) (any, error) { return nil, tx.RequestJobCancellation(id) })
		if err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-changed:
	default:
		t.Fatal("cancellation did not wake the Worker stream")
	}
	s.Close()
	s, err = Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Read(ctx, func(tx *Tx) error {
		found, err := tx.JobCancellationRequested(id)
		if !found {
			t.Fatal("cancellation was lost on restart")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	after, err := s.Get(ctx, domain.JobKind, id)
	if err != nil || before.Revision != after.Revision || !bytes.Equal(before.Data, after.Data) {
		t.Fatal("cancellation rewrote the claimed envelope")
	}
	if err := s.Read(ctx, func(tx *Tx) error { return tx.RequestJobCancellation(id) }); domain.SafeError(err).Code != domain.PermissionDenied {
		t.Fatal("read-only cancellation accepted")
	}
}
