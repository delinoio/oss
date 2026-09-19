package core

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var objectID = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

func gitCommand(ctx context.Context, path string, args ...string) *exec.Cmd {
	argv := append([]string{"-c", "core.quotePath=false", "-c", "core.hooksPath=" + os.DevNull, "-c", "core.fsmonitor=false", "-C", path}, args...)
	c := exec.CommandContext(ctx, "git", argv...)
	env := SystemEnvironment()
	env["GIT_CONFIG_NOSYSTEM"] = "1"
	env["GIT_CONFIG_GLOBAL"] = os.DevNull
	env["GIT_TERMINAL_PROMPT"] = "0"
	env["GIT_LFS_SKIP_SMUDGE"] = "1"
	for k, v := range env {
		c.Env = append(c.Env, k+"="+v)
	}
	return c
}
func Git(ctx context.Context, path string, args ...string) (string, error) {
	c := gitCommand(ctx, path, args...)
	var errout bytes.Buffer
	c.Stderr = &errout
	b, err := c.Output()
	if err != nil {
		return "", E("git-error", fmt.Sprintf("git %s failed; confirm the repository and object are available", args[0]), 3)
	}
	return strings.TrimSuffix(string(b), "\n"), nil
}
func Discover(ctx context.Context, path string) (common, root, branch string, err error) {
	root, err = Git(ctx, path, "rev-parse", "--show-toplevel")
	if err != nil {
		return
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return
	}
	common, err = Git(ctx, root, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return
	}
	common, err = filepath.EvalSymlinks(common)
	if err != nil {
		return
	}
	branch, _ = Git(ctx, root, "symbolic-ref", "--quiet", "--short", "HEAD")
	return
}
func ResolveCommit(ctx context.Context, path, ref string) (string, error) {
	if ref == "" {
		ref = "HEAD"
	}
	if strings.HasPrefix(ref, "-") || strings.ContainsAny(ref, "\x00\r\n") {
		return "", E("invalid-commit", "invalid commit reference", 2)
	}
	v, err := Git(ctx, path, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	if err != nil || !objectID.MatchString(v) {
		return "", E("commit-unavailable", "commit cannot be resolved locally", 2)
	}
	return v, nil
}
func CommitConfig(ctx context.Context, path, sha string) (Project, error) {
	b, err := Git(ctx, path, "show", sha+":"+ProjectFile)
	if err != nil {
		return Project{}, E("config-unavailable", "commit does not contain "+ProjectFile+"; commit the configuration before running", 2)
	}
	return ParseProject([]byte(b))
}

type treeEntry struct{ Mode, OID, Path string }

func tree(ctx context.Context, path, sha string) ([]treeEntry, error) {
	v, err := Git(ctx, path, "ls-tree", "-rz", "--full-tree", sha)
	if err != nil {
		return nil, err
	}
	out := []treeEntry{}
	for _, entry := range strings.Split(v, "\x00") {
		if entry == "" {
			continue
		}
		pair := strings.SplitN(entry, "\t", 2)
		if len(pair) != 2 {
			return nil, E("git-tree-invalid", "invalid Git tree record", 3)
		}
		meta := strings.Fields(pair[0])
		if len(meta) != 3 || !SafeRelative(pair[1]) {
			return nil, E("unsupported-source-path", "source contains an unrepresentable file path", 2)
		}
		for _, component := range strings.Split(pair[1], "/") {
			folded := strings.ToLower(strings.TrimRight(component, " ."))
			if folded == ".git" || strings.HasPrefix(folded, "git~") {
				return nil, E("unsupported-source-path", "source path overlaps managed Git metadata", 2)
			}
		}
		if meta[0] == "160000" {
			return nil, E("submodules-unsupported", "submodule source is unsupported", 2)
		}
		out = append(out, treeEntry{meta[0], meta[2], pair[1]})
	}
	return out, nil
}
func ValidateSource(ctx context.Context, path, sha string) error {
	entries, err := tree(ctx, path, sha)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if filepath.Base(e.Path) == ".gitattributes" {
			b, err := Git(ctx, path, "cat-file", "blob", e.OID)
			if err != nil {
				return err
			}
			if strings.Contains(b, "filter=lfs") {
				return E("lfs-unsupported", "Git LFS source is unsupported", 2)
			}
		}
	}
	return nil
}
func (s *Store) Prepare(ctx context.Context, r Run) (string, error) {
	dir := filepath.Join(s.Root, "workspaces", r.ID)
	if _, err := os.Lstat(dir); err == nil {
		return "", E("workspace-exists", "a previous owned workspace requires interruption recovery", 3)
	}
	if err := PrivateDir(dir); err != nil {
		return "", err
	}
	if _, err := Git(ctx, dir, "init", "--quiet", "--template="); err != nil {
		return dir, err
	}
	if _, err := Git(ctx, dir, "config", "core.hooksPath", filepath.Join(s.Root, "disabled-hooks")); err != nil {
		return dir, err
	}
	if _, err := Git(ctx, dir, "config", "ach.managed", "true"); err != nil {
		return dir, err
	}
	// Local fetch creates independent objects: source GC/removal cannot invalidate an accepted workspace.
	if _, err := Git(ctx, dir, "-c", "protocol.file.allow=always", "fetch", "--quiet", "--no-tags", "--no-write-fetch-head", "--", r.Source, r.Commit); err != nil {
		return dir, err
	}
	if _, err := Git(ctx, dir, "update-ref", "--no-deref", "HEAD", r.Commit); err != nil {
		return dir, err
	}
	if _, err := Git(ctx, dir, "read-tree", r.Commit); err != nil {
		return dir, err
	}
	entries, err := tree(ctx, dir, r.Commit)
	if err != nil {
		return dir, err
	}
	owned, err := os.OpenRoot(dir)
	if err != nil {
		return dir, err
	}
	defer owned.Close()
	// Materialize raw blobs, bypassing smudge filters, export-ignore and line-ending rewriting.
	for _, e := range entries {
		if ctx.Err() != nil {
			return dir, ctx.Err()
		}
		c := gitCommand(ctx, dir, "cat-file", "blob", e.OID)
		data, err := c.Output()
		if err != nil {
			return dir, E("git-object-unavailable", "cannot materialize committed object", 3)
		}
		if bytes.HasPrefix(data, []byte("version https://git-lfs.github.com/spec/v1\n")) {
			return dir, E("lfs-unsupported", "Git LFS pointer source is unsupported", 2)
		}
		path := filepath.FromSlash(e.Path)
		if err = owned.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return dir, err
		}
		if e.Mode == "120000" {
			err = owned.Symlink(string(data), path)
		} else {
			mode := os.FileMode(0600)
			if e.Mode == "100755" {
				mode = 0700
			}
			var file *os.File
			file, err = owned.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
			if err == nil {
				_, err = file.Write(data)
				closeErr := file.Close()
				if err == nil {
					err = closeErr
				}
			}
		}
		if err != nil {
			return dir, Wrap("workspace-write", err)
		}
	}
	return dir, nil
}

type Branch struct {
	Name   string `json:"name"`
	Commit string `json:"commit"`
}
type Commit struct {
	ID      string   `json:"id"`
	Parents []string `json:"parents"`
	Subject string   `json:"subject"`
}
type Changes struct {
	Base      string `json:"base"`
	Head      string `json:"head"`
	MergeBase string `json:"merge_base"`
	Diff      string `json:"diff"`
	Truncated bool   `json:"truncated"`
}

func Branches(ctx context.Context, path string) ([]Branch, error) {
	s, e := Git(ctx, path, "for-each-ref", "--format=%(refname:short)%09%(objectname)", "refs/heads/")
	if e != nil {
		return nil, e
	}
	out := []Branch{}
	for _, l := range strings.Split(s, "\n") {
		p := strings.SplitN(l, "\t", 2)
		if len(p) == 2 {
			out = append(out, Branch{p[0], p[1]})
		}
	}
	return out, nil
}
func Commits(ctx context.Context, path, ref string, offset int) ([]Commit, error) {
	sha, e := ResolveCommit(ctx, path, ref)
	if e != nil {
		return nil, e
	}
	if offset < 0 || offset > 1000000 {
		return nil, E("invalid-offset", "invalid commit offset", 2)
	}
	v, e := Git(ctx, path, "log", "--format=%H%x09%P%x09%s", "--max-count=100", fmt.Sprintf("--skip=%d", offset), sha, "--")
	if e != nil {
		return nil, e
	}
	out := []Commit{}
	for _, l := range strings.Split(v, "\n") {
		p := strings.SplitN(l, "\t", 3)
		if len(p) == 3 {
			out = append(out, Commit{p[0], strings.Fields(p[1]), p[2]})
		}
	}
	return out, nil
}
func Diff(ctx context.Context, path, ref, base string) (Changes, error) {
	out := Changes{}
	sha, e := ResolveCommit(ctx, path, ref)
	if e != nil {
		return out, e
	}
	out.Head = sha
	if base == "" {
		if p, err := CommitConfig(ctx, path, sha); err == nil {
			base = p.DiffBase
		}
		if base == "" {
			base, _ = Git(ctx, path, "symbolic-ref", "--quiet", "refs/remotes/origin/HEAD")
		}
	}
	if base == "" {
		return out, E("diff-base-required", "select a local diff base; no remote fetch is performed", 2)
	}
	out.Base, e = ResolveCommit(ctx, path, base)
	if e != nil {
		return out, e
	}
	out.MergeBase, e = Git(ctx, path, "merge-base", out.Base, out.Head)
	if e != nil {
		return out, E("merge-base-unavailable", "selected histories have no available merge base", 2)
	}
	cmd := gitCommand(ctx, path, "diff", "--no-ext-diff", "--no-textconv", "--no-color", out.MergeBase, out.Head, "--")
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return out, err
	}
	if err = cmd.Start(); err != nil {
		return out, err
	}
	data, readErr := io.ReadAll(io.LimitReader(pipe, 2*1024*1024+1))
	if len(data) > 2*1024*1024 {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if readErr != nil {
		return out, readErr
	}
	if waitErr != nil && len(data) <= 2*1024*1024 {
		return out, E("git-diff-failed", "could not read committed diff", 3)
	}
	if len(data) > 2*1024*1024 {
		data = data[:2*1024*1024]
		out.Truncated = true
	}
	// Git paths/content may contain arbitrary bytes, and the byte budget may
	// split a rune. Normalize only after deciding truncation from raw bytes.
	out.Diff = strings.ToValidUTF8(string(data), "\uFFFD")
	return out, e
}
