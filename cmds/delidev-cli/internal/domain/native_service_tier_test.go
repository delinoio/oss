package domain

import (
	"testing"
	"time"
)

func TestNativeFastDiagnosticIsSeparateFromProviderHTTPTiers(t *testing.T) {
	if DiagnosticNativeServiceTier(Codex, "fast") == nil {
		t.Fatal("original Codex native Fast evidence was dropped")
	}
	for _, harness := range []Harness{ClaudeCode, OpenCode, GrokBuild} {
		if DiagnosticNativeServiceTier(harness, "fast") != nil {
			t.Fatal("Fast granted to another native profile")
		}
	}
	for _, tier := range []string{"fast", "future-tier", " FAST ", ""} {
		if DiagnosticServiceTier(tier) != nil || DiagnosticRequestedServiceTier(tier) != nil {
			t.Fatal("provider HTTP diagnostic domain expanded")
		}
	}
	if DiagnosticNativeServiceTier(Codex, "private-unknown") != nil || DiagnosticServiceTier("priority") == nil {
		t.Fatal("unknown redaction or original provider tier changed")
	}
}

func TestFastDiagnosticValidationRequiresOriginalCodexNativeSource(t *testing.T) {
	fast := "fast"
	id := NewID()
	record := RequestDiagnostic{ID: id, SessionID: NewID(), ExecutionID: NewID(), AccountID: NewID(), ConnectionID: NewID(), ProviderID: NewID(), ModelID: NewID(), Source: DiagnosticNativeInput, Operation: DiagnosticInput, State: DiagnosticInProgress, Purpose: ConversationUsage, Harness: Codex, InputID: NewID(), NativeRequestID: string(id), NativeThreadID: string(NewID()), ObservedAt: time.Now().UTC(), RequestedServiceTier: &fast, EffectiveServiceTier: &fast}
	if err := record.Validate(); err != nil {
		t.Fatal("original native Fast rejected", err)
	}
	effort := record
	effort.RequestedEffort = &fast
	if effort.Validate() == nil {
		t.Fatal("tier pointer alias expanded reasoning effort")
	}
	other := record
	other.Harness = ClaudeCode
	if other.Validate() == nil {
		t.Fatal("other native harness accepted Fast")
	}
	http := record
	http.Source = DiagnosticProxyHTTP
	http.Operation = DiagnosticResponse
	http.InputID = ""
	http.NativeRequestID = ""
	http.NativeThreadID = ""
	http.CorrelationID = id
	attempted := true
	http.HTTPAttempted = &attempted
	if http.Validate() == nil {
		t.Fatal("provider HTTP accepted native Fast")
	}
}
