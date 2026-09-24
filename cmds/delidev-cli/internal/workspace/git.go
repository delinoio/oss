// Package workspace owns Worker-local Git preparation. No operation in this
// package opens the server database or uses a server-side GitHub PAT.
package workspace

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
)

const MaxGitOutput = 4 << 20

type Inspection struct {
	Root        string            `json:"root"`
	Name        string            `json:"name"`
	Remotes     []string          `json:"remotes"`
	DefaultRefs map[string]string `json:"default_refs"`
}
type Git struct {
	Executable  string
	ProcessRoot string
	OwnerID     domain.ID
	Logger      *slog.Logger
	HooksDir    string
	Timeout     time.Duration
}

type limitedOutput struct {
	bytes.Buffer
	limit    int
	overflow bool
}

func (b *limitedOutput) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		b.overflow = true
		return 0, io.ErrShortBuffer
	}
	return b.Buffer.Write(p)
}
func (g Git) run(ctx context.Context, root string, args ...string) ([]byte, error) {
	raw, _, err := g.runCommand(ctx, root, args...)
	return raw, err
}

// Native exit status is private evidence for commands with documented absence
// statuses. Launch, ownership, cancellation and output failures return -1 and
// must never be interpreted as an absent Git reference.
func (g Git) runCommand(ctx context.Context, root string, args ...string) ([]byte, int, error) {
	binary := g.Executable
	if binary == "" {
		var err error
		binary, err = exec.LookPath("git")
		if err != nil {
			return nil, -1, domain.Fail(domain.MissingInput, "Git is not installed on this Worker.", "Install Git on the selected execution machine.")
		}
	}
	timeout := g.Timeout
	if timeout == 0 {
		timeout = 2 * time.Minute
	}
	bounded, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	commandArgs := []string{"-C", root, "-c", "core.quotePath=false", "-c", "color.ui=false"}
	if g.HooksDir != "" {
		commandArgs = append(commandArgs, "-c", "core.hooksPath="+g.HooksDir)
	}
	commandArgs = append(commandArgs, args...)

	if g.ProcessRoot == "" || g.OwnerID.Validate() != nil {
		return nil, -1, domain.Fail(domain.MissingInput, "Git requires a private execution ownership scope.", "Run this operation through its owning Worker job or session.")
	}
	var out limitedOutput
	out.limit = MaxGitOutput
	err := process.Run(bounded, process.Config{Directory: g.ProcessRoot, OwnerID: g.OwnerID, Executable: binary, Args: commandArgs, Env: gitEnvironment(), Cwd: root, Stdout: &out, Stderr: io.Discard, Logger: g.Logger})
	if err != nil {
		if domain.SafeError(err).Code == domain.RecoveryRequired {
			return nil, -1, err
		}
		if bounded.Err() != nil {
			return nil, -1, domain.SafeError(bounded.Err())
		}
		if out.overflow {
			return nil, -1, domain.Fail(domain.ResourceExhausted, "Git output exceeded its bound.", "Narrow the requested repository operation.")
		}
		var exit interface{ ExitCode() int }
		if errors.As(err, &exit) {
			return nil, exit.ExitCode(), &domain.Error{Code: domain.Unavailable, Message: "Git could not complete the operation on this Worker.", Guidance: "Check the selected repository, reference, remote access, and Worker Git authentication; no stale fallback was used.", Cause: "git_exit"}
		}
		return nil, -1, &domain.Error{Code: domain.Unavailable, Message: "Git could not be launched on this Worker.", Guidance: "Check the configured executable and filesystem permissions.", Cause: "git_launch"}
	}
	return out.Bytes(), 0, nil
}
func gitEnvironment() []string {
	allowed := map[string]bool{
		"PATH": true, "HOME": true, "USERPROFILE": true, "HOMEDRIVE": true, "HOMEPATH": true,
		"SYSTEMROOT": true, "WINDIR": true, "TEMP": true, "TMP": true, "TMPDIR": true,
		"LANG": true, "LC_ALL": true, "XDG_CONFIG_HOME": true, "SSH_AUTH_SOCK": true,
		"SSH_AGENT_PID": true, "GIT_SSH": true, "GIT_SSH_COMMAND": true,
		"GIT_CONFIG_SYSTEM": true, "GIT_CONFIG_GLOBAL": true, "GIT_CONFIG_NOSYSTEM": true,
	}
	out := []string{}
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if allowed[strings.ToUpper(name)] {
			out = append(out, entry)
		}
	}
	return append(out, "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never", "GIT_OPTIONAL_LOCKS=0")
}

func (g Git) Inspect(ctx context.Context, path string) (Inspection, error) {
	if err := domain.Text(path, "checkout path", 4096, true); err != nil {
		return Inspection{}, err
	}
	if !filepath.IsAbs(path) {
		return Inspection{}, domain.Fail(domain.InvalidArgument, "Repository inspection requires an absolute Worker path.", "Provide the checkout root or a directory inside it.")
	}
	raw, err := g.run(ctx, path, "rev-parse", "--show-toplevel")
	if err != nil {
		if domain.SafeError(err).Code == domain.RecoveryRequired {
			return Inspection{}, err
		}
		if ctx.Err() != nil {
			return Inspection{}, domain.SafeError(ctx.Err())
		}
		return Inspection{}, domain.Fail(domain.InvalidArgument, "The path is not an accessible Git working tree on this Worker.", "Select an existing root, subdirectory, or linked worktree.")
	}
	root := strings.TrimSuffix(string(raw), "\n")
	root = strings.TrimSuffix(root, "\r")
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return Inspection{}, domain.Fail(domain.Unavailable, "The canonical Git root could not be resolved.", "Check the checkout's filesystem availability.")
	}
	raw, err = g.run(ctx, root, "remote")
	if err != nil {
		return Inspection{}, err
	}
	result := Inspection{Root: root, Name: filepath.Base(root), Remotes: []string{}, DefaultRefs: map[string]string{}}
	for _, remote := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		remote = strings.TrimSuffix(remote, "\r")
		if remote == "" {
			continue
		}
		if strings.ContainsAny(remote, " /\\:\r\n") || strings.HasPrefix(remote, "-") {
			return Inspection{}, domain.Fail(domain.InvalidArgument, "A Git remote has an unsupported name.", "Rename the remote to an unambiguous name before using this checkout.")
		}
		result.Remotes = append(result.Remotes, remote)
		symbolic, err := g.run(ctx, root, "symbolic-ref", "--quiet", "refs/remotes/"+remote+"/HEAD")
		if err == nil {
			ref := strings.TrimSpace(string(symbolic))
			prefix := "refs/remotes/" + remote + "/"
			if strings.HasPrefix(ref, prefix) {
				result.DefaultRefs[remote] = strings.TrimPrefix(ref, prefix)
			}
		} else if ctx.Err() != nil {
			return Inspection{}, domain.SafeError(ctx.Err())
		}
	}
	return result, nil
}
func DefaultStarting(inspection Inspection, preferred string) (domain.Reference, error) {
	remote := preferred
	if remote != "" && !slices.Contains(inspection.Remotes, remote) {
		return domain.Reference{}, domain.Fail(domain.InvalidArgument, "The preferred remote does not exist on this Worker.", "Refresh repository inspection and select a current remote.")
	}
	if remote == "" {
		if slices.Contains(inspection.Remotes, "origin") {
			remote = "origin"
		} else if len(inspection.Remotes) == 1 {
			remote = inspection.Remotes[0]
		} else {
			return domain.Reference{}, domain.Fail(domain.MissingInput, "The starting remote is ambiguous or absent.", "Choose a preferred remote or an explicit starting reference.")
		}
	}
	branch := inspection.DefaultRefs[remote]
	if branch == "" {
		return domain.Reference{}, domain.Fail(domain.MissingInput, "The selected remote's default branch is unavailable locally.", "Configure its default branch explicitly; DeliDev does not guess main or master.")
	}
	return domain.Reference{Type: domain.RemoteBranch, Remote: remote, Name: branch}, nil
}
func (g Git) Resolve(ctx context.Context, inspection Inspection, ref domain.Reference, autoFetch bool) (string, error) {
	if err := ref.Validate(false); err != nil {
		return "", err
	}
	name := ref.Name
	switch ref.Type {
	case domain.LocalBranch:
		if _, err := g.run(ctx, inspection.Root, "check-ref-format", "refs/heads/"+ref.Name); err != nil {
			return "", domain.Fail(domain.InvalidArgument, "The local branch reference is invalid.", "Choose an exact local branch name.")
		}
		name = "refs/heads/" + ref.Name
	case domain.RemoteBranch:
		if !slices.Contains(inspection.Remotes, ref.Remote) {
			return "", domain.Fail(domain.InvalidArgument, "The remote is not present on the selected Worker.", "Reinspect and configure an existing remote.")
		}
		if _, err := g.run(ctx, inspection.Root, "check-ref-format", "refs/heads/"+ref.Name); err != nil {
			return "", domain.Fail(domain.InvalidArgument, "The remote branch reference is invalid.", "Choose an exact remote branch name.")
		}
		name = "refs/remotes/" + ref.Remote + "/" + ref.Name
		if autoFetch {
			// Fetch only this branch into its tracking ref before resolving the commit.
			// --no-recurse-submodules avoids unrelated preparation/authentication work.
			if _, err := g.run(ctx, inspection.Root, "fetch", "--no-tags", "--no-recurse-submodules", "--", ref.Remote, "+refs/heads/"+ref.Name+":"+name); err != nil {
				return "", err
			}
		}
	case domain.CommitReference:
		if len(name) != 40 && len(name) != 64 {
			return "", domain.Fail(domain.InvalidArgument, "A commit reference must use its full object ID.", "Choose an exact 40- or 64-digit Git commit identity.")
		}
		for _, c := range name {
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
				return "", domain.Fail(domain.InvalidArgument, "Invalid commit identity.", "Use lowercase hexadecimal Git object IDs.")
			}
		}
	}
	raw, err := g.run(ctx, inspection.Root, "rev-parse", "--verify", "--end-of-options", name+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(raw)), nil
}

// Git for Windows prints slash-separated paths while Go's ownership paths use
// native separators. Compare in the native path namespace without changing the
// original arguments or resolving a replacement filesystem link.
func sameNativePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
