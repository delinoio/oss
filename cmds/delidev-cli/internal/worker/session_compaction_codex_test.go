// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func TestCodexCompactionCheckpointRequiresOriginalPrivateClaimsAndJournal(t *testing.T) {
	for _, scenario := range []string{"original", "missing-command", "missing-registration", "changed-native-source", "changed-live-item", "changed-history-item", "changed-action-count", "changed-instance", "changed-assignment", "changed-report", "uncertain-journal", "foreign-device"} {
		t.Run(scenario, func(t *testing.T) {
			f := newCheckpointFixture(t)
			if err := f.retain(); err != nil {
				t.Fatal(err)
			}
			source, err := ReadCodexExecutionCheckpoint(f.root, f.ref)
			if err != nil {
				t.Fatal(err)
			}
			progress := domain.ExecutionProgress{JobID: f.jobID, ExecutionID: f.input.ExecutionID, InputID: f.input.InputID, NativeThreadID: string(f.completion.NativeThreadID), NativeTurnID: string(f.completion.NativeTurnID), LastSequence: f.completion.LastSequence, Outcome: domain.ExecutionSucceeded, CleanupVerified: true, Observed: domain.ObservedExecutionSettings{Model: f.input.Configuration.NativeModel, Permission: domain.PermissionReadOnly, ApprovalPolicy: "on-request"}, AcceptedInputs: f.ref.AcceptedInputs}
			restore := f.input
			action, job, instance := domain.NewID(), domain.NewID(), domain.NewID()
			restore.Version, restore.ExecutionID, restore.InputID = 2, action, domain.NewID()
			restore.ThreadRequestID, restore.TurnRequestID = domain.NewID(), domain.NewID()
			restore.Continuation = &domain.ExecutionContinuation{HistoryExecutionID: f.input.ExecutionID, HistoryRequestID: domain.NewID(), Previous: progress, Completion: f.ref.Completion, AssignmentInputDigest: f.ref.AssignmentInputDigest, InputMode: f.input.Input.Mode, PromptDigest: executionInputDigest([]byte(f.input.Input.Prompt)), Intent: domain.ContinueAutomatically}
			input := domain.SessionCompactionInput{Version: 2, ActionID: action, SourceJobID: f.jobID, Restore: restore, Assignment: f.input, Completion: f.ref.Completion, Dispatch: domain.DispatchReady, Intent: domain.ContinueAutomatically}
			if err := input.Validate(); err != nil {
				t.Fatal("invalid Codex action fixture", err)
			}
			credential := Credential{ServerID: domain.NewID(), DeviceID: domain.NewID()}
			registration := sessionCompactionRegistration{ActionID: action, ExecutionID: f.input.ExecutionID, RequestID: domain.NewID(), CredentialDigest: strings.Repeat("ab", 32)}
			command := sessionCompactionCommand{ActionID: action, ExecutionID: f.input.ExecutionID, RegistrationRequestID: registration.RequestID, CredentialDigest: registration.CredentialDigest}
			for _, claim := range []struct {
				name  compactionClaimName
				value any
			}{{compactionRegistrationClaim, registration}, {compactionCommandClaim, command}} {
				if err := writeCompactionClaim(f.root, job, claim.name, claim.value); err != nil {
					t.Fatal(err)
				}
			}
			record := codex.CompactionRecord{ActionID: action, TurnID: domain.NewID(), ItemID: "original-live-item", HistoryItemID: "item-0"}
			native := codex.CompactedCheckpoint{Version: 1, Source: source.Native, Records: []codex.CompactionRecord{record}, HistoryDigest: strings.Repeat("cd", 32), RolloutPath: filepath.Join(f.root, "private-rollout"), RolloutDigest: strings.Repeat("ef", 32), TurnsCount: 2}
			if scenario == "changed-native-source" {
				native.Source.TurnID = domain.NewID()
			}
			p := codexSessionCompactionCheckpoint{Version: 1, ServerID: credential.ServerID, DeviceID: credential.DeviceID, JobID: job, AssignmentRevision: 2, InstanceID: instance, AssignmentDigest: strings.Repeat("12", 32), RegistrationDigest: compactionClaimDigest(registration), CommandDigest: compactionClaimDigest(command), Input: input, Native: native}
			data, _ := json.Marshal(p)
			nativeBytes, _ := json.Marshal(native)
			ref := domain.SessionCompactionRef{JobID: job, ActionID: action, ExecutionID: f.input.ExecutionID, CheckpointDigest: executionInputDigest(data), NativeDigest: executionInputDigest(nativeBytes)}
			result := domain.SessionCompactionResult{Version: 2, Harness: domain.Codex, ActionID: action, ExecutionID: f.input.ExecutionID, Outcome: domain.CompactionSucceeded, CleanupVerified: true, Checkpoint: ref, Codex: &domain.CodexCompactionResult{NativeThreadID: domain.NativeIdentity(source.Native.ThreadID), SourceNativeTurnID: domain.NativeIdentity(source.Native.TurnID), NativeTurnID: domain.NativeIdentity(record.TurnID), LiveItemID: record.ItemID, HistoryItemID: record.HistoryItemID, HistoryDigest: native.HistoryDigest, Actions: 1, Acknowledged: true, LifecycleCompleted: true, ResponseUsages: []domain.NativeResponseUsage{}}}
			j := journal{Version: 1, JobID: job, InstanceID: instance, Revision: 2, Digest: p.AssignmentDigest, State: journalReported, ReportID: domain.NewID()}
			switch scenario {
			case "missing-command":
				os.Remove(filepath.Join(f.root, "jobs", string(job), string(compactionCommandClaim)))
			case "missing-registration":
				os.Remove(filepath.Join(f.root, "jobs", string(job), string(compactionRegistrationClaim)))
			case "changed-live-item":
				result.Codex.LiveItemID = "foreign-live-item"
			case "changed-history-item":
				result.Codex.HistoryItemID = "item-1"
			case "changed-action-count":
				result.Codex.Actions = 2
			case "changed-instance":
				j.InstanceID = domain.NewID()
			case "changed-assignment":
				j.Digest = strings.Repeat("34", 32)
			case "changed-report":
				j.ReportID = ""
			case "uncertain-journal":
				j.State = journalStarted
			case "foreign-device":
				credential.DeviceID = domain.NewID()
			}
			j.Output, _ = json.Marshal(result)
			if err := writeJSON(filepath.Join(f.root, "jobs", string(job)+".json"), j); err != nil {
				t.Fatal(err)
			}
			if err := security.PrivateDir(filepath.Join(f.root, "compaction-checkpoints")); err != nil {
				t.Fatal(err)
			}
			path, err := compactionCheckpointPath(f.root, action)
			if err != nil {
				t.Fatal(err)
			}
			if err := security.WriteAtomic(path, data); err != nil {
				t.Fatal(err)
			}
			restored, err := readCodexSessionCompactionCheckpoint(context.Background(), f.root, credential, restore, ref, source)
			if scenario == "original" {
				if err != nil || restored.Records[0].ActionID != action {
					t.Fatal("original complete checkpoint refused", err)
				}
			} else if err == nil {
				t.Fatal("foreign or incomplete proof authorized native restoration")
			}
		})
	}
}
