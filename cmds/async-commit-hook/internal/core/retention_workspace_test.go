package core

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestPruneCountsLeftoverWorkspaces(t *testing.T) {
	for _, expired := range []bool{false, true} {
		for _, evidence := range []bool{false, true} {
			name := "terminal"
			if expired {
				name = "expired"
			}
			if evidence {
				name += "-with-evidence"
			}
			t.Run(name, func(t *testing.T) {
				s, repo := fixture(t, "version=1\n")
				r := runFixture(t, s, repo)
				if expired {
					r.State = Expired
					if err := s.Store.SaveRun(r); err != nil {
						t.Fatal(err)
					}
				}
				workspace := filepath.Join(s.Store.Root, "workspaces", r.ID)
				if err := os.MkdirAll(filepath.Join(workspace, "nested"), 0700); err != nil {
					t.Fatal(err)
				}
				data := bytes.Repeat([]byte("w"), 4096)
				path := filepath.Join(workspace, "nested", "leftover")
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
				expected := int64(len(data))
				if evidence {
					if _, err := s.SaveEvidence(r.ID, "retained", []byte("log")); err != nil {
						t.Fatal(err)
					}
					expected += 3
				}
				before := Encode(r)
				quota := int64(2048) // Evidence alone never crosses the quota.
				if expired {
					quota = expected + 1 // Tombstones retry even below the quota.
				}
				for _, dry := range []bool{true, false} {
					result, err := s.Prune(dry, 0, quota)
					if err != nil || len(result.RunIDs) != 1 || result.RunIDs[0] != r.ID || result.Bytes != expected || len(result.Diagnostics) != 0 {
						t.Fatalf("prune %+v: %v", result, err)
					}
					actual, err := s.Store.Run(r.ID)
					if err != nil {
						t.Fatal(err)
					}
					if dry {
						if !bytes.Equal(before, Encode(actual)) {
							t.Fatal("dry-run changed metadata")
						}
						if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, data) {
							t.Fatal("dry-run changed workspace", err)
						}
					} else if actual.State != Expired {
						t.Fatal("missing terminal tombstone", actual.State)
					}
				}
				for _, dir := range []string{"evidence", "workspaces"} {
					if _, err := os.Lstat(filepath.Join(s.Store.Root, dir, r.ID)); !os.IsNotExist(err) {
						t.Fatal("owned files remain", err)
					}
				}
				for _, dry := range []bool{true, false} {
					result, err := s.Prune(dry, 0, quota)
					if err != nil || len(result.RunIDs) != 0 || result.Bytes != 0 {
						t.Fatal("completed cleanup was selected again", result, err)
					}
				}
			})
		}
	}
}

func TestPruneProtectsActiveWorkspaceAndInheritedEvidence(t *testing.T) {
	s, repo := fixture(t, "version=1\n[checks.test]\ncommand=\"echo test\"\n")
	oldReceipt, err := s.Submit(context.Background(), repo, "", false)
	if err != nil {
		t.Fatal(err)
	}
	old, err := s.Store.Run(oldReceipt.RunID)
	if err != nil {
		t.Fatal(err)
	}
	old.State = Failed
	if err = s.Store.SaveRun(old); err != nil {
		t.Fatal(err)
	}
	receipt, err := s.Submit(context.Background(), repo, "", false)
	if err != nil {
		t.Fatal(err)
	}
	active, err := s.Store.Run(receipt.RunID)
	if err != nil {
		t.Fatal(err)
	}
	check := active.Checks[0]
	check.InheritedFrom = old.ID
	if err = s.Store.SaveCheck(check); err != nil {
		t.Fatal(err)
	}
	for _, r := range []Run{old, active} {
		if _, err = s.SaveEvidence(r.ID, "protected", []byte("evidence")); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(s.Store.Root, "workspaces", r.ID)
		if err = os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(path, "source"), []byte("protected workspace"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, dry := range []bool{true, false} {
		result, err := s.Prune(dry, 0, 1)
		if err != nil || len(result.RunIDs) != 0 || result.Bytes != 0 {
			t.Fatal("active/inherited data selected", result, err)
		}
	}
	for _, r := range []Run{old, active} {
		if _, err = os.Stat(filepath.Join(s.Store.Root, "workspaces", r.ID, "source")); err != nil {
			t.Fatal("protected workspace removed", err)
		}
	}
}
