// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func backupScanFixture(t *testing.T, count int) (*Store, string, SessionDeletion, []domain.ID) {
	t.Helper()
	s, root := openTest(t)
	ctx := notificationOwner()
	server := domain.NewID()
	if err := s.BindIdentity(ctx, server); err != nil {
		t.Fatal(err)
	}
	target, _, _ := deletionSession(t, s, "target")
	v, _, err := s.DeleteSession(ctx, domain.NewID(), target.ID, server, target.Revision)
	if err != nil {
		t.Fatal(err)
	}
	v, err = s.PurgeDeletedSession(ctx, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	ids := []domain.ID{}
	for range count {
		id, err := s.Backup(ctx)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return s, root, v, ids
}
func TestSessionBackupScanBoundedPassesPersistAcrossRestart(t *testing.T) {
	s, root, v, ids := backupScanFixture(t, 3)
	copies := 0
	for pass := 0; pass < 3; pass++ {
		ctx, cancel := context.WithCancel(context.Background())
		inPass := 0
		err := s.removeSessionBackups(ctx, v, func() {
			copies++
			inPass++
			if inPass == 2 {
				cancel()
			}
		})
		cancel()
		if pass < 2 && err == nil || pass == 2 && err != nil {
			t.Fatalf("pass %d did not advance bounded classification: %v", pass, err)
		}
		scan, e := s.readSessionBackupScan(v)
		if e != nil || len(scan.Clean) != pass+1 {
			t.Fatal("completed facts lost or unseen image marked clean", e)
		}
		if pass == 0 {
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			s, e = Open(context.Background(), root)
			if e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() { s.Close() })
			items, e := s.SessionDeletions(context.Background())
			if e != nil || len(items) != 1 || items[0].ID != v.ID {
				t.Fatal("checkpoint changed original journal inventory", e)
			}
		}
	}
	if copies != 5 {
		t.Fatal("successful images were inspected again", copies)
	}
	for _, id := range ids {
		if _, err := os.Stat(filepath.Join(root, "backups", string(id)+".sqlite")); err != nil {
			t.Fatal("unrelated clean image deleted", err)
		}
	}
	done, err := s.CompleteSessionDeletion(context.Background(), v.SessionID)
	if err != nil || done.FinishedAt == nil {
		t.Fatal("finite inventory never completed", err)
	}
}
func TestSessionBackupScanRevalidatesOneChangedImageAndLaterInventory(t *testing.T) {
	s, root, v, ids := backupScanFixture(t, 2)
	if err := s.RemoveSessionBackups(context.Background(), v); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "backups", string(ids[0])+".sqlite")
	before, err := backupInfo(path)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Same inode/length/visible mtime, but a new native change generation.
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, before.ModTime(), before.ModTime()); err != nil {
		t.Fatal(err)
	}
	later := domain.NewID()
	laterPath := filepath.Join(root, "backups", string(later)+".sqlite")
	copies := 0
	err = s.removeSessionBackups(context.Background(), v, func() {
		copies++
		if err := os.WriteFile(laterPath, data, 0600); err != nil {
			t.Fatal(err)
		}
	})
	if err == nil || copies != 1 {
		t.Fatal("changed image was reused or new inventory ignored", copies, err)
	}
	scan, err := s.readSessionBackupScan(v)
	if err != nil || len(scan.Clean) != 2 {
		t.Fatal("unrelated classification invalidated", err)
	}
	copies = 0
	if err := s.removeSessionBackups(context.Background(), v, func() { copies++ }); err != nil || copies != 1 {
		t.Fatal("later image not classified independently", copies, err)
	}
	scan, err = s.readSessionBackupScan(v)
	if err != nil || len(scan.Clean) != 3 {
		t.Fatal("later inventory checkpoint missing", err)
	}
}
func TestSessionBackupScanReplacementStillUsesDurableBackupDeletion(t *testing.T) {
	s, root := openTest(t)
	ctx := notificationOwner()
	server := domain.NewID()
	if err := s.BindIdentity(ctx, server); err != nil {
		t.Fatal(err)
	}
	target, _, _ := deletionSession(t, s, "target")
	old, err := s.Backup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	oldBytes, err := os.ReadFile(filepath.Join(root, "backups", string(old)+".sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	v, _, err := s.DeleteSession(ctx, domain.NewID(), target.ID, server, target.Revision)
	if err != nil {
		t.Fatal(err)
	}
	v, err = s.PurgeDeletedSession(ctx, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveSessionBackups(ctx, v); err != nil {
		t.Fatal(err)
	}
	clean, err := s.Backup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	unrelated, err := s.Backup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveSessionBackups(ctx, v); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "backups", string(clean)+".sqlite")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, oldBytes, 0600); err != nil {
		t.Fatal(err)
	}
	copies := 0
	if err := s.removeSessionBackups(ctx, v, func() { copies++ }); err != nil || copies != 1 {
		t.Fatal("replacement did not invalidate only its clean fact", copies, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("target-containing replacement survived", err)
	}
	intent, err := s.readDeletionIntent(clean)
	if err != nil || intent.Input.Backup.ID != clean || intent.Input.ServerID != v.ServerID {
		t.Fatal("target removed without original durable backup intent", err)
	}
	if _, err := os.Stat(filepath.Join(root, "backups", string(unrelated)+".sqlite")); err != nil {
		t.Fatal("unrelated backup deleted", err)
	}
}
func TestSessionBackupScanCancellationPreservesUnknownScratch(t *testing.T) {
	s, root, v, _ := backupScanFixture(t, 2)
	ctx, cancel := context.WithCancel(context.Background())
	scratch := filepath.Join(root, "backups", ".inspection-unconfirmed.tmp")
	copies := 0
	err := s.removeSessionBackups(ctx, v, func() {
		copies++
		if copies == 2 {
			if err := os.WriteFile(scratch, []byte("private fixture bytes"), 0600); err != nil {
				t.Fatal(err)
			}
			cancel()
		}
	})
	cancel()
	scan, e := s.readSessionBackupScan(v)
	if err == nil || e != nil || len(scan.Clean) != 1 {
		t.Fatal("canceled copy marked unseen image clean", err, e)
	}
	if err := s.RemoveSessionBackups(context.Background(), v); err == nil {
		t.Fatal("unknown scratch cleanup treated as complete")
	}
	if _, err := os.Stat(scratch); err != nil {
		t.Fatal("unknown scratch unlinked without original proof", err)
	}
	if err := os.Remove(scratch); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveSessionBackups(context.Background(), v); err != nil {
		t.Fatal("independently recovered scratch did not permit original scan", err)
	}
}
func TestSessionBackupScanBoundsAndOriginalIntentBinding(t *testing.T) {
	s, _, v, _ := backupScanFixture(t, 1)
	if err := s.RemoveSessionBackups(context.Background(), v); err != nil {
		t.Fatal(err)
	}
	scan, err := s.readSessionBackupScan(v)
	if err != nil {
		t.Fatal(err)
	}
	scan.DeletionID = domain.NewID()
	raw, _ := json.Marshal(scan)
	if err := os.WriteFile(s.sessionBackupScanPath(v.SessionID), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveSessionBackups(context.Background(), v); err == nil {
		t.Fatal("checkpoint borrowed another deletion intent")
	}
	if _, err := s.SessionDeletions(context.Background()); err == nil {
		t.Fatal("tampered checkpoint silently skipped at recovery")
	}
	scan.DeletionID = v.ID
	scan.Clean = nil
	for range MaxBackupInventory {
		scan.Clean = append(scan.Clean, sessionBackupClassification{Backup: Backup{ID: domain.NewID(), Bytes: uint64(MaxBackupInspectionBytes), ModifiedAt: time.Now().UTC()}, Mode: 0600, NativeIdentity: strings.Repeat("a", 256), SHA256: strings.Repeat("0", 64), SchemaVersion: ^uint32(0)})
	}
	raw, err = json.Marshal(scan)
	if err != nil || len(raw) > maxSessionBackupScanBytes || scan.validate(v) != nil {
		t.Fatal("complete bounded inventory cannot persist", len(raw), err)
	}
	scan.Clean = append(scan.Clean, scan.Clean[0])
	if scan.validate(v) == nil {
		t.Fatal("over-bound inventory admitted")
	}
}

func TestSessionBackupScanFinalAcknowledgementRejectsLatePublication(t *testing.T) {
	s, _, v, _ := backupScanFixture(t, 1)
	ctx := context.Background()
	if err := s.RemoveSessionBackups(ctx, v); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Backup(ctx); err != nil {
		t.Fatal(err)
	}
	if done, err := s.CompleteSessionDeletion(ctx, v.SessionID); err == nil || done.FinishedAt != nil {
		t.Fatal("late image inherited the earlier complete scan")
	}
	if err := s.RemoveSessionBackups(ctx, v); err != nil {
		t.Fatal(err)
	}
	if done, err := s.CompleteSessionDeletion(ctx, v.SessionID); err != nil || done.FinishedAt == nil {
		t.Fatal("reclassified late image did not permit completion", err)
	}
}
