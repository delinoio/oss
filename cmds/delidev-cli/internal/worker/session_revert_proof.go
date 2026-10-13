// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"encoding/json"
	"log/slog"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// revertTargetProofSource names where the original target proof came from. It
// is logged only; the values are not part of any wire contract.
type revertTargetProofSource string

const (
	revertProofSourceCheckpoint revertTargetProofSource = "source-checkpoint"
	revertProofSourceTarget     revertTargetProofSource = "target-checkpoint"
	revertProofSourceTextOnly   revertTargetProofSource = "text-only"
)

// resolveRevertTarget builds the HistoricalInput that the harness compares with
// observed native history. The selected-skill digest always comes from the
// immutable retained checkpoint of the exact target input, never from the latest
// source input or from freshly observed history. A target whose prompt selects
// skills fails closed when that original proof is missing or zero, so a zero
// digest can only mean "text-only" when the frozen prompt has no skills.
func resolveRevertTarget(root string, source CodexExecutionCheckpoint, i domain.SessionCompactionInput, logger *slog.Logger) (codex.HistoricalInput, error) {
	revert := i.Revert
	if revert == nil {
		return codex.HistoricalInput{}, domain.CompactionUncertain()
	}
	target := codex.HistoricalInput{ID: revert.InputID, PromptDigest: revert.Prompt.InputDigest()}
	retained, found := codex.HistoricalInput{}, false
	origin := revertProofSourceCheckpoint
	if revert.ExecutionID != "" && revert.ExecutionID != source.Completion.ExecutionID {
		// An earlier execution owns this target; read its own immutable checkpoint.
		origin = revertProofSourceTarget
		retained, found = readRevertTargetCheckpointInput(root, source, i, target)
	} else {
		retained, found = findRetainedInput(source.Native.Inputs, target)
	}
	if found {
		target.SkillDigest = retained.SkillDigest
	}
	if len(revert.Prompt.Skills) != 0 && target.SkillDigest == ([32]byte{}) {
		if logger != nil {
			logger.Warn("codex_revert_target_skill_proof_missing", "action_id", i.ActionID, "input_id", revert.InputID, "proof_source", origin, "proof_found", found, "skills", len(revert.Prompt.Skills))
		}
		return codex.HistoricalInput{}, domain.CompactionUncertain()
	}
	if logger != nil {
		if len(revert.Prompt.Skills) == 0 && target.SkillDigest == ([32]byte{}) {
			origin = revertProofSourceTextOnly
		}
		logger.Info("codex_revert_target_proof_resolved", "action_id", i.ActionID, "input_id", revert.InputID, "proof_source", origin, "skills", len(revert.Prompt.Skills))
	}
	return target, nil
}

// findRetainedInput matches both identity and prompt digest, so an input id
// that was retained with different text cannot lend its skill proof.
func findRetainedInput(inputs []codex.HistoricalInput, target codex.HistoricalInput) (codex.HistoricalInput, bool) {
	for _, input := range inputs {
		if input.ID == target.ID && input.PromptDigest == target.PromptDigest {
			return input, true
		}
	}
	return codex.HistoricalInput{}, false
}

// readRevertTargetCheckpointInput reads the retained checkpoint of the target's
// own execution. The frozen digest binds the exact private bytes; the identity
// checks bind that execution to this session, Worker machine, native thread,
// turn and input so another execution's proof can never be substituted.
func readRevertTargetCheckpointInput(root string, source CodexExecutionCheckpoint, i domain.SessionCompactionInput, target codex.HistoricalInput) (codex.HistoricalInput, bool) {
	revert := i.Revert
	path, err := executionCheckpointPath(root, revert.ExecutionID)
	if err != nil {
		return codex.HistoricalInput{}, false
	}
	raw, err := security.ReadPrivate(path, maxExecutionCheckpointBytes)
	if err != nil || executionInputDigest(raw) != revert.CheckpointDigest {
		return codex.HistoricalInput{}, false
	}
	var checkpoint CodexExecutionCheckpoint
	if domain.Decode(raw, &checkpoint) != nil {
		return codex.HistoricalInput{}, false
	}
	canonical, err := json.Marshal(checkpoint)
	if err != nil || !bytes.Equal(raw, canonical) {
		return codex.HistoricalInput{}, false
	}
	if checkpoint.Version != 1 || checkpoint.SessionID != i.Assignment.SessionID || checkpoint.MachineID != i.Assignment.MachineID || checkpoint.Completion.ExecutionID != revert.ExecutionID || checkpoint.Completion.InputID != revert.InputID || checkpoint.Native.ThreadID != source.Native.ThreadID || domain.NativeIdentity(checkpoint.Native.TurnID) != revert.NativeTurnID || len(checkpoint.Native.Inputs) == 0 || checkpoint.Native.Inputs[0].ID != target.ID {
		return codex.HistoricalInput{}, false
	}
	return findRetainedInput(checkpoint.Native.Inputs, target)
}
