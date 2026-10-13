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
