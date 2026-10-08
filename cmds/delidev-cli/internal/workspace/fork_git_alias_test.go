// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestForkRejectsNativeGitAliases(t *testing.T) {
	for _, nested := range []bool{false, true} {
		for _, pointer := range []bool{false, true} {
			name := "directory"
			if pointer {
				name = "pointer"
			}
			if nested {
				name = "nested-" + name
			}
			t.Run(name, func(t *testing.T) {
				source, _ := filepath.EvalSymlinks(t.TempDir())
				parent := source
				if nested {
					parent = filepath.Join(source, "nested")
					if err := os.Mkdir(parent, 0700); err != nil {
						t.Fatal(err)
					}
				}
				external := repository(t)
				admin, err := filepath.EvalSymlinks(filepath.Join(external, ".git"))
				if err != nil {
					t.Fatal(err)
				}
				before, err := scanForkTree(context.Background(), admin, "", false)
				if err != nil {
					t.Fatal(err)
				}
				alias := filepath.Join(parent, ".GIT")
				if pointer {
					if err := os.WriteFile(alias, []byte("gitdir: "+admin+"\n"), 0600); err != nil {
						t.Fatal(err)
					}
				} else {
					gitTest(t, parent, "init", "-b", "main")
					if err := os.Rename(filepath.Join(parent, ".git"), alias); err != nil {
						t.Fatal(err)
					}
				}
				marker, err := os.Lstat(filepath.Join(parent, ".git"))
				if os.IsNotExist(err) {
					t.Skip("native filesystem distinguishes .GIT from .git")
				}
				actual, err2 := os.Lstat(alias)
				if err != nil || err2 != nil || !os.SameFile(marker, actual) {
					t.Fatal("native alias fixture invalid", err, err2)
				}
				gitTest(t, parent, "rev-parse", "--absolute-git-dir")
				for _, copying := range []bool{false, true} {
					target := ""
					if copying {
						target, _ = filepath.EvalSymlinks(t.TempDir())
					}
					// A nested marker remains unsupported even in a declared repository.
					_, err := scanForkTree(context.Background(), source, target, nested)
					if domain.SafeError(err).Code != domain.Unsupported {
						t.Fatal("native administration accepted", err)
					}
				}
				after, err := scanForkTree(context.Background(), admin, "", false)
				if err != nil || before != after {
					t.Fatal("external administration changed", err)
				}
				if _, err := os.Lstat(alias); err != nil {
					t.Fatal("source marker changed", err)
				}
			})
		}
	}
}

func TestForkDistinctGitCaseNameRemainsContent(t *testing.T) {
	source, _ := filepath.EvalSymlinks(t.TempDir())
	if err := os.WriteFile(filepath.Join(source, ".GIT"), []byte("ordinary hidden content"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(source, ".git")); !os.IsNotExist(err) {
		t.Skip("native filesystem aliases .GIT to .git")
	}
	target, _ := filepath.EvalSymlinks(t.TempDir())
	copy, err := copyForkTree(context.Background(), source, target, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := copy.verify(context.Background(), Git{}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(target, ".GIT"))
	if err != nil || string(raw) != "ordinary hidden content" {
		t.Fatal("distinct content omitted", err)
	}
}

func TestForkGitMarkerIdentityChangesFailClosed(t *testing.T) {
	for _, scenario := range []string{"appears", "removed", "replaced", "edited"} {
		t.Run(scenario, func(t *testing.T) {
			source := t.TempDir()
			path := filepath.Join(source, ".git")
			if scenario != "appears" {
				if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			root, err := os.OpenRoot(source)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			marker, err := inspectForkGitMarker(root, ".")
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "appears":
				err = os.WriteFile(path, []byte("new"), 0600)
			case "removed":
				err = os.Remove(path)
			case "replaced":
				err = os.Rename(path, filepath.Join(source, "original"))
				if err == nil {
					err = os.WriteFile(path, []byte("replacement"), 0600)
				}
			case "edited":
				err = os.WriteFile(path, []byte("changed original"), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if domain.SafeError(marker.verify(root, ".")).Code != domain.Conflict {
				t.Fatal("marker drift accepted")
			}
			if _, err := marker.matches(root, ".", ".git"); domain.SafeError(err).Code != domain.Conflict {
				t.Fatal("classification accepted changed marker", err)
			}
		})
	}
}

func TestGeneralChatGitAliasRejectsBeforePublication(t *testing.T) {
	for _, pointer := range []bool{false, true} {
		t.Run(map[bool]string{false: "directory", true: "pointer"}[pointer], func(t *testing.T) {
			m := manager(t)
			source, err := m.Prepare(context.Background(), PrepareRequest{SessionID: domain.NewID(), MachineID: domain.NewID(), Type: domain.GeneralChat})
			if err != nil {
				t.Fatal(err)
			}
			if pointer {
				external := repository(t)
				admin, err := filepath.EvalSymlinks(filepath.Join(external, ".git"))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(source.PrimaryPath, ".GIT"), []byte("gitdir: "+admin+"\n"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				gitTest(t, source.PrimaryPath, "init", "-b", "main")
				if err := os.Rename(filepath.Join(source.PrimaryPath, ".git"), filepath.Join(source.PrimaryPath, ".GIT")); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := os.Lstat(filepath.Join(source.PrimaryPath, ".git")); os.IsNotExist(err) {
				t.Skip("native filesystem distinguishes case")
			}
			childID := domain.NewID()
			request, err := m.ForkPreparation(context.Background(), source, childID, domain.GeneralChat)
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := m.InspectForkSnapshot(context.Background(), source, request)
			if domain.SafeError(err).Code != domain.Unsupported || snapshot != nil {
				t.Fatal("alias inspection admitted child", err)
			}
			if child, err := m.Read(childID); err == nil && child.State == Ready {
				t.Fatal("rejected child was published")
			}
		})
	}
}

type forkMarkerMutationContext struct {
	context.Context
	mutate func()
	calls  int
}

func (c *forkMarkerMutationContext) Err() error {
	c.calls++
	if c.calls == 2 {
		c.mutate()
	}
	return c.Context.Err()
}

func TestForkScannerDetectsExcludedMarkerReplacement(t *testing.T) {
	for _, copying := range []bool{false, true} {
		t.Run(map[bool]string{false: "inspection", true: "copy"}[copying], func(t *testing.T) {
			source, _ := filepath.EvalSymlinks(t.TempDir())
			marker := filepath.Join(source, ".git")
			for path, value := range map[string]string{marker: "original marker", filepath.Join(source, "file"): "data"} {
				if err := os.WriteFile(path, []byte(value), 0600); err != nil {
					t.Fatal(err)
				}
			}
			ctx := &forkMarkerMutationContext{Context: context.Background(), mutate: func() {
				if err := os.Rename(marker, filepath.Join(source, "old-marker")); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(marker, []byte("replacement"), 0600); err != nil {
					t.Fatal(err)
				}
			}}
			target := ""
			if copying {
				target, _ = filepath.EvalSymlinks(t.TempDir())
			}
			if _, err := scanForkTree(ctx, source, target, true); domain.SafeError(err).Code != domain.Conflict {
				t.Fatal("changed excluded marker accepted", err)
			}
		})
	}
}

func TestForkDeclaredGitAliasIsExcluded(t *testing.T) {
	source, _ := filepath.EvalSymlinks(t.TempDir())
	gitTest(t, source, "init", "-b", "main")
	if err := os.Rename(filepath.Join(source, ".git"), filepath.Join(source, ".GIT")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(source, ".git")); os.IsNotExist(err) {
		t.Skip("native filesystem distinguishes case")
	}
	if err := os.WriteFile(filepath.Join(source, "file"), []byte("source content"), 0600); err != nil {
		t.Fatal(err)
	}
	target, _ := filepath.EvalSymlinks(t.TempDir())
	if _, err := copyForkTree(context.Background(), source, target, true); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".git", ".GIT"} {
		if _, err := os.Lstat(filepath.Join(target, name)); !os.IsNotExist(err) {
			t.Fatal("source administration copied", err)
		}
	}
	if raw, err := os.ReadFile(filepath.Join(target, "file")); err != nil || string(raw) != "source content" {
		t.Fatal("source content lost", err)
	}
}

func TestForkDistinctGitCaseNameBesideAdministration(t *testing.T) {
	source, _ := filepath.EvalSymlinks(t.TempDir())
	if err := os.Mkdir(filepath.Join(source, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, ".GIT"), []byte("distinct"), 0600); err != nil {
		if info, e := os.Stat(filepath.Join(source, ".GIT")); e == nil && info.IsDir() {
			t.Skip("native filesystem aliases .GIT to .git")
		}
		t.Fatal(err)
	}
	target, _ := filepath.EvalSymlinks(t.TempDir())
	if _, err := copyForkTree(context.Background(), source, target, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(target, ".git")); !os.IsNotExist(err) {
		t.Fatal("administration copied", err)
	}
	if raw, err := os.ReadFile(filepath.Join(target, ".GIT")); err != nil || string(raw) != "distinct" {
		t.Fatal("distinct case name omitted", err)
	}
}

func TestForkFinalVerificationRejectsLateGitAlias(t *testing.T) {
	for _, side := range []string{"source", "child"} {
		t.Run(side, func(t *testing.T) {
			source, _ := filepath.EvalSymlinks(t.TempDir())
			target, _ := filepath.EvalSymlinks(t.TempDir())
			copy, err := copyForkTree(context.Background(), source, target, false)
			if err != nil {
				t.Fatal(err)
			}
			parent := source
			if side == "child" {
				parent = target
			}
			if err := os.WriteFile(filepath.Join(parent, ".GIT"), []byte("gitdir: /not-opened\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(filepath.Join(parent, ".git")); os.IsNotExist(err) {
				t.Skip("native filesystem distinguishes case")
			}
			if domain.SafeError(copy.verify(context.Background(), Git{})).Code != domain.Unsupported {
				t.Fatal("late native administration accepted")
			}
		})
	}
}
