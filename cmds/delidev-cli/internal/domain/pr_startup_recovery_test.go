package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPRStartupRecoveryIsAnExclusiveOriginalComparison(t *testing.T) {
	r := ExecutionRecoveryRequest{Version: 1, Startup: &PRStartupRecoveryReference{ExecutionID: NewID(), InputID: NewID()}, ServerID: NewID(), DeviceID: NewID(), InstanceID: NewID(), JobID: NewID(), SessionID: NewID(), MachineID: NewID(), AccountID: NewID(), ConnectionID: NewID(), AssignmentRevision: 2, AssignmentDigest: strings.Repeat("a", 64), AssignmentInputDigest: strings.Repeat("b", 64), ConfigurationDigest: strings.Repeat("c", 64), Preparation: json.RawMessage(`{}`), Manifest: json.RawMessage(`{}`)}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*ExecutionRecoveryRequest){
		func(v *ExecutionRecoveryRequest) { v.Harness = Codex },
		func(v *ExecutionRecoveryRequest) { v.Completion.Version = 1 },
		func(v *ExecutionRecoveryRequest) { v.HistoryExecutionID = NewID() },
		func(v *ExecutionRecoveryRequest) { v.InputMode = ExecuteMode },
		func(v *ExecutionRecoveryRequest) { v.PromptDigest = strings.Repeat("d", 64) },
		func(v *ExecutionRecoveryRequest) { v.AssignmentRevision = 0 },
		func(v *ExecutionRecoveryRequest) { v.DeviceID = "" },
		func(v *ExecutionRecoveryRequest) { v.AssignmentDigest = strings.Repeat("A", 64) },
		func(v *ExecutionRecoveryRequest) { v.Manifest = nil },
		func(v *ExecutionRecoveryRequest) { v.JobID = v.Startup.ExecutionID },
	} {
		copy := r
		change(&copy)
		if err := copy.Validate(); err == nil {
			t.Fatal("mixed or incomplete startup comparison was accepted")
		}
	}
}
