package cli

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

func verifyNativeCLIFork(t *testing.T, ctx context.Context, run func([]string, any) map[string]any, sourceID string, original domain.Session, originalJob domain.Job, workerRoot string, calls *atomic.Int64) {
	t.Helper()
	var assignment domain.ExecutionJobInput
	var manifest workspace.Manifest
	if domain.Decode(originalJob.Input, &assignment) != nil || domain.Decode(assignment.Manifest, &manifest) != nil {
		t.Fatal("original assignment unavailable")
	}
	paths := []string{manifest.PrimaryPath}
	if len(manifest.Repositories) > 0 {
		paths = nil
		for _, repo := range manifest.Repositories {
			paths = append(paths, repo.Path)
		}
	}
	for _, path := range paths {
		if err := os.WriteFile(filepath.Join(path, "fork-private.txt"), []byte("original private files"), 0600); err != nil {
			t.Fatal(err)
		}
		if len(manifest.Repositories) > 0 {
			if err := os.WriteFile(filepath.Join(path, "tracked.txt"), []byte("fork staged files"), 0600); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.CommandContext(ctx, "git", "-C", path, "add", "tracked.txt").CombinedOutput(); err != nil {
				t.Fatalf("stage temporary fork fixture: %v %s", err, out)
			}
			if err := os.WriteFile(filepath.Join(path, "tracked.txt"), []byte("fork unstaged files"), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	current := run([]string{"session", "get", "--id", sourceID}, nil)
	rev := func(row map[string]any) string { return strconv.FormatUint(uint64(row["revision"].(float64)), 10) }
	run([]string{"session", "stop", "--id", sourceID, "--revision", rev(current)}, nil)
	run([]string{"session", "enqueue", "--id", sourceID}, domain.SessionInput{Mode: domain.ExecuteMode, Prompt: "Source-owned pending input"})
	current = run([]string{"session", "get", "--id", sourceID}, nil)
	before, _ := json.Marshal(current["data"])
	args := []string{"session", "fork", "--id", sourceID, "--revision", rev(current), "--turn-id", original.Execution.NativeTurnID, "--name", "Independent native fork", "--request-id", string(domain.NewID()), "--wait"}
	result := run(args, nil)
	childResource, ok := result["session"].(map[string]any)
	if !ok {
		t.Fatalf("fork child was not published: %v", result)
	}
	childID := childResource["id"].(string)
	var child domain.Session
	raw, _ := json.Marshal(childResource["data"])
	if domain.Decode(raw, &child) != nil || child.Fork == nil || child.Fork.SourceSessionID != domain.ID(sourceID) || child.PendingInputs != 0 || child.InitialExecution != nil || child.Fork.NativeThreadID == domain.NativeIdentity(original.Execution.NativeThreadID) || child.Dispatch != domain.DispatchPaused || calls.Load() != 1 {
		t.Fatal("fork changed history, inherited input or executed inference")
	}
	replay := run(args, nil)
	if replay["replayed"] != true || replay["session"].(map[string]any)["id"] != childID {
		t.Fatal("fork receipt created a second child")
	}
	var forkManifest workspace.Manifest
	raw, _ = json.Marshal(result["job"].(map[string]any)["data"])
	var forkJob domain.Job
	var output domain.ForkJobResult
	if domain.Decode(raw, &forkJob) != nil || domain.Decode(forkJob.Output, &output) != nil || domain.Decode(output.Manifest, &forkManifest) != nil {
		t.Fatal("fork ownership unavailable")
	}
	childPaths := []string{forkManifest.PrimaryPath}
	if len(forkManifest.Repositories) > 0 {
		childPaths = nil
		for _, repo := range forkManifest.Repositories {
			childPaths = append(childPaths, repo.Path)
		}
	}
	for i, path := range childPaths {
		if path == paths[i] {
			t.Fatal("independent fork shared source files")
		}
		content, err := os.ReadFile(filepath.Join(path, "fork-private.txt"))
		if err != nil || string(content) != "original private files" {
			t.Fatal("fork lost private files", err)
		}
		if len(manifest.Repositories) > 0 {
			a, ae := exec.CommandContext(ctx, "git", "-C", paths[i], "status", "--porcelain").Output()
			b, be := exec.CommandContext(ctx, "git", "-C", path, "status", "--porcelain").Output()
			if ae != nil || be != nil || string(a) != string(b) || !strings.Contains(string(b), "MM tracked.txt") {
				t.Fatal("fork lost exact staged and working files")
			}
		}
		if err := os.WriteFile(filepath.Join(path, "fork-private.txt"), []byte("child private files"), 0600); err != nil {
			t.Fatal(err)
		}
		content, err = os.ReadFile(filepath.Join(paths[i], "fork-private.txt"))
		if err != nil || string(content) != "original private files" {
			t.Fatal("child modified source files", err)
		}
	}
	run([]string{"session", "enqueue", "--id", childID}, domain.SessionInput{Mode: domain.ExecuteMode, Prompt: "Public second prompt"})
	current = run([]string{"session", "get", "--id", childID}, nil)
	run([]string{"session", "resume", "--id", childID, "--revision", rev(current)}, nil)
	wait := func(want int64) {
		for {
			current = run([]string{"session", "get", "--id", childID}, nil)
			raw, _ := json.Marshal(current["data"])
			child = domain.Session{}
			if domain.Decode(raw, &child) != nil || child.Recovery != domain.NoRecovery || child.Outcome == domain.ExecutionFailed || child.Dispatch == domain.DispatchBlocked {
				t.Fatalf("child continuation failed: %+v", child)
			}
			if calls.Load() == want && child.Execution != nil && child.Execution.CleanupVerified && child.PendingInputs == 0 {
				break
			}
			select {
			case <-time.After(30 * time.Millisecond):
			case <-ctx.Done():
				t.Fatalf("child continuation timed out: %+v", child)
			}
		}
		if child.Execution.NativeThreadID != string(child.Fork.NativeThreadID) || child.InitialExecution.ConfigurationDigest != original.InitialExecution.ConfigurationDigest || child.InitialExecution.InitialAccountID != original.InitialExecution.InitialAccountID {
			t.Fatal("child rerouted or lost native lineage")
		}
	}
	wait(2)
	run([]string{"session", "enqueue", "--id", childID}, domain.SessionInput{Mode: domain.PlanMode, Prompt: "Public third prompt"})
	wait(3)
	after, _ := json.Marshal(run([]string{"session", "get", "--id", sourceID}, nil)["data"])
	if string(before) != string(after) {
		t.Fatal("fork or child continuation modified source state")
	}
	t.Log("Public CLI/Connect/Worker native fork, exact retries, empty child queue, independent files and two process-replacement child turns passed without user credentials or seeded readiness")
}
