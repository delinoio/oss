// SPDX-License-Identifier: Apache-2.0
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Complete immutable assignment/continuation evidence, without an installed
// native harness or account. Padding stays within original workspace JSON.
func restoreCompactionInput(t *testing.T, session, machine domain.ID, size int) json.RawMessage {
	t.Helper()
	source := failedForkDeletionInput(t, session, machine)
	assignment, done := source.SourceAssignment, source.Completion
	assignment.Preparation = json.RawMessage(`{"padding":""}`)
	action := domain.NewID()
	restored := assignment
	restored.Version = 2
	restored.ExecutionID = action
	restored.InputID = domain.NewID()
	restored.ThreadRequestID = domain.NewID()
	restored.TurnRequestID = domain.NewID()
	original, _ := json.Marshal(assignment)
	sum := sha256.Sum256(original)
	progress := source.Progress
	progress.Observed = domain.ObservedExecutionSettings{Model: assignment.Configuration.NativeModel, Permission: domain.PermissionReadOnly, ApprovalPolicy: "on-request"}
	progress.AcceptedInputs = []domain.ExecutionInputBinding{domain.BindExecutionInput(assignment.InputID, assignment.Input.Prompt)}
	restored.Continuation = &domain.ExecutionContinuation{HistoryExecutionID: assignment.ExecutionID, HistoryRequestID: domain.NewID(), Previous: progress, Completion: done, AssignmentInputDigest: hex.EncodeToString(sum[:]), InputMode: assignment.Input.Mode, PromptDigest: domain.BindSessionInput(assignment.InputID, assignment.Input).PromptDigest, Intent: domain.ContinueAutomatically}
	input := domain.SessionCompactionInput{Version: 2, ActionID: action, SourceJobID: source.SourceJobID, Assignment: assignment, Restore: restored, Completion: done, Dispatch: domain.DispatchReady, Intent: domain.ContinueAutomatically}
	raw, _ := json.Marshal(input)
	if (size-len(raw))%2 != 0 {
		input.Completion.LastSequence = 9
		input.Restore.Continuation.Completion.LastSequence = 9
		input.Restore.Continuation.Previous.LastSequence = 9
		raw, _ = json.Marshal(input)
	}
	if size < len(raw) {
		t.Fatal("fixture size too small", size, len(raw))
	}
	padding, _ := json.Marshal(struct {
		Padding string `json:"padding"`
	}{strings.Repeat("x", (size-len(raw))/2)})
	input.Assignment.Preparation = padding
	input.Restore.Preparation = padding
	original, _ = json.Marshal(input.Assignment)
	sum = sha256.Sum256(original)
	input.Restore.Continuation.AssignmentInputDigest = hex.EncodeToString(sum[:])
	if err := input.Validate(); err != nil {
		t.Fatal("invalid complete compaction fixture", err)
	}
	raw, err := json.Marshal(input)
	if err != nil || len(raw) != size {
		t.Fatal("fixture bound mismatch", len(raw), size, err)
	}
	return raw
}

func TestRestoreCompactionDecoderRejectsTrailingInputAndEnvelope(t *testing.T) {
	input := restoreCompactionInput(t, domain.NewID(), domain.NewID(), 512<<10)
	var typed domain.SessionCompactionInput
	if domain.DecodeCompactionInput(append(append([]byte(nil), input...), []byte(` {}`)...), &typed) == nil {
		t.Fatal("typed input accepted trailing document")
	}
	raw, _ := json.Marshal(domain.Job{Type: domain.CompactSessionJob, State: domain.JobQueued, MachineID: domain.NewID(), Input: input})
	if _, err := decodeRestoreJob(append(raw, []byte(` {}`)...)); err == nil {
		t.Fatal("restore decoder accepted trailing outer document")
	}
}
