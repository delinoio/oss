package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestExecutionJobInputRecheckBoundary(t *testing.T) {
	limit := MaxExecutionJobDocumentBytes - ExecutionJobWrapperReserveBytes
	if err := RequireExecutionJobInput(json.RawMessage(strings.Repeat("1", limit)), nil); err != nil {
		t.Fatal("input exactly at the budget was rejected", err)
	}
	err := RequireExecutionJobInput(json.RawMessage(strings.Repeat("1", limit+1)), nil)
	if SafeError(err).Code != ResourceExhausted {
		t.Fatal("input one byte above the budget was accepted", err)
	}
}

func TestExecutionJobBudgetBoundary(t *testing.T) {
	fixed := ExecutionJobInputReserveBytes + ExecutionConfigurationReserveBytes + ExecutionJobWrapperReserveBytes
	budget := ExecutionJobBudget{PreparationBytes: 100, ManifestBytes: 200, InputBytes: 300, ConfigurationBytes: 400, ExtraBytes: 50}
	budget.InputBytes += MaxExecutionJobDocumentBytes - fixed - 1050
	if budget.JobDocumentBytes() != MaxExecutionJobDocumentBytes {
		t.Fatal("budget does not sum every component", budget.JobDocumentBytes())
	}
	if err := budget.Require(nil); err != nil {
		t.Fatal("budget exactly at the limit was rejected", err)
	}
	budget.ExtraBytes++
	err := budget.Require(nil)
	if SafeError(err).Code != ResourceExhausted {
		t.Fatal("budget above the limit was accepted", err)
	}
	if err := (ExecutionJobBudget{PreparationBytes: 2048, ManifestBytes: 2048, InputBytes: 128}).Require(nil); err != nil {
		t.Fatal("small job was rejected", err)
	}
}

func TestExecutionJobInputMeasuresEscapedPrompt(t *testing.T) {
	plain, escaped := SessionInput{Prompt: strings.Repeat("a", 1000), Mode: ExecuteMode}, SessionInput{Prompt: strings.Repeat("<", 1000), Mode: ExecuteMode}
	a, _ := json.Marshal(plain)
	b, _ := json.Marshal(escaped)
	if len(b) < len(a)+5*1000 {
		t.Fatal("escaped prompt was not measured after JSON escaping", len(a), len(b))
	}
}

func TestExecutionJobInputReserveCoversEscapedExecutablePath(t *testing.T) {
	path := ExecutionStartupSelection{Harness: Codex, ExplicitPath: strings.Repeat("<", 4096)}
	raw, err := json.Marshal(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > ExecutionExecutablePathReserveBytes+ExecutionJobInputScalarReserveBytes {
		t.Fatal("reserve does not cover the escaped maximum executable path", len(raw))
	}
}

// The reserve must cover a complete configuration whose every bounded text
// field is at its accepted maximum with the widest JSON escaping. Accounts,
// templates and instructions are measured separately and stay empty here.
func TestExecutionConfigurationReserveCoversWorstCaseScalars(t *testing.T) {
	text := strings.Repeat("<", 256)
	if Text(text, "fixture", 256, true) != nil || ValidateBranchPrefix(text) != nil {
		t.Fatal("fixture is not an accepted maximum text")
	}
	identity := ModelIdentity{ProviderID: NewID(), NativeID: text}
	child := ModelIdentity{ProviderID: identity.ProviderID, NativeID: text}
	configuration := ExecutionConfiguration{
		BranchPrefix:        &BranchPrefixSelection{Version: BranchPrefixVersionExactQuote, Prefix: text, SettingsID: NewID(), SettingsRevision: 1<<63 - 1, ProjectID: NewID(), ProjectRevision: 1<<63 - 1},
		ReviewerNativeModel: CodexReviewerNativeModel,
		ImageInputDeclared:  true,
		SidechatPolicy:      CodexReadOnlySidechatV1,
		AgentID:             NewID(),
		AgentRevision:       1<<63 - 1,
		Harness:             Codex,
		ModelID:             identity.Key(),
		ModelRevision:       1<<63 - 1,
		ProviderID:          identity.ProviderID,
		NativeModel:         text,
		SubagentModel:       &ExecutionSubagentModel{ModelID: child.Key(), ModelRevision: 1<<63 - 1, NativeModel: text},
		Effort:              text,
		Options:             AgentOptions{ApprovalsReviewer: CodexReviewerAuto, SubagentModel: text, SubagentEffort: text, MaxConcurrency: 1<<32 - 1, Permission: PermissionWorkspaceWrite, ApprovalPolicy: text, ApprovalReviewModel: text, ServiceTier: text},
		Routing:             RoutingPolicy(strings.Repeat("r", 64)),
		OpenCodeContext:     &OpenCodeModelContext{Tokens: 1_000_000_000, Source: UserDeclared, Policy: OpenCodeNativeContextV1},
		GrokContext:         &GrokModelContext{Tokens: 1_000_000_000, Source: UserDeclared},
		Subscription:        true,
	}
	raw, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > ExecutionConfigurationReserveBytes {
		t.Fatalf("reserve %d does not cover the worst-case scalar configuration %d", ExecutionConfigurationReserveBytes, len(raw))
	}
}

func TestExecutionJobRejectionGuidanceNamesRetainedOwnership(t *testing.T) {
	admission := (ExecutionJobBudget{InputBytes: MaxExecutionJobDocumentBytes}).Require(nil)
	dispatch := RequireExecutionJobInput(json.RawMessage(strings.Repeat("1", MaxExecutionJobDocumentBytes)), nil)
	for name, err := range map[string]error{"admission": admission, "dispatch": dispatch} {
		if SafeError(err).Code != ResourceExhausted {
			t.Fatal(name, "was not rejected", err)
		}
	}
	if !strings.Contains(SafeError(admission).Guidance, "no workspace was prepared") {
		t.Fatal("admission guidance lost its no-workspace statement", SafeError(admission).Guidance)
	}
	guidance := SafeError(dispatch).Guidance
	if strings.Contains(guidance, "no workspace was prepared") || !strings.Contains(guidance, "prepared workspace remains retained") || !strings.Contains(guidance, "rolled back") {
		t.Fatal("dispatch guidance misstates workspace ownership", guidance)
	}
}
