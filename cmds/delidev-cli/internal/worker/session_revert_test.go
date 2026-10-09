// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRevertPrivateCheckpointRequiresIntentCleanupAndOriginalRecoveryReceipt(t *testing.T) {
	for _, scenario := range []string{"original", "observed-recovery", "missing-intent", "missing-cleanup", "changed-cleanup", "changed-prefix", "changed-report"} {
		t.Run(scenario, func(t *testing.T) {
			f := newCheckpointFixture(t)
			if err := f.retain(); err != nil {
				t.Fatal(err)
			}
			source, err := ReadCodexExecutionCheckpoint(f.root, f.ref)
			if err != nil {
				t.Fatal(err)
			}
			action, owner, instance := domain.NewID(), domain.NewID(), domain.NewID()
			progress := domain.ExecutionProgress{JobID: f.jobID, ExecutionID: f.input.ExecutionID, InputID: f.input.InputID, NativeThreadID: string(f.completion.NativeThreadID), NativeTurnID: string(f.completion.NativeTurnID), LastSequence: f.completion.LastSequence, Outcome: domain.ExecutionSucceeded, CleanupVerified: true, Observed: domain.ObservedExecutionSettings{Model: f.input.Configuration.NativeModel, Permission: domain.PermissionReadOnly, ApprovalPolicy: "on-request"}, AcceptedInputs: f.ref.AcceptedInputs}
			restore := f.input
			restore.Version, restore.ExecutionID, restore.InputID = 2, action, domain.NewID()
			restore.ThreadRequestID, restore.TurnRequestID = domain.NewID(), domain.NewID()
			restore.Continuation = &domain.ExecutionContinuation{HistoryExecutionID: f.input.ExecutionID, HistoryRequestID: domain.NewID(), Previous: progress, Completion: f.ref.Completion, AssignmentInputDigest: f.ref.AssignmentInputDigest, InputMode: f.input.Input.Mode, PromptDigest: domain.BindSessionInput(f.input.InputID, f.input.Input).PromptDigest, Intent: domain.ContinueAutomatically}
			input := domain.SessionCompactionInput{Version: 4, ActionID: action, SourceJobID: f.jobID, Assignment: f.input, Restore: restore, Completion: f.ref.Completion, Dispatch: domain.DispatchReady, Revert: &domain.SessionRevertTarget{MessageID: domain.NewID(), InputID: f.input.InputID, NativeTurnID: f.completion.NativeTurnID, Prompt: f.input.Input}}
			if input.Validate() != nil {
				t.Fatal("invalid Revert fixture")
			}
			credential := Credential{ServerID: domain.NewID(), DeviceID: domain.NewID()}
			registration := sessionCompactionRegistration{ActionID: action, ExecutionID: f.input.ExecutionID, RequestID: domain.NewID(), CredentialDigest: strings.Repeat("a", 64)}
			command := sessionCompactionCommand{ActionID: action, ExecutionID: f.input.ExecutionID, RegistrationRequestID: registration.RequestID, CredentialDigest: registration.CredentialDigest}
			if writeCompactionClaim(f.root, owner, compactionRegistrationClaim, registration) != nil || writeCompactionClaim(f.root, owner, compactionCommandClaim, command) != nil {
				t.Fatal("claims")
			}
			history := []json.RawMessage{}
			native := codex.CompactedCheckpoint{Version: 2, Source: source.Native, Records: []codex.CompactionRecord{}, TurnsCount: 0, HistoryDigest: executionInputDigest(mustForkJSON(history)), RolloutPath: filepath.Join(f.root, "private-rollout"), RolloutDigest: strings.Repeat("b", 64), Revert: &codex.RevertedContext{ActionID: action, BeforeTurnID: domain.ID(input.Revert.NativeTurnID), InputID: input.Revert.InputID, PromptDigest: domain.BindSessionInput(f.input.InputID, f.input.Input).PromptDigest, RetainedTurnIDs: []domain.ID{}}}
			intent := codex.RevertIntent{Version: 1, ActionID: action, Source: source.Native, Target: codex.HistoricalInput{ID: input.Revert.InputID, PromptDigest: input.Revert.Prompt.InputDigest()}, BeforeTurnID: domain.ID(input.Revert.NativeTurnID), ExpectedHistory: history}
			p := codexSessionCompactionCheckpoint{Version: 1, ServerID: credential.ServerID, DeviceID: credential.DeviceID, JobID: owner, AssignmentRevision: 2, InstanceID: instance, AssignmentDigest: strings.Repeat("c", 64), RegistrationDigest: compactionClaimDigest(registration), CommandDigest: compactionClaimDigest(command), Input: input, Native: native}
			data, _ := json.Marshal(p)
			nativeRaw, _ := json.Marshal(native)
			result := revertResult(owner, input, native, executionInputDigest(data), executionInputDigest(nativeRaw))
			if result.Validate() != nil {
				t.Fatal("invalid result")
			}
			home := filepath.Join(f.root, "runtimes", string(action))
			security.PrivateDir(home)
			if writeRevertIntent(f.root, owner, input, intent) != nil {
				t.Fatal("intent")
			}
			cleanup := revertCleanupClaim{1, p.ServerID, p.DeviceID, owner, instance, 2, p.AssignmentDigest, executionInputDigest(mustForkJSON(input))}
			if writeRevertCleanup(f.root, action, cleanup) != nil {
				t.Fatal("cleanup")
			}
			j := journal{Version: 1, JobID: owner, InstanceID: instance, Revision: 2, Digest: p.AssignmentDigest, State: journalReported, ReportID: domain.NewID(), Output: mustForkJSON(result)}
			if scenario == "observed-recovery" || scenario == "changed-report" {
				j.Problem = domain.CompactionUncertain()
				j.Output = nil
				j.State = journalStarted
				receipt, _ := json.Marshal(revertRecoveryReceipt{1, j, result})
				security.WriteAtomicOwned(filepath.Join(home, "native-revert-recovery.json"), receipt)
				if scenario == "changed-report" {
					j.ReportID = domain.NewID()
				}
			}
			switch scenario {
			case "missing-intent":
				os.Remove(filepath.Join(home, "native-revert-intent.json"))
			case "missing-cleanup":
				os.Remove(filepath.Join(home, "native-revert-cleanup.json"))
			case "changed-cleanup":
				cleanup.InstanceID = domain.NewID()
				writeRevertCleanup(f.root, action, cleanup)
			case "changed-prefix":
				intent.BeforeTurnID = domain.NewID()
				writeRevertIntent(f.root, owner, input, intent)
			}
			if writeJSON(filepath.Join(f.root, "jobs", string(owner)+".json"), j) != nil {
				t.Fatal("journal")
			}
			security.PrivateDir(filepath.Join(f.root, "compaction-checkpoints"))
			path, _ := compactionCheckpointPath(f.root, action)
			if security.WriteAtomicOwned(path, data) != nil {
				t.Fatal("checkpoint")
			}
			_, err = readCodexSessionCompactionCheckpoint(context.Background(), f.root, credential, restore, result.Checkpoint, source)
			if scenario == "original" || scenario == "observed-recovery" {
				if err != nil {
					t.Fatal("exact original proof rejected", err)
				}
			} else if err == nil {
				t.Fatal("missing or changed original proof accepted")
			}
		})
	}
}
