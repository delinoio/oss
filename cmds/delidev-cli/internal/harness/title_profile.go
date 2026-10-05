package harness

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// VerifyCodexTitleProfile proves the exact installed binary and native thread
// protocol without logging in, opening a workspace, or sending inference.
func VerifyCodexTitleProfile(ctx context.Context, root string, owner domain.ID, executable, expectedVersion string, logger *slog.Logger) (supported bool, returned error) {
	if err := owner.Validate(); err != nil {
		return false, err
	}
	resolved, state := resolve(domain.Codex, executable)
	if state != domain.InstallationUnchecked || resolved != executable {
		return false, nil
	}
	processRoot := filepath.Join(root, "processes")
	probeRoot := filepath.Join(root, "probes")
	for _, directory := range []string{processRoot, filepath.Join(processRoot, string(owner)), probeRoot} {
		if err := security.PrivateDir(directory); err != nil {
			return false, err
		}
	}
	directory, err := os.MkdirTemp(probeRoot, string(owner)+"-title-")
	if err != nil {
		return false, err
	}
	if err := security.PrivateDir(directory); err != nil {
		return false, err
	}
	directory, err = filepath.EvalSymlinks(directory)
	if err != nil {
		return false, err
	}
	directory, err = filepath.Abs(directory)
	if err != nil {
		return false, err
	}
	defer func() {
		if err := process.ReconcileOwner(processRoot, owner); err != nil {
			supported, returned = false, err
			return
		}
		if err := os.RemoveAll(directory); err != nil && returned == nil {
			supported, returned = false, domain.Fail(domain.RecoveryRequired, "The private Codex title probe could not be removed.", "Inspect the Worker's retained probe directory before retrying.")
		}
	}()

	home := filepath.Join(directory, "codex")
	env, err := probeEnvironment(home)
	if err != nil {
		return false, err
	}
	bounded, cancel := context.WithTimeout(ctx, probeTimeout)
	stdout := probeOutput{capture: true}
	stderr := probeOutput{}
	stdout.cancel = cancel
	stderr.cancel = cancel
	err = process.Run(bounded, process.Config{Directory: processRoot, OwnerID: owner, Executable: resolved, Args: []string{"--version"}, Env: env, Cwd: home, Stdout: &stdout, Stderr: &stderr, Logger: logger})
	cancel()
	if err != nil {
		if domain.SafeError(err).Code == domain.RecoveryRequired {
			return false, err
		}
		return false, nil
	}
	version := parseVersion(domain.Codex, stdout.buffer.String())
	if stdout.overflow || stderr.overflow || !domain.CodexVersionAllowed(version) || version != expectedVersion {
		return false, nil
	}
	probeCtx, stopProbe := context.WithTimeout(ctx, probeTimeout)
	client, err := codex.Open(probeCtx, codex.Config{
		Mode: codex.ThreadProtocol, Version: version,
		Home:    filepath.Join(home, "codex"),
		Process: process.Config{Directory: processRoot, OwnerID: owner, Executable: resolved, Env: env, Cwd: home, Logger: logger},
	})
	stopProbe()
	if err != nil {
		if domain.SafeError(err).Code == domain.RecoveryRequired {
			return false, err
		}
		return false, nil
	}
	if err := client.Close(); err != nil {
		return false, domain.Fail(domain.RecoveryRequired, "Codex title profile cleanup could not be verified.", "Retain its private probe directory and reconcile owned processes before reconnecting.")
	}
	if logger != nil {
		logger.InfoContext(ctx, "codex automatic title profile verified", "owner_id", owner, "version", version)
	}
	return true, nil
}
