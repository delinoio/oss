// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestSidechatUnpublishedMetadataRollbackPreservesParentAndForeignRoots(t *testing.T) {
	for _, mode := range []string{"before-publication", "after-publication", "parent-change", "foreign-metadata"} {
		t.Run(mode, func(t *testing.T) {
			m, prepare, parent := chatExecutionFixture(t)
			child := domain.NewID()
			root := filepath.Join(m.Root, "workspaces", string(child))
			keep := filepath.Join(parent.PrimaryPath, "keep")
			if err := os.WriteFile(keep, []byte("parent-owned"), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "before-publication" {
				m.sidechatBeforeMetadataPublish = cancel
			}
			m.sidechatAfterMetadataPublish = func() {
				switch mode {
				case "after-publication":
					cancel()
				case "parent-change":
					if err := os.WriteFile(keep, []byte("parent-edited"), 0600); err != nil {
						t.Fatal(err)
					}
					if err := os.Remove(filepath.Join(m.Root, "workspaces", string(parent.SessionID), "manifest.json")); err != nil {
						t.Fatal(err)
					}
				case "foreign-metadata":
					if err := os.Rename(root, root+"-retained"); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(root, 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(root, "foreign"), []byte("preserve"), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, _, err := m.PrepareSidechatReference(ctx, child, prepare, parent); err == nil {
				t.Fatal("failed unpublished preparation reported success")
			}
			if mode == "foreign-metadata" {
				if raw, err := os.ReadFile(filepath.Join(root, "foreign")); err != nil || string(raw) != "preserve" {
					t.Fatal("foreign metadata was adopted", err)
				}
			} else if _, err := os.Lstat(root); !os.IsNotExist(err) {
				t.Fatal("failed reference blocks parent cleanup", err)
			}
			expected := "parent-owned"
			if mode == "parent-change" {
				expected = "parent-edited"
			}
			if raw, err := os.ReadFile(keep); err != nil || string(raw) != expected {
				t.Fatal("reference rollback changed parent files", err)
			}
		})
	}
}

func TestJoinedFailedForkDiscardsOnlyOriginalSidechatReference(t *testing.T) {
	m, prepare, parent := chatExecutionFixture(t)
	input, child, err := m.PrepareSidechatReference(context.Background(), domain.NewID(), prepare, parent)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.requireNoSidechatReferences(context.Background(), parent.SessionID); err == nil {
		t.Fatal("reference did not retain dependent ownership")
	}
	// A new owner receives the original bound result after the failed native fork
	// joins. It removes metadata without replaying preparation or touching sources.
	m = &Manager{Root: m.Root, Logger: m.Logger}
	if err := m.DiscardUnpublishedSidechatReference(context.Background(), input, child); err != nil {
		t.Fatal(err)
	}
	if err := m.requireNoSidechatReferences(context.Background(), parent.SessionID); err != nil {
		t.Fatal("original unpublished reference still blocks parent", err)
	}
	if _, err := os.Stat(parent.PrimaryPath); err != nil {
		t.Fatal("rollback removed referenced source", err)
	}
	if err := m.DiscardUnpublishedSidechatReference(context.Background(), input, child); err != nil {
		t.Fatal("rollback replay changed original scope", err)
	}
}
