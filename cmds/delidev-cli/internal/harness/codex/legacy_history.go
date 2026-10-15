// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bufio"
	"context"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

var legacyRolloutName = regexp.MustCompile(`^rollout-[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}-[0-9]{2}-[0-9]{2}-([0-9a-f-]{36})(?:_([0-9a-f-]{36}))?\.jsonl$`)

// FindLegacyThreadHistory inspects only the original private history tree of
// an already validated version-1 checkpoint. Codex rust-v0.159.2 names the
// thread ID in a rollout basename and stores it again in session_meta. A
// reverted thread can have several rollout IDs; without an exact checkpoint
// path this importer leaves that history for recovery instead of guessing.
func FindLegacyThreadHistory(ctx context.Context, home string, thread domain.ID, exactPath string) (ThreadHistoryProof, error) {
	if thread.Validate() != nil || security.CheckPrivateDir(home) != nil || security.CheckPrivateDir(filepath.Join(home, "sessions")) != nil {
		return ThreadHistoryProof{}, continuationUncertain()
	}
	var candidates []string
	visited := 0
	err := filepath.WalkDir(filepath.Join(home, "sessions"), func(path string, entry fs.DirEntry, visitErr error) error {
		visited++
		if visitErr != nil || ctx.Err() != nil || visited > 4096 || entry == nil || entry.Type()&os.ModeSymlink != 0 {
			return continuationUncertain()
		}
		if entry.IsDir() {
			if security.CheckPrivateDir(path) != nil {
				return continuationUncertain()
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return continuationUncertain()
		}
		name := legacyRolloutName.FindStringSubmatch(entry.Name())
		if name == nil || domain.ID(name[1]) != thread {
			return nil
		}
		if exactPath != "" && path != exactPath {
			return nil
		}
		if !legacySessionMetaMatches(home, path, thread) {
			return continuationUncertain()
		}
		candidates = append(candidates, path)
		return nil
	})
	if err != nil || len(candidates) != 1 {
		return ThreadHistoryProof{}, continuationUncertain()
	}
	digest, err := forkRolloutDigest(ctx, home, candidates[0])
	if err != nil {
		return ThreadHistoryProof{}, continuationUncertain()
	}
	return ThreadHistoryProof{ThreadID: thread, Path: candidates[0], Digest: hex.EncodeToString(digest[:])}, nil
}

func legacySessionMetaMatches(home, path string, thread domain.ID) bool {
	rel, err := filepath.Rel(home, path)
	if err != nil || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return false
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		return false
	}
	defer root.Close()
	before, err := root.Lstat(rel)
	if err != nil || !before.Mode().IsRegular() {
		return false
	}
	file, err := root.OpenFile(rel, os.O_RDONLY|forkNonblockFlag(), 0)
	if err != nil {
		return false
	}
	defer file.Close()
	actual, err := file.Stat()
	if err != nil || !os.SameFile(before, actual) {
		return false
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for i := 0; i < 8 && scanner.Scan(); i++ {
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var record struct {
			Type    string `json:"type"`
			Payload struct {
				ID domain.ID `json:"id"`
			} `json:"payload"`
		}
		return json.Unmarshal(line, &record) == nil && record.Type == "session_meta" && record.Payload.ID == thread && scanner.Err() == nil
	}
	return false
}
