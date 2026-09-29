package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCreateSessionNameModePreservesManualReceiptShape(t *testing.T) {
	base := CreateSession{
		Name: "Original manual name", AgentID: NewID(), MachineID: NewID(),
		Workspace: GeneralChat, Prompt: "first message", Mode: ExecuteMode, Source: ManualSession,
	}
	manual, err := json.Marshal(base)
	if err != nil || strings.Contains(string(manual), "name_mode") || base.Validate() != nil {
		t.Fatalf("omitted mode changed the legacy request shape: %s %v", manual, err)
	}
	automatic := base
	automatic.Name = ""
	automatic.NameMode = AutomaticSessionName
	raw, err := json.Marshal(automatic)
	if err != nil || automatic.Validate() != nil || !strings.Contains(string(raw), `"name_mode":"automatic"`) {
		t.Fatalf("automatic session creation was rejected or not explicit: %s %v", raw, err)
	}
	automatic.Name = "Caller supplied title"
	if SafeError(automatic.Validate()).Code != InvalidArgument {
		t.Fatal("automatic mode accepted a caller-selected title")
	}
	automatic.Name = ""
	automatic.NameMode = "unknown"
	if SafeError(automatic.Validate()).Code != InvalidArgument {
		t.Fatal("unknown name mode was accepted")
	}
}

func TestAuxiliaryTitleResultAllowsMissingUsageButRejectsMisattribution(t *testing.T) {
	input := AuxiliaryTitleInput{
		Version: 1, SessionID: NewID(), OperationID: NewID(), NameGeneration: 1,
		OriginalJobID: NewID(), OriginalExecutionID: NewID(), MachineID: NewID(),
		OriginalDeviceID: NewID(), OriginalInstanceID: NewID(), AgentID: NewID(),
		Harness: Codex, NativeVersion: CodexProtocolVersion, Executable: "/bin/codex",
		AccountID: NewID(), ConnectionID: NewID(), ProviderID: NewID(), ProviderProtocol: OpenAIResponses,
		ModelID: NewID(), NativeModel: "fixture-model", Prompt: "first message",
	}
	result := AuxiliaryTitleResult{Version: 1, OperationID: input.OperationID, NameGeneration: input.NameGeneration, Title: "A concise title", CleanupVerified: true}
	if err := result.Validate(input); err != nil {
		t.Fatalf("valid title without usage telemetry failed: %v", err)
	}
	usage := ResponseUsageRecord{
		Purpose: SessionTitleUsage, SessionID: input.SessionID, ExecutionID: input.OriginalExecutionID,
		AccountID: input.AccountID, ConnectionID: input.ConnectionID, ProviderID: input.ProviderID,
		ModelID: input.ModelID, Harness: input.Harness, Version: input.NativeVersion,
		ThreadID: string(NewID()), TurnID: string(NewID()), Sequence: 1,
		Usage: NativeResponseUsage{ResponseDigest: strings.Repeat("a", 64), CostEvidence: UsageCostMissing},
	}
	result.UsageRecord = &usage
	if err := result.Validate(input); err != nil {
		t.Fatalf("valid separately attributed title usage failed: %v", err)
	}
	usage.Purpose = ConversationUsage
	result.UsageRecord = &usage
	if SafeError(result.Validate(input)).Code != InvalidArgument {
		t.Fatal("title result accepted conversation-purpose usage")
	}
}
