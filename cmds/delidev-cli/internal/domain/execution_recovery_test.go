package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRecoveryHarnessSelectionPreservesHistoricalCodexWire(t *testing.T) {
	creation := NewID()
	r := ExecutionRecoveryRequest{
		Version: 1, ServerID: NewID(), DeviceID: NewID(), InstanceID: NewID(), JobID: NewID(), SessionID: NewID(), MachineID: NewID(),
		AssignmentRevision: 2, AssignmentDigest: strings.Repeat("ab", 32), AssignmentInputDigest: strings.Repeat("bc", 32), ConfigurationDigest: strings.Repeat("cd", 32),
		AccountID: NewID(), ConnectionID: NewID(), HistoryExecutionID: NewID(), InputMode: ExecuteMode, PromptDigest: strings.Repeat("ef", 32),
		Preparation: json.RawMessage(`{}`), Manifest: json.RawMessage(`{}`),
		Completion: ExecutionCompletion{Version: 1, ExecutionID: NewID(), InputID: NewID(), NativeThreadID: NativeIdentity(NewID()), NativeTurnID: NativeIdentity(NewID()), LastSequence: 3, Outcome: ExecutionSucceeded, CleanupVerified: true},
	}
	raw, _ := json.Marshal(r)
	if r.Validate() != nil || r.NativeHarness() != Codex || strings.Contains(string(raw), "harness") || strings.Contains(string(raw), "opencode") {
		t.Fatal("historical Codex recovery wire changed")
	}
	r.Completion.NativeThreadID, r.Completion.NativeTurnID = "ses_01960dcbe1faabcdefghijklmn", "msg_01960dcbe1faABCDEFGHIJKLMN"
	if r.Validate() == nil {
		t.Fatal("native ID spelling inferred recovery authority")
	}
	r.Harness, r.OpenCode = OpenCode, &OpenCodeRecoveryReference{ClaimVersion: 1, CreationRequestID: creation, BindingRequestID: creation, InputRequestID: NewID()}
	r.HistoryExecutionID = r.Completion.ExecutionID
	if r.Validate() != nil {
		t.Fatal("explicit original OpenCode comparison profile rejected")
	}
	completion := r.Completion
	completion.Version, completion.NativeCheckpointDigest = 2, strings.Repeat("12", 32)
	evidence := ExecutionRecoveryEvidence{Version: 1, JobID: r.JobID, ReportID: NewID(), Completion: completion}
	if evidence.Validate(r) != nil {
		t.Fatal("original native completion lost selected harness")
	}
	for _, scenario := range []string{"missing-metadata", "codex", "claude", "grok", "unknown", "creation", "binding", "input", "version", "completion", "history"} {
		t.Run(scenario, func(t *testing.T) {
			next, ref := r, *r.OpenCode
			next.OpenCode = &ref
			switch scenario {
			case "missing-metadata":
				next.OpenCode = nil
			case "codex":
				next.Harness = Codex
			case "claude":
				next.Harness = ClaudeCode
			case "grok":
				next.Harness = GrokBuild
			case "unknown":
				next.Harness = "future"
			case "creation":
				ref.CreationRequestID = NewID()
			case "binding":
				ref.BindingRequestID = ref.InputRequestID
			case "input":
				ref.InputRequestID = ref.CreationRequestID
			case "version":
				ref.ClaimVersion = 2
			case "completion":
				next.Completion.NativeTurnID = NativeIdentity(NewID())
			case "history":
				next.HistoryExecutionID = ""
			}
			if next.Validate() == nil || evidence.Validate(next) == nil {
				t.Fatal("foreign/incomplete comparison authority accepted")
			}
		})
	}
	r.OpenCode.ClaimVersion, r.OpenCode.BindingRequestID = 2, NewID()
	r.HistoryExecutionID = NewID()
	if r.Validate() != nil || evidence.Validate(r) != nil {
		t.Fatal("resumed binding replaced original creation marker")
	}
}
