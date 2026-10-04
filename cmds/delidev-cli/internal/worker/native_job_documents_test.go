// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"strings"
	"testing"
	"time"
)

func TestNativeCompactionAssignmentPreservesLargeOriginalInput(t *testing.T) {
	_, input := workerCompactionInput(t)
	prompt := strings.Repeat("\"", domain.MaxPromptBytes)
	input.Assignment.Input.Prompt = prompt
	input.Restore.Input.Prompt = prompt
	original, err := json.Marshal(input.Assignment)
	if err != nil {
		t.Fatal(err)
	}
	input.Restore.Continuation.AssignmentInputDigest = executionInputDigest(original)
	input.Restore.Continuation.PromptDigest = executionInputDigest([]byte(prompt))
	input.Restore.Continuation.Previous.AcceptedInputs = []domain.ExecutionInputBinding{domain.BindExecutionInput(input.Assignment.InputID, prompt)}
	if err := input.Validate(); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(input)
	if len(raw) <= 1<<20 {
		t.Fatal("fixture did not exercise original byte bound")
	}
	job := domain.Job{Type: domain.CompactSessionJob, State: domain.JobClaimed, MachineID: input.Assignment.MachineID, InstanceID: domain.NewID(), AssignedDeviceID: domain.NewID(), ParentID: input.SourceJobID, Input: raw, AcceptedAt: time.Now().UTC()}
	document, _ := json.Marshal(job)
	var decoded domain.Job
	if err := decodeNativeAssignment(document, &decoded); err != nil {
		t.Fatal(err)
	}
	var observed domain.SessionCompactionInput
	if domain.DecodeCompactionInput(decoded.Input, &observed) != nil || observed.Assignment.Input.Prompt != prompt || observed.Restore.Input.Prompt != prompt {
		t.Fatal("large immutable assignment changed")
	}
	if decodeNativeAssignment(append(document, []byte(" {}")...), &decoded) == nil {
		t.Fatal("trailing document accepted")
	}
	job.Type = domain.ExecuteSessionJob
	document, _ = json.Marshal(job)
	if decodeNativeAssignment(document, &decoded) == nil {
		t.Fatal("ordinary execution borrowed compaction byte bound")
	}
}
