// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

func directoryCheckpointFixture(t *testing.T) (string, Credential, codexDirectoryCheckpoint, domain.SessionDirectoryRef) {
	t.Helper()
	f := newCheckpointFixture(t)
	manifest := workspace.Manifest{SessionID: f.input.SessionID, MachineID: f.input.MachineID, Type: domain.GeneralChat, State: workspace.Ready, PrimaryPath: f.root, Repositories: []workspace.PreparedRepository{}}
	f.input.Manifest, _ = json.Marshal(manifest)
	f.job.Input, _ = json.Marshal(f.input)
	f.ref.AssignmentInputDigest = executionInputDigest(f.job.Input)
	if err := f.retain(); err != nil {
		t.Fatal(err)
	}
	progress := domain.ExecutionProgress{JobID: f.jobID, ExecutionID: f.input.ExecutionID, InputID: f.input.InputID, NativeThreadID: string(f.completion.NativeThreadID), NativeTurnID: string(f.completion.NativeTurnID), LastSequence: f.completion.LastSequence, Outcome: domain.ExecutionSucceeded, CleanupVerified: true, Observed: domain.ObservedExecutionSettings{Model: f.input.Configuration.NativeModel, Permission: domain.PermissionReadOnly, ApprovalPolicy: "on-request"}}
	input := domain.SessionDirectoryInput{RequestingActor: domain.Principal{Type: domain.OwnerDevice}, Version: 1, RequestID: domain.NewID(), GenerationID: domain.NewID(), SourceJobID: f.jobID, HistoryExecutionID: f.input.ExecutionID, Assignment: f.input, Completion: f.ref.Completion, PreviousExecution: progress, RelativePath: "nested"}
	if err := input.Validate(); err != nil {
		t.Fatal("invalid fixture", err)
	}
	source, err := directorySourceCheckpoint(f.root, input, manifest)
	if err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(f.root, "nested")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal(err)
	}
	selected := source.Native
	selected.Effective.Cwd = nested
	selected.Effective.WorkspaceRoots = []string{f.root}
	inputRaw, _ := json.Marshal(input)
	owner := directoryOwnership{ServerID: domain.NewID(), DeviceID: domain.NewID(), JobID: domain.NewID(), InstanceID: domain.NewID(), Revision: 2, AssignmentDigest: strings.Repeat("a", 64), InputDigest: executionInputDigest(inputRaw)}
	registration := directoryRegistration{Ownership: owner, RequestID: domain.NewID(), CredentialDigest: strings.Repeat("b", 64)}
	registrationRaw, _ := json.Marshal(registration)
	intent := directoryNativeIntent{Ownership: owner, GenerationID: input.GenerationID, RegistrationDigest: executionInputDigest(registrationRaw), Native: codex.DirectoryIntent{RequestID: input.RequestID, Source: source.Native, Destination: nested, WorkspaceRoots: []string{f.root}}}
	intentRaw, _ := json.Marshal(intent)
	if err := writeDirectoryClaim(f.root, owner.JobID, directoryRegistrationClaim, registration); err != nil {
		t.Fatal(err)
	}
	if err := writeDirectoryClaim(f.root, owner.JobID, directoryNativeClaim, intent); err != nil {
		t.Fatal(err)
	}
	p := codexDirectoryCheckpoint{Version: 1, Ownership: owner, Input: input, RegistrationDigest: executionInputDigest(registrationRaw), NativeIntentDigest: executionInputDigest(intentRaw), Source: source, Before: source.Native, Selected: selected, Reload: codex.DirectoryReloadEvidence{ConfigDigest: strings.Repeat("c", 64), Instructions: []codex.DirectoryInstructionEvidence{}}}
	ref, err := retainDirectoryCheckpoint(f.root, p)
	if err != nil {
		t.Fatal(err)
	}
	result := domain.SessionDirectoryResult{Version: 1, RequestID: input.RequestID, GenerationID: input.GenerationID, ExecutionID: f.input.ExecutionID, Checkpoint: ref, CleanupVerified: true}
	output, _ := json.Marshal(result)
	j := journal{Version: 1, JobID: owner.JobID, InstanceID: owner.InstanceID, Revision: owner.Revision, Digest: owner.AssignmentDigest, State: journalFinished, ReportID: domain.NewID(), Output: output}
	if err := writeJSON(filepath.Join(f.root, "jobs", string(owner.JobID)+".json"), j); err != nil {
		t.Fatal(err)
	}
	return f.root, Credential{ServerID: owner.ServerID, DeviceID: owner.DeviceID}, p, ref
}

func TestDirectoryCheckpointRequiresOriginalClaimsJournalAndSource(t *testing.T) {
	for _, changed := range []string{"none", "native claim absent", "registration changed", "journal uncertain", "source missing", "foreign device", "generation changed", "checkpoint changed"} {
		t.Run(changed, func(t *testing.T) {
			root, credential, p, ref := directoryCheckpointFixture(t)
			switch changed {
			case "native claim absent":
				path, _ := directoryClaimPath(root, ref.JobID, directoryNativeClaim, false)
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "registration changed":
				path, _ := directoryClaimPath(root, ref.JobID, directoryRegistrationClaim, false)
				if err := os.WriteFile(path, []byte(`{}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "journal uncertain":
				path := filepath.Join(root, "jobs", string(ref.JobID)+".json")
				var j journal
				raw, _ := os.ReadFile(path)
				_ = json.Unmarshal(raw, &j)
				j.State = journalStarted
				if err := writeJSON(path, j); err != nil {
					t.Fatal(err)
				}
			case "source missing":
				path, _ := executionCheckpointPath(root, p.Source.Completion.ExecutionID)
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "foreign device":
				credential.DeviceID = domain.NewID()
			case "generation changed":
				ref.GenerationID = domain.NewID()
			case "checkpoint changed":
				path, _ := directoryCheckpointPath(root, ref.GenerationID, false)
				if err := os.WriteFile(path, []byte(`{}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			got, err := readDirectoryCheckpoint(root, credential, p.Input.Assignment, ref)
			if changed == "none" {
				if err != nil || !sameDirectoryValue(got, p) {
					t.Fatal("retained immutable generation", err)
				}
			} else if err == nil {
				t.Fatal("lost directory proof granted authority")
			}
		})
	}
}
func TestDirectoryNativeClaimCannotBeReplaced(t *testing.T) {
	root, _, p, _ := directoryCheckpointFixture(t)
	if err := writeDirectoryClaim(root, p.Ownership.JobID, directoryNativeClaim, map[string]string{"replacement": "new"}); err == nil {
		t.Fatal("original native intent overwritten")
	}
}
