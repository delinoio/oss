// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	"os"
	"path/filepath"
)

type revertIntentClaim struct {
	Version     uint32             `json:"version"`
	JobID       domain.ID          `json:"job_id"`
	InputDigest string             `json:"input_digest"`
	Intent      codex.RevertIntent `json:"intent"`
}

func writeRevertIntent(root string, owner domain.ID, i domain.SessionCompactionInput, intent codex.RevertIntent) error {
	raw, e := json.Marshal(revertIntentClaim{1, owner, executionInputDigest(mustForkJSON(i)), intent})
	if e != nil || len(raw) > maxCompactionCheckpoint {
		return domain.CompactionUncertain()
	}
	return security.WriteAtomicOwned(filepath.Join(root, "runtimes", string(i.ActionID), "native-revert-intent.json"), raw)
}
func revertResult(owner domain.ID, i domain.SessionCompactionInput, p codex.CompactedCheckpoint, checkpoint, native string) domain.SessionCompactionResult {
	ids := []domain.NativeIdentity{}
	for _, id := range p.Revert.RetainedTurnIDs {
		ids = append(ids, domain.NativeIdentity(id))
	}
	return domain.SessionCompactionResult{Version: 4, Harness: domain.Codex, ActionID: i.ActionID, ExecutionID: i.Assignment.ExecutionID, Outcome: domain.CompactionSucceeded, CleanupVerified: true, Checkpoint: domain.SessionCompactionRef{Revert: true, ContextRevision: i.Revert.ContextRevision + 1, JobID: owner, ActionID: i.ActionID, ExecutionID: i.Assignment.ExecutionID, CheckpointDigest: checkpoint, NativeDigest: native}, Revert: &domain.SessionRevertResult{Target: *i.Revert, NativeThreadID: domain.NativeIdentity(p.Source.ThreadID), RetainedTurnIDs: ids, HistoryDigest: p.HistoryDigest, ContextRevision: i.Revert.ContextRevision + 1}}
}

type revertCleanupClaim struct {
	Version          uint32    `json:"version"`
	ServerID         domain.ID `json:"server_id"`
	DeviceID         domain.ID `json:"device_id"`
	JobID            domain.ID `json:"job_id"`
	InstanceID       domain.ID `json:"instance_id"`
	Revision         uint64    `json:"revision"`
	AssignmentDigest string    `json:"assignment_digest"`
	InputDigest      string    `json:"input_digest"`
}

func writeRevertCleanup(root string, action domain.ID, p revertCleanupClaim) error {
	raw, err := json.Marshal(p)
	if err != nil {
		return domain.CompactionUncertain()
	}
	return security.WriteAtomicOwned(filepath.Join(root, "runtimes", string(action), "native-revert-cleanup.json"), raw)
}

type revertRecoveryReceipt struct {
	Version  uint32                         `json:"version"`
	Original journal                        `json:"original"`
	Result   domain.SessionCompactionResult `json:"result"`
}

func readRevertRecoveryReceipt(root string, p codexSessionCompactionCheckpoint, original journal, result *domain.SessionCompactionResult) bool {
	if p.Input.Revert == nil {
		return false
	}
	raw, err := security.ReadPrivate(filepath.Join(root, "runtimes", string(p.Input.ActionID), "native-revert-recovery.json"), maxCompactionCheckpoint)
	var receipt revertRecoveryReceipt
	if err != nil || domain.DecodeWithLimit(raw, &receipt, maxCompactionCheckpoint) != nil || receipt.Version != 1 || receipt.Result.Validate() != nil || receipt.Result.Version != 4 || receipt.Result.Checkpoint.JobID != p.JobID {
		return false
	}
	a, _ := json.Marshal(original)
	b, _ := json.Marshal(receipt.Original)
	if !bytes.Equal(a, b) {
		return false
	}
	*result = receipt.Result
	return true
}

func recoverSessionRevert(ctx context.Context, config Config, job domain.Job, request domain.ExecutionRecoveryRequest) (output json.RawMessage, returned error) {
	input := request.Revert.Input
	fail := domain.ExecutionRecoveryUncertain
	originalRaw, err := security.ReadPrivate(filepath.Join(config.Root, "jobs", string(request.JobID)+".json"), 2<<20)
	var original journal
	if err != nil || domain.Decode(originalRaw, &original) != nil || original.Version != 1 || original.JobID != request.JobID || original.InstanceID != request.InstanceID || original.Revision != request.AssignmentRevision || original.Digest != request.AssignmentDigest || original.ReportID.Validate() != nil || original.State != journalStarted && original.State != journalFinished && original.State != journalReported {
		return nil, fail()
	}
	raw, err := security.ReadPrivate(filepath.Join(config.Root, "runtimes", string(input.ActionID), "native-revert-intent.json"), maxCompactionCheckpoint)
	var intent revertIntentClaim
	if err != nil || domain.DecodeWithLimit(raw, &intent, maxCompactionCheckpoint) != nil || intent.Version != 1 || intent.JobID != request.JobID || intent.InputDigest != request.AssignmentInputDigest || intent.Intent.ActionID != input.ActionID || intent.Intent.BeforeTurnID != domain.ID(input.Revert.NativeTurnID) || intent.Intent.Target.ID != input.Revert.InputID || intent.Intent.Target.PromptDigest != input.Revert.Prompt.InputDigest() {
		return nil, fail()
	}
	raw, err = security.ReadPrivate(filepath.Join(config.Root, "runtimes", string(input.ActionID), "native-revert-cleanup.json"), maxCompactionCheckpoint)
	var cleanup revertCleanupClaim
	wanted := revertCleanupClaim{1, request.ServerID, request.DeviceID, request.JobID, request.InstanceID, request.AssignmentRevision, request.AssignmentDigest, request.AssignmentInputDigest}
	if err != nil || domain.Decode(raw, &cleanup) != nil || cleanup != wanted {
		return nil, fail()
	}
	var prep workspace.PrepareRequest
	var manifest workspace.Manifest
	if domain.Decode(input.Assignment.Preparation, &prep) != nil || domain.Decode(input.Assignment.Manifest, &manifest) != nil {
		return nil, fail()
	}
	manager := &workspace.Manager{Root: config.Root, Logger: config.Logger}
	inspection, err := manager.InspectClosedExecution(ctx, workspace.ExecutionPredecessor{JobID: request.JobID, ExecutionID: input.ActionID}, prep, manifest)
	if err != nil {
		return nil, err
	}
	defer func() {
		if e := inspection.Close(); e != nil {
			output, returned = nil, e
		}
	}()
	source, err := codexCheckpointForContinuation(config.Root, input.Restore, manifest)
	if err != nil {
		return nil, err
	}
	a, _ := json.Marshal(source.Native)
	b, _ := json.Marshal(intent.Intent.Source)
	if !bytes.Equal(a, b) {
		return nil, fail()
	}
	installation, err := resolveOriginalStartup(ctx, config, input.SourceJobID, input.Assignment)
	if err != nil {
		return nil, err
	}
	home := filepath.Join(config.Root, "runtimes", string(input.Restore.Continuation.HistoryExecutionID), "codex")
	// Neither protected credentials nor a live API grant may be restored during
	// observation. The original cleanup receipt and closed owner are independent.
	if _, err := os.Lstat(filepath.Join(home, "auth.json")); !errors.Is(err, os.ErrNotExist) {
		return nil, fail()
	}
	recoveryHome := filepath.Join(config.Root, "runtimes", string(config.execution.Assignment.Id))
	env, err := harness.PrivateRuntimeEnvironment(recoveryHome)
	if err != nil {
		return nil, err
	}
	env, err = replaceCodexHome(env, home)
	if err != nil {
		return nil, err
	}
	native, err := codex.Open(ctx, codex.Config{RevertHistory: true, ImageRoot: config.Root, ImageMachineID: request.MachineID, Mode: codex.ThreadProtocol, Version: installation.Version, Home: home, Process: process.Config{Directory: filepath.Join(config.Root, "processes"), OwnerID: domain.ID(config.execution.Assignment.Id), Executable: installation.ResolvedPath, Cwd: inspection.WorkingDirectory(), Env: env, Logger: config.Logger}})
	if err != nil {
		return nil, err
	}
	closed := false
	defer func() {
		if !closed {
			if e := native.Close(); e != nil {
				output, returned = nil, e
			}
		}
	}()
	settings := codex.ThreadSettings{Model: input.Assignment.Configuration.NativeModel, Provider: codexExecutionProvider(input.Assignment.Configuration.Subscription), Effort: input.Assignment.Configuration.Effort, Cwd: inspection.WorkingDirectory(), WorkspaceRoots: nativeWorkspaceRoots(manifest), Instructions: input.Assignment.Configuration.Instructions, Options: input.Assignment.Configuration.Options}
	if _, err = native.ResumeThread(ctx, domain.NewID(), source.Native.ThreadID, settings); err != nil {
		return nil, err
	}
	proof, err := native.ReconcileRevert(ctx, intent.Intent)
	if err != nil {
		return nil, err
	}
	if err = native.Close(); err != nil {
		return nil, err
	}
	closed = true
	if _, err := os.Lstat(filepath.Join(home, "auth.json")); !errors.Is(err, os.ErrNotExist) {
		return nil, fail()
	}
	// The original command/registration claims survive and remain mandatory.
	registration, command, err := readRevertClaims(config.Root, request.JobID, input)
	if err != nil {
		return nil, err
	}
	checkpoint := codexSessionCompactionCheckpoint{Version: 1, ServerID: request.ServerID, DeviceID: request.DeviceID, JobID: request.JobID, AssignmentRevision: request.AssignmentRevision, InstanceID: request.InstanceID, AssignmentDigest: request.AssignmentDigest, RegistrationDigest: registration, CommandDigest: command, Input: input, Native: proof}
	data, err := json.Marshal(checkpoint)
	if err != nil {
		return nil, err
	}
	path, err := compactionCheckpointPath(config.Root, input.ActionID)
	if err != nil {
		return nil, err
	}
	if err = security.PrivateDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	existing, e := security.ReadPrivate(path, maxCompactionCheckpoint)
	if e == nil && !bytes.Equal(existing, data) {
		return nil, fail()
	}
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		return nil, fail()
	}
	if e != nil {
		if err = security.WriteAtomicOwned(path, data); err != nil {
			return nil, err
		}
	}
	nativeRaw, _ := json.Marshal(proof)
	result := revertResult(request.JobID, input, proof, executionInputDigest(data), executionInputDigest(nativeRaw))
	if result.Validate() != nil {
		return nil, fail()
	}
	if err = inspection.Close(); err != nil {
		return nil, err
	}
	receiptRaw, _ := json.Marshal(revertRecoveryReceipt{1, original, result})
	if err = security.WriteAtomicOwned(filepath.Join(config.Root, "runtimes", string(input.ActionID), "native-revert-recovery.json"), receiptRaw); err != nil {
		return nil, err
	}
	return json.Marshal(domain.ExecutionRecoveryEvidence{Version: 2, JobID: request.JobID, ReportID: original.ReportID, Revert: &result})
}
func readRevertClaims(root string, owner domain.ID, input domain.SessionCompactionInput) (string, string, error) {
	dir := filepath.Join(root, "jobs", string(owner))
	registration, err := security.ReadPrivate(filepath.Join(dir, string(compactionRegistrationClaim)), 4<<10)
	if err != nil {
		return "", "", domain.CompactionUncertain()
	}
	command, err := security.ReadPrivate(filepath.Join(dir, string(compactionCommandClaim)), 4<<10)
	if err != nil {
		return "", "", domain.CompactionUncertain()
	}
	a, b := executionInputDigest(registration), executionInputDigest(command)
	if err := readCompactionClaimRecords(root, owner, input, a, b); err != nil {
		return "", "", err
	}
	return a, b, nil
}
func verifyRetainedRevertIntent(root string, p codexSessionCompactionCheckpoint) error {
	if p.Input.Revert == nil {
		return domain.CompactionUncertain()
	}
	raw, err := security.ReadPrivate(filepath.Join(root, "runtimes", string(p.Input.ActionID), "native-revert-intent.json"), maxCompactionCheckpoint)
	var claim revertIntentClaim
	if err != nil || domain.DecodeWithLimit(raw, &claim, maxCompactionCheckpoint) != nil || claim.Version != 1 || claim.JobID != p.JobID || claim.InputDigest != executionInputDigest(mustForkJSON(p.Input)) || claim.Intent.Target.ID != p.Input.Revert.InputID || claim.Intent.Target.PromptDigest != p.Input.Revert.Prompt.InputDigest() || codex.VerifyRevertIntent(claim.Intent, p.Native) != nil {
		return domain.CompactionUncertain()
	}
	raw, err = security.ReadPrivate(filepath.Join(root, "runtimes", string(p.Input.ActionID), "native-revert-cleanup.json"), maxCompactionCheckpoint)
	var cleanup revertCleanupClaim
	if err != nil || domain.Decode(raw, &cleanup) != nil || cleanup != (revertCleanupClaim{1, p.ServerID, p.DeviceID, p.JobID, p.InstanceID, p.AssignmentRevision, p.AssignmentDigest, claim.InputDigest}) {
		return domain.CompactionUncertain()
	}
	return nil
}
