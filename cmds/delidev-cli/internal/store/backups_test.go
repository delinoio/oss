package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestManagedBackupInspectionRejectsSidecarsAppearingAfterCopy(t *testing.T) {
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		t.Run(suffix, func(t *testing.T) {
			s, root, ctx, original := deletionFixture(t)
			path := filepath.Join(root, "backups", string(original.Backup.ID)+".sqlite")
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			observed := false
			result, err := s.inspectBackup(ctx, original.Backup.ID, original.ServerID, func() {
				observed = true
				// Reproduce adjacent state independently of the immutable main file,
				// so file-identity/hash checks alone cannot reject the observation.
				if err := os.WriteFile(path+suffix, []byte("raced SQLite state"), 0600); err != nil {
					t.Fatal(err)
				}
			})
			if !observed || err == nil || domain.SafeError(err).Code != domain.RecoveryRequired || result.SHA256 != "" {
				t.Fatal("raced sidecar was published as a valid inspection", result, err)
			}
			after, readErr := os.ReadFile(path)
			if readErr != nil || !bytes.Equal(before, after) {
				t.Fatal("inspection modified the original image", readErr)
			}
			if err := os.Remove(path + suffix); err != nil {
				t.Fatal(err)
			}
			if result, err := s.InspectBackup(ctx, original.Backup.ID, original.ServerID); err != nil || result.SHA256 != original.SHA256 {
				t.Fatal("preserved source did not recover after sidecar removal", result, err)
			}
		})
	}
}

func TestManagedBackupInspectionUsesCommittedImageWithoutChangingSource(t *testing.T) {
	s, root := openTest(t)
	ctx := context.Background()
	owner := domain.NewID()
	if err := s.BindIdentity(ctx, owner); err != nil {
		t.Fatal(err)
	}
	create(t, s, domain.NewID(), "retained WAL data")
	id, err := s.Backup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "backups", string(id)+".sqlite")
	before, _ := os.ReadFile(path)
	metadata, _ := os.Stat(path)
	list, err := s.BackupInventory(ctx)
	if err != nil || len(list) != 1 || list[0].ID != id || list[0].Bytes != uint64(len(before)) {
		t.Fatal(list, err)
	}
	value, err := s.InspectBackup(ctx, id, owner)
	want := sha256.Sum256(before)
	if err != nil || value.SHA256 != hex.EncodeToString(want[:]) || value.ServerID != owner || value.SchemaVersion != SchemaVersion {
		t.Fatal(value, err)
	}
	after, _ := os.ReadFile(path)
	current, _ := os.Stat(path)
	if !reflect.DeepEqual(before, after) || !sameBackup(metadata, current) {
		t.Fatal("inspection changed original image")
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatal("inspection left scratch or SQLite sidecars", entries)
	}
	// A later live mutation does not affect the original backup's hash.
	create(t, s, domain.NewID(), "later server state")
	second, err := s.InspectBackup(ctx, id, owner)
	if err != nil || second.SHA256 != value.SHA256 {
		t.Fatal(second, err)
	}
}

func TestManagedBackupInspectionRejectsForeignCorruptAndSidecarImages(t *testing.T) {
	for _, scenario := range []string{"foreign", "corrupt", "sidecar", "link", "canceled", "missing"} {
		t.Run(scenario, func(t *testing.T) {
			s, root := openTest(t)
			ctx := context.Background()
			owner := domain.NewID()
			if err := s.BindIdentity(ctx, owner); err != nil {
				t.Fatal(err)
			}
			id, err := s.Backup(ctx)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "backups", string(id)+".sqlite")
			switch scenario {
			case "foreign":
				owner = domain.NewID()
			case "corrupt":
				if err := os.WriteFile(path, []byte("not a database"), 0600); err != nil {
					t.Fatal(err)
				}
			case "sidecar":
				if err := os.WriteFile(path+"-wal", []byte("untrusted adjacent data"), 0600); err != nil {
					t.Fatal(err)
				}
			case "link":
				if err := os.Rename(path, path+".original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path+".original", path); err != nil {
					t.Skip("symlinks unavailable", err)
				}
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "missing":
				id = domain.NewID()
			}
			if _, err := s.InspectBackup(ctx, id, owner); err == nil {
				t.Fatal("unsafe inspection succeeded")
			}
			if _, err := os.Lstat(path); err != nil {
				t.Fatal("original removed", err)
			}
			entries, _ := os.ReadDir(filepath.Dir(path))
			for _, entry := range entries {
				if len(entry.Name()) >= 12 && entry.Name()[:12] == ".inspection-" {
					t.Fatal("private scratch leaked")
				}
			}
		})
	}
}

func TestManagedBackupInventoryIgnoresUnpublishedScratchAndSortsNewestFirst(t *testing.T) {
	s, root := openTest(t)
	ctx := context.Background()
	first, err := s.Backup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Backup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "backups", string(domain.NewID())+".sqlite.pending"), []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}
	items, err := s.BackupInventory(ctx)
	if err != nil || len(items) != 2 || items[0].ID != max(first, second) || items[1].ID != min(first, second) {
		t.Fatal(items, err)
	}
	if _, err := s.InspectBackup(ctx, "../state", domain.NewID()); err == nil {
		t.Fatal("accepted path instead of ID")
	}
}
