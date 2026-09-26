package opencode

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestManualNativeOpenCodeOriginalProjectCheckpoint(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_OPENCODE_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit private pinned OpenCode project checkpoint fixture")
	}
	for _, mode := range []string{"build", "plan"} {
		t.Run(mode, func(t *testing.T) { nativeClosedCheckpoint(t, binary, mode, true) })
	}
}

func prepareNativeCheckpointGit(t *testing.T, config *apiSessionConfig) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	config.NativeRoot = config.Workspace
	if err := os.WriteFile(filepath.Join(config.Workspace, "original.txt"), []byte("Original private snapshot fixture.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q", "--initial-branch=main"},
		{"config", "core.hooksPath", filepath.Join(filepath.Dir(config.Probe.Home), "no-hooks")},
		{"add", "original.txt"},
		{"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "commit", "-qm", "Private fixture"},
	} {
		command := exec.CommandContext(ctx, "git", args...)
		command.Dir = config.Workspace
		command.Env = append(append([]string(nil), config.Probe.Process.Env...), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0")
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("private checkpoint Git fixture: %v %s", err, out)
		}
	}
}

func inspectNativeProjectSnapshot(t *testing.T, config apiSessionConfig, value nativeCheckpoint) {
	t.Helper()
	worktree := sha1.Sum([]byte(config.Workspace))
	prefix := "data/opencode/snapshot/" + value.Project + "/" + hex.EncodeToString(worktree[:])
	entries, objects := 0, 0
	for _, file := range value.Files {
		if !strings.HasPrefix(file.Path, prefix+"/") {
			continue
		}
		entries++
		if !file.Directory && strings.HasPrefix(file.Path, prefix+"/objects/") && !strings.HasPrefix(file.Path, prefix+"/objects/info/") {
			objects++
		}
	}
	root := filepath.Join(value.RuntimeHome, filepath.FromSlash(prefix))
	alternates, err := os.ReadFile(filepath.Join(root, "objects", "info", "alternates"))
	if err != nil || string(alternates) != filepath.Join(config.Workspace, ".git", "objects")+"\n" || value.NativeRoot != config.Workspace || value.Project == "global" || entries == 0 {
		t.Fatal("original native snapshot lost its independent object dependency", err)
	}
	if info, err := os.Stat(filepath.Join(root, "index")); err != nil || info.Size() == 0 {
		t.Fatal("original native snapshot index missing", err)
	}
	t.Logf("original project snapshot: entries=%d local_object_files=%d external_object_dependency=true", entries, objects)
}

func TestManualNativeOpenCodeProjectCheckpointReplacement(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_OPENCODE_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit private pinned OpenCode project replacement fixture")
	}
	for _, mode := range []string{"build", "plan", "failed", "stopped", "build-plan-build", "plan-build-plan"} {
		t.Run(mode, func(t *testing.T) { nativeCheckpointReplacement(t, binary, mode, true) })
	}
}
