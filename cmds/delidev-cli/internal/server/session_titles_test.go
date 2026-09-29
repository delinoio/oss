package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func titleOutputFixture() (domain.AuxiliaryTitleInput, domain.AuxiliaryTitleResult) {
	input := domain.AuxiliaryTitleInput{
		Version: 1, SessionID: domain.NewID(), OperationID: domain.NewID(), NameGeneration: 1,
		OriginalJobID: domain.NewID(), OriginalExecutionID: domain.NewID(), MachineID: domain.NewID(),
		OriginalDeviceID: domain.NewID(), OriginalInstanceID: domain.NewID(), AgentID: domain.NewID(),
		Harness: domain.Codex, NativeVersion: domain.CodexProtocolVersion, Executable: "/private/codex",
		AccountID: domain.NewID(), ConnectionID: domain.NewID(), ProviderID: domain.NewID(),
		ProviderProtocol: domain.OpenAIResponses, ModelID: domain.NewID(), NativeModel: "fixture-model", Prompt: "First input",
	}
	return input, domain.AuxiliaryTitleResult{Version: 1, OperationID: input.OperationID, NameGeneration: input.NameGeneration, Title: "First input", CleanupVerified: true}
}

func TestSessionTitleOutputAllowsUnavailableUsageAndRejectsForeignProject(t *testing.T) {
	input, result := titleOutputFixture()
	output, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := validateSessionTitleOutput(output, input, "")
	if err != nil || accepted.UsageRecord != nil || accepted.Title != result.Title {
		t.Fatalf("valid title without native usage was not accepted: %+v %v", accepted, err)
	}

	counts := domain.NativeTokenCounts{Input: int64ptr(3), Output: int64ptr(2), Total: int64ptr(5)}
	foreignProject := domain.NewID()
	result.UsageRecord = &domain.ResponseUsageRecord{
		Purpose: domain.SessionTitleUsage, SessionID: input.SessionID, ProjectID: foreignProject,
		ExecutionID: input.OriginalExecutionID, AccountID: input.AccountID, ConnectionID: input.ConnectionID,
		ProviderID: input.ProviderID, ModelID: input.ModelID, Harness: input.Harness, Version: input.NativeVersion,
		ThreadID: string(domain.NewID()), TurnID: string(domain.NewID()), Sequence: 1,
		Usage: domain.NativeResponseUsage{ResponseDigest: strings.Repeat("a", 64), Counts: &counts, CostEvidence: domain.UsageCostMissing},
	}
	output, err = json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := validateSessionTitleOutput(output, input, ""); err == nil {
		t.Fatal("title result with usage attributed to a different project was accepted")
	}
}

func int64ptr(value int64) *int64 { return &value }
