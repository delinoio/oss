package core

import (
	"bufio"
	"bytes"
	"context"
	"errors"
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
	unavailable := func() (Project, error) {
		return Project{}, E("config-unavailable", "commit does not contain "+ProjectFile+"; commit the configuration before running", 2)
	}
	cmd := gitCommand(ctx, path, "cat-file", "blob", sha+":"+ProjectFile)
	output, err := cmd.StdoutPipe()
	if err != nil {
		return unavailable()
	}
	if err = cmd.Start(); err != nil {
		return unavailable()
	}
	// Enforce the parser's byte limit before buffering an arbitrary Git blob.
	// One extra byte distinguishes an exact-limit configuration from overflow.
	b, readErr := io.ReadAll(io.LimitReader(output, maxProjectConfigBytes+1))
	if readErr != nil || len(b) > maxProjectConfigBytes {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if len(b) > maxProjectConfigBytes {
		return ParseProject(b)
	}
	if readErr != nil || waitErr != nil {
		return unavailable()
	}
	return ParseProject(b)
}

type treeEntry struct{ Mode, OID, Path string }

const maxTreeRecordBytes = 1024 * 1024

func walkTree(ctx context.Context, path, sha string, visit func(treeEntry) error) error {
	cmd := gitCommand(ctx, path, "ls-tree", "-rz", "--full-tree", sha)
	output, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err = cmd.Start(); err != nil {
		return err
	}
	err = readTreeEntries(output, visit)
	if err != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if err != nil {
		return err
	}
	if waitErr != nil {
		return E("git-tree-unavailable", "cannot read committed Git tree", 3)
	}
	return nil
}

func readTreeEntries(input io.Reader, visit func(treeEntry) error) error {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), maxTreeRecordBytes+1)
	scanner.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		if i := bytes.IndexByte(data, 0); i >= 0 {
			return i + 1, data[:i], nil
		}
		if atEOF && len(data) > 0 {
			return 0, nil, E("git-tree-invalid", "unterminated Git tree record", 3)
		}
		return 0, nil, nil
	})
	for scanner.Scan() {
		pair := strings.SplitN(scanner.Text(), "\t", 2)
		if len(pair) != 2 {
			return E("git-tree-invalid", "invalid Git tree record", 3)
		}
		meta := strings.Fields(pair[0])
		if len(meta) != 3 || !objectID.MatchString(meta[2]) || !SafeRelative(pair[1]) {
			return E("unsupported-source-path", "source contains an unrepresentable file path", 2)
		}
		for _, component := range strings.Split(pair[1], "/") {
			folded := strings.ToLower(strings.TrimRight(component, " ."))
			if folded == ".git" || strings.HasPrefix(folded, "git~") {
				return E("unsupported-source-path", "source path overlaps managed Git metadata", 2)
			}
		}
		if meta[0] == "160000" {
			return E("submodules-unsupported", "submodule source is unsupported", 2)
		}
		if (meta[0] != "100644" && meta[0] != "100755" && meta[0] != "120000") || meta[1] != "blob" {
			return E("git-tree-invalid", "unsupported Git tree object mode or type", 3)
		}
		if err := visit(treeEntry{meta[0], meta[2], pair[1]}); err != nil {
			return err
		}
	}
	if errors.Is(scanner.Err(), bufio.ErrTooLong) {
		return E("unsupported-source-path", "Git tree record exceeds the supported 1 MiB limit", 2)
	}
	return scanner.Err()
}
func ValidateSource(ctx context.Context, path, sha string) error {
	return walkTree(ctx, path, sha, func(e treeEntry) error {
		if filepath.Base(e.Path) == ".gitattributes" {
			b, err := readAttributeBlob(ctx, path, e.OID)
			if err != nil {
				return err
			}
			if declaresLFSFilter(b) {
				return E("lfs-unsupported", "Git LFS source is unsupported", 2)
			}
		}
		return nil
	})
}

const maxAttributeBytes = 1024 * 1024

func readAttributeBlob(ctx context.Context, path, oid string) (string, error) {
	cmd := gitCommand(ctx, path, "cat-file", "blob", oid)
	output, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	if err = cmd.Start(); err != nil {
		return "", err
	}
	b, readErr := io.ReadAll(io.LimitReader(output, maxAttributeBytes+1))
	if readErr != nil || len(b) > maxAttributeBytes {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if len(b) > maxAttributeBytes {
		return "", E("attributes-too-large", "committed .gitattributes exceeds the supported 1 MiB per-file limit", 2)
	}
	if readErr != nil || waitErr != nil {
		return "", E("git-object-unavailable", "cannot read committed attributes", 3)
	}
	return string(b), nil
}

func declaresLFSFilter(attributes string) bool {
	for _, line := range strings.Split(strings.TrimPrefix(attributes, "\uFEFF"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// The first field is a pattern (or macro name), not an attribute.
		// C-quoted patterns can contain spaces and escaped quotes.
		end := strings.IndexAny(line, " \t\r")
		if line[0] == '"' {
			end = -1
			for i := 1; i < len(line); i++ {
				if line[i] == '\\' {
					i++
				} else if line[i] == '"' {
					end = i + 1
					break
				}
			}
		}
		if end < 0 || end >= len(line) || !strings.ContainsRune(" \t\r", rune(line[end])) {
			continue
		}
		filter := ""
		for _, attribute := range strings.Fields(line[end:]) {
			if strings.HasPrefix(attribute, "filter=") {
				filter = strings.TrimPrefix(attribute, "filter=")
			} else if attribute == "filter" || attribute == "-filter" || attribute == "!filter" {
				filter = ""
			}
		}
		if filter == "lfs" {
			return true
		}
	}
	return false
}

func (s *Store) Prepare(ctx context.Context, r Run) (string, error) {
	dir := filepath.Join(s.Root, "workspaces", r.ID)
	if _, err := os.Lstat(dir); err == nil {
		return "", E("workspace-exists", "a previous owned workspace requires interruption recovery", 3)
	}
	if err := PrivateDir(dir); err != nil {
		return "", err
	}
	format, err := Git(ctx, r.Source, "rev-parse", "--show-object-format=storage")
	if err != nil {
		return dir, err
	}
	if format != "sha1" && format != "sha256" {
		return dir, E("unsupported-object-format", "source Git object format is unsupported", 2)
	}
	if _, err := Git(ctx, dir, "init", "--quiet", "--template=", "--object-format="+format); err != nil {
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
	owned, err := os.OpenRoot(dir)
	if err != nil {
		return dir, err
	}
	defer owned.Close()
	if err := materializeBlobs(ctx, dir, owned, r.Commit); err != nil {
		return dir, err
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
			out = append(out, Commit{p[0], strings.Fields(p[1]), strings.ToValidUTF8(p[2], "\uFFFD")})
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
