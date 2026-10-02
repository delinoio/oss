// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
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
// Ordinary fork/exec retains the client's OS sandbox and original native
// execution ownership (macOS coalition, Linux subreaper or Windows job).
// Do not use process.Run here: its macOS launchd supervisor would create a
// separate unsandboxed coalition. The Worker joins the original native owner
// before verifying a push, including any surviving local Git descendants.
func runPRLocalGit(ctx context.Context, scope prGitScope, args []string, stdout io.Writer) error {
	local := &PRGitTool{scope: scope, path: filepath.Join(scope.Root, "pr-git", string(scope.Claim.ExecutionID), "scope.json")}
	environment := local.localEnvironmentFrom(os.Environ())
	command := []string{"-C", scope.RepositoryPath, "-c", "core.fsmonitor=false", "-c", "core.hooksPath=" + filepath.Join(scope.Root, "empty-hooks"), "-c", "user.name=" + scope.LocalName, "-c", "user.email=" + scope.LocalEmail, "-c", "commit.gpgSign=false", "-c", "core.editor=true"}
	if args[0] == "diff" || args[0] == "show" {
		args = append([]string{args[0], "--no-ext-diff", "--no-textconv"}, args[1:]...)
	}
	command = append(command, args...)
	bounded, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	output := limitedOutput{limit: MaxGitOutput}
	child := exec.CommandContext(bounded, scope.GitExecutable, command...)
	child.Dir, child.Env = scope.RepositoryPath, environment
	child.Stdout, child.Stderr = &output, io.Discard
	// Bound inherited output-pipe waiting; descendant cleanup is proved by the
	// original native owner, not by treating this command's exit as cleanup.
	child.WaitDelay = time.Second
	err := child.Run()
	if err != nil {
		return domain.Fail(domain.Unavailable, "The sandboxed local PR Git command did not complete.", "Inspect the original workspace and resolve the command without replaying its push.")
	}
	_, err = stdout.Write(output.Bytes())
	return err
}
