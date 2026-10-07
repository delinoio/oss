// SPDX-License-Identifier: Apache-2.0
package store

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func TestBackupRestoreRejectsSQLDeletionBeforeExternalIntent(t *testing.T) {
	s, root, ctx, input, _ := restoreFixture(t)
	source := filepath.Join(root, "backups", string(input.Backup.ID)+".sqlite")
	original, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	// Fail only the external intent write, after the real acceptance path has
	// committed its job, receipt and SQL obligation. Recreate the private intent
	// directory so its absence cannot mask the SQL-only window during restore.
	intentRoot := filepath.Dir(s.deletionPath(input.Backup.ID))
	if err := os.Remove(intentRoot); err != nil {
		t.Fatal(err)
	}
	deletionRequest := domain.NewID()
	deletionInput := BackupDeletionInput{Actor: input.Actor, ServerID: input.ServerID, Backup: input.Backup, SHA256: input.SHA256, ExpectedRevision: 1}
	job, _, err := s.DeleteBackup(ctx, deletionRequest, deletionInput)
	if domain.SafeError(err).Code != domain.Unavailable || job.ID.Validate() != nil {
		t.Fatal("fixture did not retain SQL acceptance before intent failure", err)
	}
	if err := security.PrivateDir(intentRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := s.readDeletionIntent(input.Backup.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("fixture has an external intent", err)
	}
	var obligated bool
	if err := s.db.QueryRow("SELECT EXISTS(SELECT 1 FROM backup_deletions WHERE backup_id=?)", input.Backup.ID).Scan(&obligated); err != nil || !obligated {
		t.Fatal("fixture lost its durable SQL obligation", err)
	}
	input.ExpectedRevision, err = s.RestoreRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	restoreRequest := domain.NewID()
	if _, _, err := s.RestoreBackup(ctx, restoreRequest, input); err == nil || domain.SafeError(err).Code != domain.Conflict || s.RestoreFrozen() {
		t.Fatal("restore published a deletion-owned image", err)
	}
	revision, err := s.RestoreRevision(ctx)
	if err != nil || revision != input.ExpectedRevision || s.restoreReservations[restoreRequest] {
		t.Fatal("rejected restore changed live state or accepted a request", err)
	}
	current, err := os.ReadFile(source)
	if err != nil || !bytes.Equal(current, original) {
		t.Fatal("rejected restore changed the selected source", err)
	}
	// Recovery retains the original deletion UUID/job rather than accepting a
	// replacement after the failed restore.
	replayed, replay, err := s.DeleteBackup(ctx, deletionRequest, deletionInput)
	if err != nil || !replay || replayed.ID != job.ID {
		t.Fatal("original deletion could not finish intent persistence", err)
	}
}

func TestBackupRestoreContinuesWithPendingSessionDeletion(t *testing.T) {
	s, root, ctx, in, _ := restoreFixture(t)
	row, _, _ := deletionSession(t, s, "pending")
	if _, _, err := s.DeleteSession(ctx, domain.NewID(), row.ID, in.ServerID, row.Revision); err != nil {
		t.Fatal(err)
	}
	in.ExpectedRevision, _ = s.RestoreRevision(ctx)
	if _, _, err := s.RestoreBackup(ctx, domain.NewID(), in); err != nil || !s.RestoreFrozen() {
		t.Fatal("pending ownership blocked atomic database restore", err)
	}
	if _, err := os.Stat(filepath.Join(root, "backups", string(in.Backup.ID)+".sqlite")); err != nil {
		t.Fatal(err)
	}
}

func TestBackupRestoreHonorsPermanentDeletionAndRetiresSafetyImages(t *testing.T) {
	s, root := openTest(t)
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	server := domain.NewID()
	if err := s.BindIdentity(ctx, server); err != nil {
		t.Fatal(err)
	}
	target, input, _ := deletionSession(t, s, "deleted")
	other, _, _ := deletionSession(t, s, "retained")
	backup, err := s.Backup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(filepath.Join(root, "backups", string(backup)+".sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	deletion, _, err := s.DeleteSession(ctx, domain.NewID(), target.ID, server, target.Revision)
	if err != nil {
		t.Fatal(err)
	}
	deletion, err = s.PurgeDeletedSession(ctx, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveSessionBackups(ctx, deletion); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CompleteSessionDeletion(ctx, target.ID); err != nil {
		t.Fatal(err)
	}
	// A stale same-server image supplied under a different managed UUID must
	// still obey the independently completed session obligation.
	stale := domain.NewID()
	source := filepath.Join(root, "backups", string(stale)+".sqlite")
	if err := os.WriteFile(source, original, 0600); err != nil {
		t.Fatal(err)
	}
	observed, err := s.InspectBackup(ctx, stale, server)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := s.RestoreRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	request := domain.NewID()
	in := BackupRestoreInput{Backup: observed.Backup, SHA256: observed.SHA256, ExpectedRevision: revision, ServerID: server, Actor: domain.Principal{Type: domain.OwnerDevice}}
	if _, _, err := s.RestoreBackup(ctx, request, in); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.RestoreSessionDeletionIntents(ctx, server); err != nil {
		t.Fatal(err)
	}
	for _, id := range []domain.ID{target.ID, input.ID} {
		var n int
		if err := reopened.db.QueryRow("SELECT COUNT(*) FROM entities WHERE id=?", id).Scan(&n); err != nil || n != 0 {
			t.Fatal("deleted content revived", id, n, err)
		}
	}
	if err := reopened.checkRestoreImagesRetired(ctx); err != nil {
		t.Fatal("temporary database copies survived recovery", err)
	}
	if got, err := os.ReadFile(source); err != nil || !bytes.Equal(got, original) {
		t.Fatal("restore changed source backup", err)
	}
	// The restored survivor can subsequently be erased without an unmanaged
	// safety image preventing completion or retaining its content.
	current, err := reopened.Get(ctx, domain.SessionKind, other.ID)
	if err != nil {
		t.Fatal(err)
	}
	next, _, err := reopened.DeleteSession(ctx, domain.NewID(), other.ID, server, current.Revision)
	if err != nil {
		t.Fatal(err)
	}
	next, err = reopened.PurgeDeletedSession(ctx, other.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.RemoveSessionBackups(ctx, next); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.CompleteSessionDeletion(ctx, other.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.GetBackupRestore(ctx, request); err != nil {
		t.Fatal("metadata receipt lost after erasure", err)
	}
}

func TestBackupRestoreUnacceptedImagesKeepDeletionPending(t *testing.T) {
	s, root, ctx, _, _ := restoreFixture(t)
	orphan := domain.NewID()
	dir := restoreDirectory(root, orphan)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "candidate.sqlite"), []byte("uncertain original staging"), 0600); err != nil {
		t.Fatal(err)
	}
	s.restoreReservations[orphan] = true
	if err := s.checkRestoreImagesRetired(ctx); err == nil {
		t.Fatal("unknown image skipped")
	}
}

func TestBackupRestoreRedactsDeletedSharedRemediation(t *testing.T) {
	s, root := openTest(t)
	ctx := notificationOwner()
	f := newRemediationStoreFixture(t, s)
	attempt, err := f.reserve(t, domain.PRRemediationManual)
	if err != nil {
		t.Fatal(err)
	}
	attempt = f.bind(t, attempt)
	bound, err := Decode[domain.PRRemediationAttempt](attempt)
	if err != nil {
		t.Fatal(err)
	}
	originalChain := f.chain(t)
	server := domain.NewID()
	if err := s.BindIdentity(ctx, server); err != nil {
		t.Fatal(err)
	}
	backup, err := s.Backup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(filepath.Join(root, "backups", string(backup)+".sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.Get(ctx, domain.SessionKind, bound.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	deletion, _, err := s.DeleteSession(ctx, domain.NewID(), session.ID, server, session.Revision)
	if err != nil {
		t.Fatal(err)
	}
	deletion, err = s.PurgeDeletedSession(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveSessionBackups(ctx, deletion); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CompleteSessionDeletion(ctx, session.ID); err != nil {
		t.Fatal(err)
	}
	stale := domain.NewID()
	if err := os.WriteFile(filepath.Join(root, "backups", string(stale)+".sqlite"), original, 0600); err != nil {
		t.Fatal(err)
	}
	observed, err := s.InspectBackup(ctx, stale, server)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := s.RestoreRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	in := BackupRestoreInput{Backup: observed.Backup, SHA256: observed.SHA256, ExpectedRevision: revision, ServerID: server, Actor: domain.Principal{Type: domain.OwnerDevice}}
	if _, _, err := s.RestoreBackup(ctx, domain.NewID(), in); err != nil {
		t.Fatal(err)
	}
	s.Close()
	reopened, err := Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.Read(ctx, func(tx *Tx) error {
		_, v, err := tx.GetPRRemediationAttempt(attempt.ID)
		if err != nil {
			return err
		}
		if v.SessionID != "" || v.InputID != "" || v.InputDigest != "" || v.ExecutionID != "" || v.State != domain.PRRemediationCanceled {
			t.Fatal("deleted operands restored", v)
		}
		_, set, err := tx.GetPRProblemSet(f.set.ID)
		if err != nil {
			return err
		}
		if set.Remediation.ActiveAttemptID != "" || set.Remediation.Sequence != originalChain.Sequence || set.Remediation.AutomaticAttempts != originalChain.AutomaticAttempts {
			t.Fatal("restored erasure refunded history", set.Remediation)
		}
		var count int
		err = tx.tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM entities WHERE kind='problem' AND json_extract(body,'$.type')='pull-request-activity' AND json_extract(body,'$.source_id')=?", attempt.ID).Scan(&count)
		if err != nil || count != 0 {
			t.Fatal("deleted activity restored", count, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
