package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func checkpointRuntimeFixture(t *testing.T) string {
	t.Helper()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(parent, "runtime")
	for _, dir := range []string{"", "data", "data/opencode"} {
		if err := security.PrivateDir(filepath.Join(home, dir)); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"data/opencode/opencode.db", "data/opencode/opencode.db-wal", "data/opencode/native-artifact"} {
		if err := security.WriteAtomic(filepath.Join(home, name), []byte("private fixture bytes: "+name)); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func attachCheckpointFixture(t *testing.T, api *OwnedAPI) string {
	t.Helper()
	s := api.session
	home := checkpointRuntimeFixture(t)
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	project, err := collectProjectInstructions(workspace, workspace)
	if err != nil {
		t.Fatal(err)
	}
	s.owner, s.cwd, s.runtimeRoot, s.runtimeHome = domain.NewID(), workspace, workspace, home
	s.apiProfile = &nativeAPIProfile{Settings: s.creation.settings, BaseURL: "http://127.0.0.1:1/api-proxy/v1", Token: "private fixture token", Rejection: StopOnInteractionRejection, ProjectInstructions: project}
	s.apiVerified = true
	return home
}

func completedCheckpointFixture(t *testing.T) (*OwnedAPI, string) {
	t.Helper()
	f := newHistoryFixture(t)
	prepareHistoryCleanup(f, func(context.Context) error { return nil })
	if _, err := f.api.CloseCompleted(context.Background()); err != nil {
		t.Fatal(err)
	}
	return f.api, attachCheckpointFixture(t, f.api)
}

func TestOpenCodeCheckpointPreservesOriginalClosedOwnership(t *testing.T) {
	api, home := completedCheckpointFixture(t)
	ctx := context.Background()
	raw, ref, err := api.RetainCheckpoint(ctx)
	if err != nil || ref.RequiresResume || InspectCheckpoint(ctx, home, raw, ref) != nil {
		t.Fatal("closed original evidence did not round trip", err)
	}
	for _, private := range []string{"private input", "private fixture bytes", "private fixture token"} {
		if bytes.Contains(raw, []byte(private)) {
			t.Fatal("private content entered comparison evidence")
		}
	}
	for _, change := range []func(*CheckpointReference){
		func(r *CheckpointReference) { r.OwnerID = domain.NewID() },
		func(r *CheckpointReference) { r.CreationRequestID = domain.NewID() },
		func(r *CheckpointReference) { r.InputRequestID = domain.NewID() },
		func(r *CheckpointReference) { r.InputSHA256 = strings.Repeat("0", 64) },
		func(r *CheckpointReference) { r.HistorySHA256 = strings.Repeat("0", 64) },
		func(r *CheckpointReference) { r.RequiresResume = true },
	} {
		other := ref
		change(&other)
		if InspectCheckpoint(ctx, home, raw, other) == nil {
			t.Fatal("changed independent original reference was accepted")
		}
	}
	if InspectCheckpoint(ctx, filepath.Dir(home), raw, ref) == nil {
		t.Fatal("checkpoint supplied its own runtime ownership")
	}
	raw[0] = '['
	again, same, err := api.RetainCheckpoint(ctx)
	if err != nil || same != ref || InspectCheckpoint(ctx, home, again, same) != nil {
		t.Fatal("caller bytes mutated original retention")
	}
}

func TestOpenCodeCheckpointRejectsNoncanonicalAndContradictoryMetadata(t *testing.T) {
	api, home := completedCheckpointFixture(t)
	raw, ref, err := api.RetainCheckpoint(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"whitespace", "unknown", "duplicate", "version", "history", "part", "identity", "root", "files", "stop"} {
		t.Run(mode, func(t *testing.T) {
			var value nativeCheckpoint
			if json.Unmarshal(raw, &value) != nil {
				t.Fatal("fixture decode failed")
			}
			switch mode {
			case "version":
				value.NativeVersion = "foreign"
			case "history":
				value.History.Messages[0].Digest = strings.Repeat("0", 64)
			case "part":
				value.History.Messages[0].Parts[0].Kind = "foreign"
			case "identity":
				value.Reference.CreationRequestID = value.Reference.InputRequestID
			case "root":
				value.Workspace = home
			case "files":
				value.Files = value.Files[1:]
			case "stop":
				value.Stop = &StopReceipt{RequestID: domain.NewID()}
			}
			changed, _ := json.Marshal(value)
			switch mode {
			case "whitespace":
				changed = append(changed, '\n')
			case "unknown":
				changed = append([]byte(`{"foreign":true,`), changed[1:]...)
			case "duplicate":
				changed = append([]byte(`{"version":1,`), changed[1:]...)
			}
			other := ref
			other.SHA256 = mutationDigest(changed)
			if InspectCheckpoint(context.Background(), home, changed, other) == nil {
				t.Fatal("new outer hash accepted inconsistent private evidence")
			}
		})
	}
}

func TestOpenCodeCheckpointCannotUpgradeUnprovedCleanup(t *testing.T) {
	for _, mode := range []string{"live", "ordinary-close", "failed-cleanup", "problem", "unverified", "missing-profile"} {
		t.Run(mode, func(t *testing.T) {
			f := newHistoryFixture(t)
			prepareHistoryCleanup(f, func(context.Context) error { return nil })
			switch mode {
			case "ordinary-close":
				if err := f.api.Close(context.Background()); err != nil {
					t.Fatal(err)
				}
			case "failed-cleanup":
				f.api.session.closeOwned = func(context.Context) error { return sessionUncertain() }
				if _, err := f.api.CloseCompleted(context.Background()); err == nil {
					t.Fatal("fixture cleanup succeeded")
				}
			case "live":
			default:
				if _, err := f.api.CloseCompleted(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			attachCheckpointFixture(t, f.api)
			switch mode {
			case "problem":
				f.api.session.problem = sessionUncertain()
			case "unverified":
				f.api.session.apiVerified = false
			case "missing-profile":
				f.api.session.apiProfile = nil
			}
			if raw, _, err := f.api.RetainCheckpoint(context.Background()); err == nil || len(raw) != 0 {
				t.Fatal("missing original cleanup/configuration gained checkpoint authority")
			}
		})
	}
}

func TestOpenCodeCheckpointKeepsStoppedCompletionSeparate(t *testing.T) {
	for _, kind := range []InteractionKind{PermissionInteraction, QuestionInteraction} {
		f := newStoppedHistoryFixture(t, kind, true)
		api := f.history.api
		proof, err := api.CloseAfterStop(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		home := attachCheckpointFixture(t, api)
		raw, ref, err := api.RetainCheckpoint(context.Background())
		if err != nil || !ref.RequiresResume || InspectCheckpoint(context.Background(), home, raw, ref) != nil {
			t.Fatal("stopped original evidence lost explicit resume requirement", err)
		}
		value, err := decodeCheckpoint(raw, ref, home)
		if err != nil || value.Stop == nil || *value.Stop != proof.Stop || value.Stop.HTTPAccepted {
			t.Fatal("stopped checkpoint invented HTTP acceptance")
		}
	}
}

func TestOpenCodeCheckpointFileChangesCannotBeRepinned(t *testing.T) {
	for _, mode := range []string{"altered", "missing", "extra", "wal", "mode", "link", "hardlink"} {
		t.Run(mode, func(t *testing.T) {
			if runtime.GOOS == "windows" && (mode == "mode" || mode == "link" || mode == "hardlink") {
				t.Skip("Unix filesystem permission/link fault")
			}
			api, home := completedCheckpointFixture(t)
			raw, ref, err := api.RetainCheckpoint(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(home, "data/opencode/native-artifact")
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "altered":
				err = os.WriteFile(path, bytes.Repeat([]byte{'x'}, len(original)), 0600)
			case "missing":
				err = os.Remove(path)
			case "extra":
				err = security.WriteAtomic(filepath.Join(home, "extra"), []byte("extra"))
			case "wal":
				err = os.Remove(filepath.Join(home, "data/opencode/opencode.db-wal"))
			case "mode":
				err = os.Chmod(path, 0620)
			case "link":
				err = os.Symlink(path, filepath.Join(home, "link"))
			case "hardlink":
				err = os.Link(path, filepath.Join(t.TempDir(), "external-link"))
			}
			if err != nil {
				t.Fatal(err)
			}
			if InspectCheckpoint(context.Background(), home, raw, ref) == nil {
				t.Fatal("changed runtime passed independent file inspection")
			}
			if _, _, err := api.RetainCheckpoint(context.Background()); err == nil {
				t.Fatal("changed runtime was repinned")
			}
			if mode == "altered" {
				if err := os.WriteFile(path, original, 0600); err != nil {
					t.Fatal(err)
				}
				if _, _, err := api.RetainCheckpoint(context.Background()); err == nil {
					t.Fatal("restored bytes erased observed checkpoint uncertainty")
				}
			}
		})
	}
}

func TestOpenCodeCheckpointInventoryBoundsAndCancellation(t *testing.T) {
	home := checkpointRuntimeFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := checkpointFiles(ctx, home); err == nil {
		t.Fatal("canceled runtime inspection succeeded")
	}
	path := filepath.Join(home, "data/opencode/opencode.db")
	if err := os.Truncate(path, maxCheckpointFileBytes+1); err != nil {
		t.Fatal(err)
	}
	if _, err := checkpointFiles(context.Background(), home); err == nil {
		t.Fatal("oversized runtime was partially inspected")
	}
	api, _ := completedCheckpointFixture(t)
	if _, _, err := api.RetainCheckpoint(ctx); err == nil || api.checkpointAttempted {
		t.Fatal("already canceled caller consumed checkpoint inspection")
	}
	if _, _, err := api.RetainCheckpoint(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestOpenCodeLargeCheckpointRetainsOriginalPrivateInventory(t *testing.T) {
	api, home := completedCheckpointFixture(t)
	for index := 0; index < 7000; index++ {
		path := filepath.Join(home, "data", "opencode", fmt.Sprintf("original-%04d-%s", index, strings.Repeat("x", 100)))
		if err := security.WriteAtomic(path, []byte("original bounded private bytes")); err != nil {
			t.Fatal(err)
		}
	}
	raw, ref, err := api.RetainCheckpoint(context.Background())
	if err != nil || len(raw) <= 1<<20 || len(raw) > maxCheckpointBytes {
		t.Fatal("large original checkpoint retention", len(raw), err)
	}
	if err := InspectCheckpoint(context.Background(), home, raw, ref); err != nil {
		t.Fatal("large original comparison evidence rejected", err)
	}
	path := filepath.Join(home, "data", "opencode", fmt.Sprintf("original-%04d-%s", 6999, strings.Repeat("x", 100)))
	if err := os.WriteFile(path, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	if InspectCheckpoint(context.Background(), home, raw, ref) == nil {
		t.Fatal("large checkpoint adopted changed private files")
	}
}
