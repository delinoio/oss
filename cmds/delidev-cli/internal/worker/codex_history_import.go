// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

const maxLegacyCodexHistoryBytes = 64 << 20

// importCodexV1History validates the original checkpoint and original native
// file before copying only that thread's rollout into its account-owned home.
// The original checkpoint and legacy file remain immutable. An ambiguous
// reverted rollout or changed file stays recovery-required. The copy writes the
// account-wide home, so it runs only while the caller holds that home's lock or
// runs under the owner that holds it; without it nothing is written.
func importCodexV1History(ctx context.Context, root string, ref ExecutionCheckpointRef, hold *sharedHomeHold) (CodexExecutionCheckpoint, string, error) {
	checkpoint, err := ReadCodexExecutionCheckpoint(root, ref)
	if err != nil || checkpoint.Version != 1 || !ref.Subscription {
		return CodexExecutionCheckpoint{}, "", executionCheckpointUncertain()
	}
	historyRoot, err := peekHistoryNativeRoot(root, ref.MachineID, domain.Codex, ref.HistoryExecutionID)
	if err != nil {
		return CodexExecutionCheckpoint{}, "", executionCheckpointUncertain()
	}
	legacyHome := filepath.Join(historyRoot, "codex")
	exactPath := ""
	if checkpoint.Native.Context != nil {
		exactPath = checkpoint.Native.Context.RolloutPath
		if err := codex.VerifyContinuationContextRollout(ctx, legacyHome, checkpoint.Native); err != nil {
			return CodexExecutionCheckpoint{}, "", executionCheckpointUncertain()
		}
	}
	proof, err := codex.FindLegacyThreadHistory(ctx, legacyHome, checkpoint.Native.ThreadID, exactPath)
	if err != nil {
		return CodexExecutionCheckpoint{}, "", executionCheckpointUncertain()
	}
	if checkpoint.Native.Context != nil && proof.Digest != checkpoint.Native.Context.RolloutDigest {
		return CodexExecutionCheckpoint{}, "", executionCheckpointUncertain()
	}
	home, err := codexAccountHistoryHome(root, ref.AccountID)
	if err != nil {
		return CodexExecutionCheckpoint{}, "", err
	}
	rel, err := filepath.Rel(legacyHome, proof.Path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || !strings.HasPrefix(rel, "sessions"+string(filepath.Separator)) {
		return CodexExecutionCheckpoint{}, "", executionCheckpointUncertain()
	}
	target := filepath.Join(home, rel)
	if !hold.holds(home) {
		return CodexExecutionCheckpoint{}, "", executionCheckpointUncertain()
	}
	if err := security.PrivateDir(filepath.Dir(target)); err != nil || security.CheckPrivateDir(home) != nil {
		return CodexExecutionCheckpoint{}, "", executionCheckpointUncertain()
	}
	readRoot, err := os.OpenRoot(legacyHome)
	if err != nil {
		return CodexExecutionCheckpoint{}, "", executionCheckpointUncertain()
	}
	defer readRoot.Close()
	before, err := readRoot.Lstat(rel)
	if err != nil || !before.Mode().IsRegular() || before.Size() < 1 || before.Size() > maxLegacyCodexHistoryBytes {
		return CodexExecutionCheckpoint{}, "", executionCheckpointUncertain()
	}
	file, err := readRoot.OpenFile(rel, os.O_RDONLY, 0)
	if err != nil {
		return CodexExecutionCheckpoint{}, "", executionCheckpointUncertain()
	}
	actual, statErr := file.Stat()
	if statErr != nil || !os.SameFile(before, actual) {
		_ = file.Close()
		return CodexExecutionCheckpoint{}, "", executionCheckpointUncertain()
	}
	content, readErr := io.ReadAll(io.LimitReader(file, maxLegacyCodexHistoryBytes+1))
	defer clear(content)
	closeErr := file.Close()
	digest := sha256.Sum256(content)
	if readErr != nil || closeErr != nil || len(content) == 0 || len(content) > maxLegacyCodexHistoryBytes || hex.EncodeToString(digest[:]) != proof.Digest || codex.VerifyThreadHistory(ctx, legacyHome, proof) != nil {
		return CodexExecutionCheckpoint{}, "", executionCheckpointUncertain()
	}
	prior, err := security.ReadPrivate(target, maxLegacyCodexHistoryBytes)
	if err == nil {
		defer clear(prior)
		if !bytes.Equal(prior, content) {
			return CodexExecutionCheckpoint{}, "", executionCheckpointUncertain()
		}
	} else if errors.Is(err, os.ErrNotExist) {
		if err := security.WriteAtomic(target, content); err != nil {
			return CodexExecutionCheckpoint{}, "", executionCheckpointUncertain()
		}
	} else {
		return CodexExecutionCheckpoint{}, "", executionCheckpointUncertain()
	}
	proof.Path = target
	if err := codex.VerifyThreadHistory(ctx, home, proof); err != nil {
		return CodexExecutionCheckpoint{}, "", executionCheckpointUncertain()
	}
	if checkpoint.Native.Context != nil {
		copy := *checkpoint.Native.Context
		copy.RolloutPath = target
		checkpoint.Native.Context = &copy
	}
	return checkpoint, home, nil
}
