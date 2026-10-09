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
	if r.Validate() != nil || r.NativeHarness() != Codex || strings.Contains(string(raw), "harness") || strings.Contains(string(raw), "opencode") || strings.Contains(string(raw), "claude") {
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

func TestClaudeRecoveryRequiresExplicitOriginalComparison(t *testing.T) {
	r := ExecutionRecoveryRequest{
		Version: 1, Harness: ClaudeCode, ServerID: NewID(), DeviceID: NewID(), InstanceID: NewID(), JobID: NewID(), SessionID: NewID(), MachineID: NewID(),
		AssignmentRevision: 2, AssignmentDigest: strings.Repeat("ab", 32), AssignmentInputDigest: strings.Repeat("bc", 32), ConfigurationDigest: strings.Repeat("cd", 32),
		AccountID: NewID(), ConnectionID: NewID(), InputMode: ExecuteMode, PromptDigest: strings.Repeat("ef", 32), Preparation: json.RawMessage(`{}`), Manifest: json.RawMessage(`{}`),
		Completion: ExecutionCompletion{Version: 1, ExecutionID: NewID(), InputID: NewID(), NativeTurnID: NativeIdentity(NewID()), LastSequence: 3, Outcome: ExecutionSucceeded, CleanupVerified: true},
		Claude:     &ClaudeRecoveryReference{ClaimVersion: 1, Version: ClaudeProtocolVersion, Executable: "/fixture/claude", Model: "fixture-model", Permission: ClaudePermissionDefault, InstructionsDigest: strings.Repeat("12", 32), BindingRequestID: NewID(), InputRequestID: NewID()},
	}
	r.Completion.NativeThreadID, r.HistoryExecutionID = NativeIdentity(r.SessionID), r.Completion.ExecutionID
	if err := r.Validate(); err != nil {
		t.Fatal("original first comparison rejected", err)
	}
	completion := r.Completion
	completion.Version, completion.NativeCheckpointDigest = 2, strings.Repeat("34", 32)
	evidence := ExecutionRecoveryEvidence{Version: 1, JobID: r.JobID, ReportID: NewID(), Completion: completion}
	if evidence.Validate(r) != nil {
		t.Fatal("original completion lost Claude comparison")
	}
	for _, scenario := range []string{"implicit", "codex", "opencode", "missing", "mixed", "claim-version", "protocol", "executable", "model", "effort", "permission", "instructions", "instructions-uppercase", "request", "first-history", "thread", "stopped", "multiple-inputs"} {
		t.Run(scenario, func(t *testing.T) {
			next, native := r, *r.Claude
			next.Claude = &native
			switch scenario {
			case "implicit":
				next.Harness = ""
			case "codex":
				next.Harness = Codex
			case "opencode":
				next.Harness = OpenCode
			case "missing":
				next.Claude = nil
			case "mixed":
				next.OpenCode = &OpenCodeRecoveryReference{}
			case "claim-version":
				native.ClaimVersion = 3
			case "protocol":
				native.Version = "invalid/version"
			case "executable":
				native.Executable = ""
			case "model":
				native.Model = ""
			case "effort":
				native.Effort = "invalid\x00effort"
			case "permission":
				native.Permission = "unknown"
			case "instructions":
				native.InstructionsDigest = ""
			case "instructions-uppercase":
				native.InstructionsDigest = strings.Repeat("AB", 32)
			case "request":
				native.BindingRequestID = native.InputRequestID
			case "first-history":
				next.HistoryExecutionID = NewID()
			case "thread":
				next.Completion.NativeThreadID = NativeIdentity(NewID())
			case "stopped":
				next.Completion.Outcome = ExecutionStopped
			case "multiple-inputs":
				next.AcceptedInputs = []ExecutionInputBinding{{InputID: r.Completion.InputID, PromptDigest: r.PromptDigest}, BindExecutionInput(NewID(), "Another input")}
			}
			if next.Validate() == nil || evidence.Validate(next) == nil {
				t.Fatal("incomplete or foreign Claude recovery accepted")
			}
		})
	}
	r.Completion.Outcome, evidence.Completion.Outcome = ExecutionFailed, ExecutionFailed
	if r.Validate() != nil || evidence.Validate(r) != nil {
		t.Fatal("settled failed outcome lost comparison-only recovery")
	}
	r.Claude.ClaimVersion, r.HistoryExecutionID = 2, NewID()
	if r.Validate() != nil || evidence.Validate(r) != nil {
		t.Fatal("resumed original history rejected")
	}
}
