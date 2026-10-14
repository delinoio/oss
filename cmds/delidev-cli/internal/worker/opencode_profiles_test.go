package worker

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/nativeprofile"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// Executions, continuations and an owner restart resolve one permanent OpenCode
// profile, and every execution identifier is bound to it as history.
func TestOpenCodeExecutionProfileIsStableAcrossContinuationsAndRestart(t *testing.T) {
	config, machine := profileTestConfig(t)
	account, session := domain.NewID(), domain.NewID()
	ctx := context.Background()
	first := profileExecution(machine, account, session)
	lease, err := openCodeExecutionProfile(ctx, config, first, "")
	if err != nil {
		t.Fatal(err)
	}
	root := lease.Root
	marker := filepath.Join(root, "data", "opencode", "history")
	if err := security.PrivateDir(filepath.Dir(marker)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("retained"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	restartProfileOwner(config, machine)
	next := profileExecution(machine, account, session)
	next.Continuation = &domain.ExecutionContinuation{HistoryExecutionID: first.ExecutionID}
	again, err := openCodeExecutionProfile(ctx, config, next, first.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	if again.Root != root || again.Created {
		t.Fatal("a continuation allocated a new native home")
	}
	if raw, err := os.ReadFile(marker); err != nil || string(raw) != "retained" {
		t.Fatal("native history was not retained")
	}
	if err := again.Close(); err != nil {
		t.Fatal(err)
	}
	// A later continuation names the original history, not the newest execution.
	third := profileExecution(machine, account, session)
	third.Continuation = &domain.ExecutionContinuation{HistoryExecutionID: first.ExecutionID}
	later, err := openCodeExecutionProfile(ctx, config, third, next.ExecutionID)
	if err != nil || later.Root != root {
		t.Fatalf("later continuation resolved a different profile: %v", err)
	}
	_ = later.Close()
	for _, id := range []domain.ID{first.ExecutionID, next.ExecutionID, third.ExecutionID} {
		resolved, err := openCodeHistoryRoot(ctx, config.Root, machine, account, id)
		if err != nil || resolved != root {
			t.Fatalf("history %s did not resolve the permanent profile: %v", id, err)
		}
	}
}

// A different account or conversation never shares an OpenCode profile.
func TestOpenCodeExecutionProfilesAreIsolated(t *testing.T) {
	config, machine := profileTestConfig(t)
	ctx := context.Background()
	one, err := openCodeExecutionProfile(ctx, config, profileExecution(machine, domain.NewID(), domain.NewID()), "")
	if err != nil {
		t.Fatal(err)
	}
	defer one.Close()
	two, err := openCodeExecutionProfile(ctx, config, profileExecution(machine, domain.NewID(), domain.NewID()), "")
	if err != nil {
		t.Fatal(err)
	}
	defer two.Close()
	if one.Root == two.Root || one.ID == two.ID {
		t.Fatal("independent conversations share an OpenCode profile")
	}
}

// A legacy Fork child kept its native runtime below "native". Registering the
// legacy directory in place resolves that subdirectory without moving it.
func TestOpenCodeRuntimeRootResolvesLegacyForkNativeDirectory(t *testing.T) {
	config, machine := profileTestConfig(t)
	account, runtime := domain.NewID(), domain.NewID()
	legacy := filepath.Join(config.Root, "runtimes", string(runtime))
	if err := security.PrivateDir(filepath.Join(legacy, "native")); err != nil {
		t.Fatal(err)
	}
	resolved, err := openCodeHistoryRoot(context.Background(), config.Root, machine, account, runtime)
	if err != nil || resolved != filepath.Join(legacy, "native") {
		t.Fatalf("legacy fork native directory was not resolved in place: %q %v", resolved, err)
	}
	managed := nativeprofile.Info{Origin: nativeprofile.OriginManaged, Root: legacy}
	if root, err := openCodeRuntimeRoot(managed); err != nil || root != legacy {
		t.Fatal("a managed profile root must be the runtime root")
	}
}

// Resetting an unsent attempt drops its abandoned native session but keeps the
// profile and its other retained files.
func TestResetUnsentOpenCodeHistoryKeepsProfileFiles(t *testing.T) {
	config, machine := profileTestConfig(t)
	lease, err := openCodeExecutionProfile(context.Background(), config, profileExecution(machine, domain.NewID(), domain.NewID()), "")
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	for _, directory := range []string{"data/opencode", "cache/opencode", "tmp", "config", "state", "opencode"} {
		if err := security.PrivateDir(filepath.Join(lease.Root, filepath.FromSlash(directory))); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{"data/opencode/opencode.db": "abandoned", "cache/opencode/model-cache": "kept", "instructions.txt": "per-run"}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(lease.Root, filepath.FromSlash(name)), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := resetUnsentOpenCodeHistory(lease.Root); err != nil {
		t.Fatal(err)
	}
	for _, removed := range []string{"data/opencode", "instructions.txt"} {
		if _, err := os.Lstat(filepath.Join(lease.Root, filepath.FromSlash(removed))); !os.IsNotExist(err) {
			t.Fatalf("%s survived the unsent reset", removed)
		}
	}
	if raw, err := os.ReadFile(filepath.Join(lease.Root, "cache", "opencode", "model-cache")); err != nil || string(raw) != "kept" {
		t.Fatal("the unsent reset removed a retained cache")
	}
	if err := resetUnsentOpenCodeHistory(lease.Root); err != nil {
		t.Fatalf("the unsent reset is not idempotent: %v", err)
	}
}
