package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
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
	home := filepath.Join(parent, "claude")
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

func TestRetainedHistoryReaderBindsOriginalMainAndChildFiles(t *testing.T) {
	for _, manual := range []bool{false, true} {
		session, workspace, records, messages := historyFixture(t)
		var compactions []HistoryCompactionProof
		var actions []HistoryCompactionActionProof
		if manual {
			session, workspace, records, messages, compactions, actions = manualHistoryFixture(t, true)
		}
		raw := historyJSONL(t, records)
		name := filepath.Join("projects", "delidev", string(session)+".jsonl")
		home := historyFileFixture(t, map[string][]byte{name: raw})
		var logs bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&logs, nil))
		observed, err := ReadMainTranscript(context.Background(), home, session, workspace, messages, compactions, actions, logger)
		expected, proofErr := verifyTranscript(context.Background(), raw, session, workspace, messages, nil, compactions, actions)
		if err != nil || proofErr != nil || observed != expected {
			t.Fatal("main reader changed original evidence", err, proofErr)
		}
		if _, err := ReadMainTranscript(context.Background(), home, session, workspace+"-changed", messages, compactions, actions, logger); err == nil {
			t.Fatal("changed workspace passed reader")
		}
		for _, private := range []string{home, workspace, "Private original", "Private compaction"} {
			if strings.Contains(logs.String(), private) {
				t.Fatal("reader leaked private state")
			}
		}
		stored, err := os.ReadFile(filepath.Join(home, name))
		if err != nil || !bytes.Equal(raw, stored) {
			t.Fatal("inspection rewrote native history")
		}
	}
	session, workspace, records, metadata, binding, messages := childHistoryFixture(t)
	raw, sidecar := historyJSONL(t, records), []byte(nil)
	sidecar, _ = json.Marshal(metadata)
	base := filepath.Join("projects", "delidev", string(session), "subagents", "agent-"+binding.TaskID)
	home := historyFileFixture(t, map[string][]byte{base + ".jsonl": raw, base + ".meta.json": sidecar})
	if runtime.GOOS != "windows" {
		if err := os.Chmod(filepath.Join(home, base+".meta.json"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	observed, err := ReadChildTranscript(context.Background(), home, session, workspace, binding, messages, nil)
	expected, proofErr := VerifyChildTranscript(context.Background(), raw, sidecar, session, workspace, binding, messages)
	if err != nil || proofErr != nil || !reflect.DeepEqual(observed, expected) {
		t.Fatal("child pair lost original proof or private root protection", err, proofErr)
	}
}

func TestRetainedHistoryReaderRejectsUnsafeScopesWithoutRepair(t *testing.T) {
	for _, name := range []string{"missing-home", "home-file", "home-link", "parent-link", "file-link", "directory-link", "hard-link", "missing-file", "directory-file", "shared-root", "shared-parent", "shared-file", "too-large", "empty", "relative-home", "unclean-home", "canceled"} {
		t.Run(name, func(t *testing.T) {
			if runtime.GOOS == "windows" && (strings.Contains(name, "link") || strings.HasPrefix(name, "shared-")) {
				t.Skip("Unix mode/link fixture; Windows ACLs are independently checked")
			}
			session, workspace, records, messages := historyFixture(t)
			relative := filepath.Join("projects", "delidev", string(session)+".jsonl")
			home := historyFileFixture(t, map[string][]byte{relative: historyJSONL(t, records)})
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
				home = filepath.Join(home, "missing", "claude")
			case "home-file":
				must(os.Rename(home, home+"-retained"))
				must(os.WriteFile(home, []byte("not a directory"), 0600))
			case "home-link":
				must(os.Rename(home, home+"-retained"))
				must(os.Symlink(home+"-retained", home))
			case "parent-link":
				alias := filepath.Join(filepath.Dir(home), "alias")
				must(os.Symlink(filepath.Dir(home), alias))
				home = filepath.Join(alias, "claude")
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
				must(os.Truncate(file, maxHistoryTranscript+1))
			case "empty":
				must(os.Truncate(file, 0))
			case "relative-home":
				home = "claude"
			case "unclean-home":
				home += string(filepath.Separator) + "."
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			observed, err := ReadMainTranscript(ctx, home, session, workspace, messages, nil, nil, nil)
			if err == nil || observed != (TranscriptObservation{}) {
				t.Fatal("unsafe retained path granted partial history", observed)
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
			scope, err := openHistoryFiles(context.Background(), home)
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

func TestRetainedChildFilenameDoesNotAcquirePathAuthority(t *testing.T) {
	for _, task := range []string{"", ".", "..", "../other", "other/child", `other\child`, "C:stream", "child.jsonl", "child ", "child\x00", strings.Repeat("a", 129)} {
		if _, err := ReadChildTranscript(context.Background(), "missing", domain.NewID(), "missing", ChildHistoryBinding{TaskID: task}, nil, nil); err == nil || nativeHistoryTaskFilename(task) {
			t.Fatal("task gained filename authority")
		}
	}
	for _, task := range []string{"a1b2c3", "agent-0123456789_ABC"} {
		if !nativeHistoryTaskFilename(task) {
			t.Fatal("native token rejected")
		}
	}
}

func TestRetainedHistoryReaderAllowsUnrelatedDirectoryEntriesWithoutChangingProof(t *testing.T) {
	for _, relative := range []string{"unrelated-root", "native/unrelated-parent"} {
		t.Run(relative, func(t *testing.T) {
			home := historyFileFixture(t, map[string][]byte{"native/first": []byte("original"), "native/second": []byte("second")})
			scope, err := openHistoryFiles(context.Background(), home)
			if err != nil {
				t.Fatal(err)
			}
			defer scope.Close()
			if _, err := scope.read(context.Background(), filepath.Join("native", "first"), 16); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(home, relative), []byte("unrelated native metadata"), 0600); err != nil {
				t.Fatal(err)
			}
			if raw, err := scope.read(context.Background(), filepath.Join("native", "second"), 16); err != nil || string(raw) != "second" {
				t.Fatal("unrelated directory metadata invalidated unchanged file ownership", err)
			}
			if err := os.Remove(filepath.Join(home, relative)); err != nil {
				t.Fatal(err)
			}
			if err := scope.check(context.Background()); err != nil {
				t.Fatal("unrelated entry removal invalidated unchanged files", err)
			}
		})
	}
}
