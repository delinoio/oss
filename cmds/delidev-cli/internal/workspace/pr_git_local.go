// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
)

// These operands never grant an external file, editor, output or config path.
// In addition, local Git executes in the launcher's inherited harness sandbox;
// filters, merge drivers and other repository helpers cannot borrow bridge authority.
func validPRLocalArgs(args []string) bool {
	switch args[0] {
	case "status":
		return len(args) == 1 || len(args) == 2 && slices.Contains([]string{"--short", "--porcelain", "--porcelain=v1"}, args[1])
	case "diff":
		return len(args) == 1 || len(args) == 2 && slices.Contains([]string{"--cached", "--stat", "--check"}, args[1])
	case "log":
		return len(args) == 1 || slices.Equal(args, []string{"log", "-1", "--oneline"}) || slices.Equal(args, []string{"log", "-1", "--format=%B"})
	case "show":
		return len(args) == 1 || len(args) == 2 && (args[1] == "HEAD" || canonicalCommit(args[1]))
	case "rev-parse":
		return slices.Equal(args, []string{"rev-parse", "HEAD"}) || slices.Equal(args, []string{"rev-parse", "--show-toplevel"}) || slices.Equal(args, []string{"rev-parse", "--verify", "HEAD^{commit}"})
	case "commit":
		return len(args) == 3 && args[1] == "-m" && strings.TrimSpace(args[2]) != "" || slices.Equal(args, []string{"commit", "--no-edit"})
	case "add":
		if slices.Equal(args, []string{"add", "-A"}) || slices.Equal(args, []string{"add", "--all"}) {
			return true
		}
		paths := args[1:]
		if len(paths) > 0 && paths[0] == "--" {
			paths = paths[1:]
		}
		if len(paths) == 0 {
			return false
		}
		for _, path := range paths {
			if path == "" || strings.ContainsAny(path, "\\:") || strings.HasPrefix(path, "-") || filepath.IsAbs(path) || filepath.Clean(path) != path {
				return false
			}
			for _, part := range strings.Split(path, "/") {
				if part == ".." || strings.EqualFold(part, ".git") {
					return false
				}
			}
		}
		return true
	}
	return false
}

// Called only in the harness-launched client after the live Worker bridge has
// checked the immutable scope. Native authentication never enters this process.
// The independently owned child inherits the client's OS sandbox. Its private
// transient process journal lives in that sandbox's temporary directory.
func runPRLocalGit(ctx context.Context, scope prGitScope, args []string, stdout io.Writer) error {
	dir, err := os.MkdirTemp("", "delidev-pr-local-")
	if err != nil {
		return toolFailure()
	}
	local := &PRGitTool{scope: scope, path: filepath.Join(dir, "scope.json")}
	environment := local.localEnvironmentFrom(os.Environ())
	command := []string{"-C", scope.RepositoryPath, "-c", "core.fsmonitor=false", "-c", "core.hooksPath=" + filepath.Join(scope.Root, "empty-hooks"), "-c", "user.name=" + scope.LocalName, "-c", "user.email=" + scope.LocalEmail, "-c", "commit.gpgSign=false", "-c", "core.editor=true"}
	if args[0] == "diff" || args[0] == "show" {
		args = append([]string{args[0], "--no-ext-diff", "--no-textconv"}, args[1:]...)
	}
	command = append(command, args...)
	bounded, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	output := limitedOutput{limit: MaxGitOutput}
	err = process.Run(bounded, process.Config{Directory: dir, OwnerID: scope.Claim.JobID, Executable: scope.GitExecutable, Args: command, Env: environment, Cwd: scope.RepositoryPath, Stdout: &output, Stderr: io.Discard, Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))})
	if err != nil && domain.SafeError(err).Code == domain.RecoveryRequired {
		return err
	}
	_ = os.RemoveAll(dir)
	if err != nil {
		return domain.Fail(domain.Unavailable, "The sandboxed local PR Git command did not complete.", "Inspect the original workspace and resolve the command without replaying its push.")
	}
	_, err = stdout.Write(output.Bytes())
	return err
}
