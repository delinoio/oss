package connections

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
)

func retainedRemoval(t *testing.T) (string, record, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "desktop")
	if err := security.PrivateDir(root); err != nil {
		t.Fatal(err)
	}
	id := domain.NewID()
	client := filepath.Join(profileRoot(root, id), "client")
	if err := security.PrivateDir(client); err != nil {
		t.Fatal(err)
	}
	lock, err := security.TryLock(filepath.Join(root, "connections.lock"))
	if err != nil {
		t.Fatal(err)
	}
	lock.Close()
	code, _ := worker.RandomToken()
	token, _ := worker.RandomToken()
	value := record{Version: 1, ID: id, Name: "Private cleanup", Grant: worker.PairingCode{Version: 1, Endpoint: "http://127.0.0.1:43210", ServerID: domain.NewID(), PairingID: domain.NewID(), Code: code}, DeviceID: domain.NewID(), CreatedAt: time.Now().UTC()}
	credential := worker.Credential{Version: 1, Type: domain.ClientDevice, Endpoint: value.Grant.Endpoint, ServerID: value.Grant.ServerID, PairingID: value.Grant.PairingID, DeviceID: value.DeviceID, Token: token}
	raw, _ := json.Marshal(credential)
	path := filepath.Join(client, "device.json")
	if err := security.WriteAtomic(path, raw); err != nil {
		t.Fatal(err)
	}
	digest, err := fileDigest(path)
	if err != nil {
		t.Fatal(err)
	}
	value.Removal = &removalReceipt{RequestID: domain.NewID(), ExpectedRevision: 1, StartedAt: time.Now().UTC(), CredentialDigest: digest}
	value.Grant.Code = ""
	if err := writeRecord(root, value); err != nil {
		t.Fatal(err)
	}
	return root, value, path
}
func TestRemovalResumesOriginalCleanupAfterMarkerAndPartialDeletion(t *testing.T) {
	for _, unlinked := range []bool{false, true} {
		t.Run(map[bool]string{false: "marker-only", true: "credential-unlinked"}[unlinked], func(t *testing.T) {
			root, value, path := retainedRemoval(t)
			if unlinked {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			before, err := Inspect(root, value.ID)
			if err != nil || before.State != Removing {
				t.Fatal(before, err)
			}
			if _, err := Rename(context.Background(), root, value.ID, domain.NewID(), before.Revision, "No repair"); err == nil {
				t.Fatal("removing client renamed")
			}
			if _, err := Remove(context.Background(), root, value.ID, domain.NewID(), value.Removal.ExpectedRevision); err == nil {
				t.Fatal("cleanup request replaced")
			}
			after, err := Remove(context.Background(), root, value.ID, value.Removal.RequestID, value.Removal.ExpectedRevision)
			if err != nil || after.State != Removed {
				t.Fatal(after, err)
			}
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatal("original secret not deleted", err)
			}
		})
	}
}
func TestRemovalPreservesReplacedOrLinkedEvidence(t *testing.T) {
	for _, linked := range []bool{false, true} {
		t.Run(map[bool]string{false: "replacement", true: "symlink"}[linked], func(t *testing.T) {
			root, value, path := retainedRemoval(t)
			if linked {
				outside := filepath.Join(t.TempDir(), "untouched")
				if err := os.WriteFile(outside, []byte("foreign"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, path); err != nil {
					t.Skip("symlink unavailable")
				}
			} else if err := security.WriteAtomic(path, []byte("foreign")); err != nil {
				t.Fatal(err)
			}
			if _, err := Remove(context.Background(), root, value.ID, value.Removal.RequestID, value.Removal.ExpectedRevision); err == nil {
				t.Fatal("foreign evidence deleted")
			}
			if raw, err := os.ReadFile(path); err != nil || string(raw) != "foreign" {
				t.Fatal("foreign data changed", err)
			}
			if got, err := Inspect(root, value.ID); err != nil || got.State != Removing {
				t.Fatal("uncertain cleanup marked complete", err)
			}
		})
	}
}

func TestRemovedPaginationAndActiveCapacityKeepOriginalDirectories(t *testing.T) {
	root := filepath.Join(t.TempDir(), "desktop")
	if err := security.PrivateDir(root); err != nil {
		t.Fatal(err)
	}
	code, _ := worker.RandomToken()
	create := func(complete bool) domain.ID {
		t.Helper()
		id := domain.NewID()
		if err := security.PrivateDir(profileRoot(root, id)); err != nil {
			t.Fatal(err)
		}
		value := record{Version: 1, ID: id, Name: "Retained history", Grant: worker.PairingCode{Version: 1, Endpoint: "http://127.0.0.1:43210", ServerID: domain.NewID(), PairingID: domain.NewID(), Code: code}, CreatedAt: time.Now().UTC()}
		if complete {
			value.Grant.Code = ""
			value.Removal = &removalReceipt{RequestID: domain.NewID(), ExpectedRevision: 1, StartedAt: time.Now().UTC(), Complete: true}
		}
		if err := writeRecord(root, value); err != nil {
			t.Fatal(err)
		}
		return id
	}
	for range MaxProfiles {
		create(false)
	}
	for range 17 {
		create(true)
	}
	if values, err := List(root); err != nil || len(values) != MaxProfiles {
		t.Fatal("removed history consumed active slots", err)
	}
	first, err := ListRemoved(root, "")
	if err != nil || len(first.Connections) != 16 || first.NextAfter == "" {
		t.Fatal(first, err)
	}
	second, err := ListRemoved(root, first.NextAfter)
	if err != nil || len(second.Connections) != 1 || second.NextAfter != "" || second.Connections[0].ID <= first.NextAfter {
		t.Fatal(second, err)
	}
	if _, err := ListRemoved(root, "../escape"); err == nil {
		t.Fatal("invalid removed cursor accepted")
	}
	all, err := records(root)
	if err != nil || len(all) != MaxProfiles+17 {
		t.Fatal("removed scope was moved or erased", err)
	}
	for len(all) < MaxRetainedProfiles+1 {
		create(true)
		all = append(all, record{})
	}
	if _, err := List(root); err == nil {
		t.Fatal("unbounded retained inventory accepted")
	}
}
