//go:build !windows

// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/terminal"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

// A real General Chat zsh terminal keeps the verified directory, logs only the
// shell kind and outcome, and removes its wrapper files when closed.
func TestTerminalGeneralChatPromptIntegrationLifecycle(t *testing.T) {
	shell, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh is not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	root := filepath.Join(t.TempDir(), "worker")
	workspaceManager := workspace.Manager{Root: root, Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}
	input := workspace.PrepareRequest{SessionID: domain.NewID(), MachineID: domain.NewID(), Type: domain.GeneralChat}
	manifest, err := workspaceManager.Prepare(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	var logs terminalLogBuffer
	manager := newTerminalManager(ctx, Config{Root: root, Logger: slog.New(slog.NewJSONHandler(&logs, nil))}, nil, Credential{MachineID: input.MachineID}, domain.NewID())
	defer manager.close()
	assignment := terminal.Assignment{ID: domain.NewID(), SessionID: input.SessionID, Terminal: domain.Terminal{MachineID: input.MachineID, ShellOverride: shell, State: domain.TerminalStarting, Rows: 24, Columns: 80}, Operation: domain.TerminalOperation{ID: domain.NewID(), Action: domain.TerminalCreate}, Preparation: &input, Manifest: &manifest}
	result := manager.execute(assignment)
	if result.State != domain.TerminalRunning || result.Cwd != manifest.PrimaryPath {
		t.Fatalf("the shell must start in the verified original directory: %+v", result)
	}
	wrapper := terminal.PromptIntegrationDirectory(root, assignment.ID)
	if _, err := os.Stat(filepath.Join(wrapper, ".zshrc")); err != nil {
		t.Fatalf("wrapper files must exist while the shell runs: %v", err)
	}
	recorded := string(logs.snapshot())
	if !strings.Contains(recorded, `"msg":"terminal_prompt_integration"`) || !strings.Contains(recorded, `"shell_kind":"zsh"`) || !strings.Contains(recorded, `"outcome":"applied"`) {
		t.Fatalf("missing bounded integration log: %s", recorded)
	}
	if strings.Contains(recorded, manifest.PrimaryPath) || strings.Contains(recorded, "chat") {
		t.Fatalf("the integration log must not record the directory or label: %s", recorded)
	}
	closing := assignment
	closing.Terminal.State = domain.TerminalRunning
	closing.Operation = domain.TerminalOperation{ID: domain.NewID(), Action: domain.TerminalClose}
	closing.Preparation, closing.Manifest = nil, nil
	if closed := manager.execute(closing); closed.State != domain.TerminalClosed || !closed.CleanupVerified {
		t.Fatalf("close must verify cleanup: %+v", closed)
	}
	if _, err := os.Lstat(wrapper); !os.IsNotExist(err) {
		t.Fatalf("closing the terminal must remove its wrapper files: %v", err)
	}
}
