// SPDX-License-Identifier: Apache-2.0
package process

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func TestRetireCompletedOwnerRemovesReleasedNativeJournals(t *testing.T) {
	c := config(t, "streams")
	h, err := Start(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.Lstat(filepath.Join(c.Directory, string(c.OwnerID)))
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Resume(); err != nil {
		t.Fatal(err)
	}
	if err := h.CloseInput(); err != nil {
		t.Fatal(err)
	}
	_ = h.Wait()
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	if err := RetireCompletedOwnerContext(context.Background(), c.Directory, c.OwnerID, original); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(c.Directory, string(c.OwnerID)), filepath.Join(c.Directory, string(c.OwnerID)+".recovery.lock")} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatal("owner retained", err)
		}
	}
}

func TestRetireCompletedOwnerFailureDoesNotClaimCleanup(t *testing.T) {
	for _, scenario := range []string{"remove", "sync", "foreign-journal", "held-controller", "canceled"} {
		t.Run(scenario, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "processes")
			owner := domain.NewID()
			path := filepath.Join(root, string(owner))
			if err := security.PrivateDir(path); err != nil {
				t.Fatal(err)
			}
			original, _ := os.Lstat(path)
			remove := os.Remove
			syncParent := security.SyncParent
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch scenario {
			case "remove":
				remove = func(string) error { return errors.New("injected removal") }
			case "sync":
				syncParent = func(string) error { return errors.New("injected sync") }
			case "foreign-journal":
				if err := os.WriteFile(filepath.Join(path, "foreign"), []byte("retain"), 0600); err != nil {
					t.Fatal(err)
				}
			case "held-controller":
				lock, err := security.TryLock(path + ".recovery.lock")
				if err != nil {
					t.Fatal(err)
				}
				defer lock.Close()
			case "canceled":
				cancel()
			}
			err := retireCompletedOwnerContext(ctx, root, owner, original, remove, syncParent)
			if err == nil {
				t.Fatal("cleanup failure accepted")
			}
			if scenario != "sync" {
				if _, err := os.Lstat(path); err != nil {
					t.Fatal("unconfirmed owner removed", err)
				}
			} else {
				if _, err := os.Lstat(path + ".recovery.lock"); err != nil {
					t.Fatal("sync failure lost remaining evidence", err)
				}
			}
		})
	}
}
