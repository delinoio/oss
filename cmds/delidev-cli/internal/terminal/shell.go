package terminal

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
)

func shellError() error {
	return domain.Fail(domain.InvalidArgument, "The selected Worker shell is invalid or unavailable.", "Select an absolute executable on the Worker; no alternate shell will be substituted.")
}

func ResolveShell(ctx context.Context, root string, owner domain.ID, override string, logger *slog.Logger) (string, error) {
	path := override
	if path == "" {
		var err error
		path, err = defaultShell(ctx, root, owner, logger)
		if err != nil {
			return "", err
		}
	}
	return validateShell(path)
}

func validateShell(path string) (string, error) {
	if !filepath.IsAbs(path) || domain.Text(path, "shell path", 4096, true) != nil {
		return "", shellError()
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", shellError()
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() || !shellExecutable(info) {
		return "", shellError()
	}
	return resolved, nil
}

type boundedShellOutput struct{ bytes []byte }

func (b *boundedShellOutput) Write(raw []byte) (int, error) {
	if len(b.bytes)+len(raw) > 8192 {
		return 0, shellError()
	}
	b.bytes = append(b.bytes, raw...)
	return len(raw), nil
}

func accountShellQuery(ctx context.Context, root, executable string, owner domain.ID, args []string, logger *slog.Logger) (string, error) {
	output := &boundedShellOutput{}
	err := process.Run(ctx, process.Config{Directory: filepath.Join(root, "terminal-processes"), OwnerID: owner, Executable: executable, Args: args, Env: []string{"PATH=/usr/bin:/bin", "LC_ALL=C"}, Cwd: root, Stdout: output, Stderr: io.Discard, Logger: logger})
	if err != nil {
		if domain.SafeError(err).Code == domain.RecoveryRequired {
			return "", domain.SafeError(err)
		}
		return "", domain.Fail(domain.Unavailable, "The Worker's account default shell could not be resolved.", "Inspect the account shell on that Worker or provide an explicit valid override.")
	}
	return strings.TrimSpace(string(output.bytes)), nil
}
