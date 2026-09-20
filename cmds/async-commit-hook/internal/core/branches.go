package core

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"strings"
)

const branchRecordLimit = 64 << 10
const branchPageBytes = 128 << 10

// BranchPage streams raw refs, retaining one bounded page. Use lexical keysets
// rather than Git's newer --start-after option to support Ubuntu 22.04's Git.
func BranchPage(ctx context.Context, path, cursor string, limit int) (result []Branch, next string, err error) {
	defer preserveGitCancellation(ctx, &err)
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 50 {
		return nil, "", E("invalid-limit", "branch page limit must be 1..50", 2)
	}
	scope := "branches-v1:" + Hash([]byte(path)) + "\x00"
	after := ""
	if cursor != "" {
		if len(cursor) > 2*branchRecordLimit {
			return nil, "", E("invalid-cursor", "invalid branch cursor", 2)
		}
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || !strings.HasPrefix(string(raw), scope) {
			return nil, "", E("invalid-cursor", "branch cursor belongs to another worktree or is malformed", 2)
		}
		after = strings.TrimPrefix(string(raw), scope)
		if !strings.HasPrefix(after, "refs/heads/") || len(after) > branchRecordLimit || strings.ContainsAny(after, "\x00\r\n\t") {
			return nil, "", E("invalid-cursor", "invalid branch cursor", 2)
		}
	}
	owned, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := gitCommand(owned, path, "for-each-ref", "--sort=refname", "--format=%(refname)%09%(objectname)", "refs/heads/")
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, "", err
	}
	if err = cmd.Start(); err != nil {
		return nil, "", E("git-error", "cannot list branches", 3)
	}
	waited := false
	defer func() {
		cancel()
		if !waited {
			_ = cmd.Wait()
		}
	}()
	reader := bufio.NewReaderSize(pipe, branchRecordLimit)
	out := []Branch{}
	size, last := 0, ""
	for {
		line, err := reader.ReadSlice('\n')
		if errors.Is(err, io.EOF) && len(line) == 0 {
			err = cmd.Wait()
			waited = true
			if err != nil {
				return nil, "", E("git-error", "cannot list branches", 3)
			}
			return out, "", nil
		}
		if err != nil {
			return nil, "", E("git-error", "branch record exceeds the 64 KiB limit or could not be read", 3)
		}
		ref, commit, ok := strings.Cut(strings.TrimSuffix(string(line), "\n"), "\t")
		if !ok || !strings.HasPrefix(ref, "refs/heads/") || !objectID.MatchString(commit) {
			return nil, "", E("git-error", "invalid branch record", 3)
		}
		if ref <= after {
			continue
		}
		if len(out) == limit || size+len(line) > branchPageBytes {
			return out, base64.RawURLEncoding.EncodeToString([]byte(scope + last)), nil
		}
		out = append(out, Branch{Name: strings.TrimPrefix(ref, "refs/heads/"), Commit: commit})
		last = ref
		size += len(line)
	}
}

// Branch identities encode raw bytes independently of lossy display labels.
func branchIdentity(worktree, branch string) string {
	if branch == "" {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString([]byte("branch-v1:" + worktree + "\x00" + branch))
}

func branchSelection(worktree, id, legacy string, source bool) (string, error) {
	if id == "" {
		return legacy, nil
	}
	invalid := func() (string, error) {
		return "", E("invalid-branch", "branch identity is malformed, belongs to another worktree or conflicts with a legacy selector", 2)
	}
	if legacy != "" || !ValidID(worktree) || len(id) > 2*branchRecordLimit {
		return invalid()
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(id)
	prefix := "branch-v1:" + worktree + "\x00"
	if err != nil || !strings.HasPrefix(string(raw), prefix) {
		return invalid()
	}
	branch := strings.TrimPrefix(string(raw), prefix)
	if branch == "" || len(branch) > branchRecordLimit || strings.ContainsAny(branch, "\x00\r\n\t") {
		return invalid()
	}
	if source {
		return "refs/heads/" + branch, nil
	}
	return branch, nil
}
