package grok

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func historyFileFixture(t *testing.T, files map[string][]byte) string {
	t.Helper()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(parent, "grok")
	if err := security.PrivateDir(home); err != nil {
		t.Fatal(err)
	}
	for name, data := range files {
		path := filepath.Join(home, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func TestRetainedHistoryReaderRejectsUnsafeScopesWithoutRepair(t *testing.T) {
	for _, name := range []string{"missing-home", "home-file", "home-link", "parent-link", "file-link", "directory-link", "hard-link", "missing-file", "directory-file", "shared-root", "shared-parent", "shared-file", "too-large", "empty", "relative-home", "unclean-home", "canceled"} {
		t.Run(name, func(t *testing.T) {
			if runtime.GOOS == "windows" && (strings.Contains(name, "link") || strings.HasPrefix(name, "shared-")) {
				t.Skip("Unix mode/link fixture; Windows ACLs are independently checked")
			}
			session := domain.NewID()
			relative := filepath.Join("sessions", "fixture", string(session), "updates.jsonl")
			home := historyFileFixture(t, map[string][]byte{relative: []byte("original")})
			file := filepath.Join(home, relative)
			ctx := context.Background()
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			switch name {
			case "missing-home":
				home = filepath.Join(home, "missing", "grok")
			case "home-file":
				must(os.Rename(home, home+"-retained"))
				must(os.WriteFile(home, []byte("not a directory"), 0600))
			case "home-link":
				must(os.Rename(home, home+"-retained"))
				must(os.Symlink(home+"-retained", home))
			case "parent-link":
				alias := filepath.Join(filepath.Dir(home), "alias")
				must(os.Symlink(filepath.Dir(home), alias))
				home = filepath.Join(alias, "grok")
			case "file-link":
				must(os.Rename(file, file+".retained"))
				must(os.Symlink(file+".retained", file))
			case "directory-link":
				path := filepath.Dir(file)
				must(os.Rename(path, path+"-retained"))
				must(os.Symlink(path+"-retained", path))
			case "hard-link":
				must(os.Link(file, filepath.Join(filepath.Dir(home), "reachable-copy")))
			case "missing-file":
				must(os.Remove(file))
			case "directory-file":
				must(os.Remove(file))
				must(os.Mkdir(file, 0700))
			case "shared-root":
				must(os.Chmod(home, 0755))
			case "shared-parent":
				must(os.Chmod(filepath.Dir(file), 0777))
			case "shared-file":
				must(os.Chmod(file, 0666))
			case "too-large":
				must(os.Truncate(file, maxHistoryFileBytes+1))
			case "empty":
				must(os.Truncate(file, 0))
			case "relative-home":
				home = "grok"
			case "unclean-home":
				home += string(filepath.Separator) + "."
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			scope, err := openHistoryFiles(ctx, home, nil)
			if err == nil {
				defer scope.Close()
				raw, readErr := scope.read(ctx, relative, maxHistoryFileBytes)
				if readErr == nil || raw != nil {
					t.Fatal("unsafe path returned native data")
				}
			}
			if name == "missing-home" {
				if _, err := os.Lstat(home); !os.IsNotExist(err) {
					t.Fatal("reader created missing state")
				}
			}
		})
	}
}

func TestRetainedHistoryReaderPinsFileAndAncestorIdentityAcrossPair(t *testing.T) {
	for _, name := range []string{"file-content", "file-mtime", "file-replaced", "directory-replaced", "root-replaced", "scope-permissions", "canceled"} {
		t.Run(name, func(t *testing.T) {
			if runtime.GOOS == "windows" && name == "scope-permissions" {
				t.Skip("Unix mode mutation")
			}
			home := historyFileFixture(t, map[string][]byte{"native/first": []byte("original"), "native/second": []byte("second")})
			scope, err := openHistoryFiles(context.Background(), home, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer scope.Close()
			if _, err := scope.read(context.Background(), filepath.Join("native", "first"), 16); err != nil {
				t.Fatal(err)
			}
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			ctx := context.Background()
			switch name {
			case "file-mtime":
				timestamp := time.Now().Add(-time.Hour)
				must(os.Chtimes(filepath.Join(home, "native", "first"), timestamp, timestamp))
			case "file-content":
				must(os.WriteFile(filepath.Join(home, "native", "first"), []byte("changed length"), 0600))
			case "file-replaced":
				file := filepath.Join(home, "native", "first")
				must(os.Rename(file, file+"-old"))
				must(os.WriteFile(file, []byte("original"), 0600))
			case "directory-replaced":
				path := filepath.Join(home, "native")
				must(os.Rename(path, path+"-old"))
				must(os.Mkdir(path, 0700))
				must(os.WriteFile(filepath.Join(path, "second"), []byte("second"), 0600))
			case "root-replaced":
				if runtime.GOOS == "windows" {
					// Windows keeps the retained root open without delete sharing, so
					// replacement is rejected before the second read can run.
					if err := os.Rename(home, home+"-old"); err == nil {
						t.Fatal("retained root was unexpectedly movable")
					}
					if _, err := scope.read(ctx, filepath.Join("native", "first"), 16); err != nil {
						t.Fatal(err)
					}
					return
				}
				must(os.Rename(home, home+"-old"))
				must(os.Mkdir(home, 0700))
			case "scope-permissions":
				must(os.Chmod(home, 0755))
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if raw, err := scope.read(ctx, filepath.Join("native", "second"), 16); err == nil || raw != nil {
				t.Fatal("mutated pair returned partial content")
			}
		})
	}
}
