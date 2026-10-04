package apiproxy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestRequestDiagnosticProviderHeaderUsesExactProtocolAndGuard(t *testing.T) {
	header := http.Header{}
	header.Set("X-Request-Id", "req_openai")
	header.Set("Request-Id", "req_anthropic")
	guard := newSecretGuard([]byte("req_PRIVATE_KEY"), nil)
	if diagnosticProviderRequestID(header, domain.OpenAIResponses, guard) != "req_openai" || diagnosticProviderRequestID(header, domain.AnthropicMessages, guard) != "req_anthropic" {
		t.Fatal("provider identity was read through another protocol's header")
	}
	for _, unsafe := range []string{"req_PRIVATE_KEY", "/private/path user@example.com", "req_" + strings.Repeat("a", 125)} {
		header.Set("Request-Id", unsafe)
		if diagnosticProviderRequestID(header, domain.AnthropicMessages, guard) != "" {
			t.Fatal("private provider header was retained")
		}
	}
	header.Set("Request-Id", "req_first")
	header.Add("Request-Id", "req_second")
	if diagnosticProviderRequestID(header, domain.AnthropicMessages, guard) != "" {
		t.Fatal("ambiguous provider headers were joined")
	}
}

func TestRequestDiagnosticSettingsRemainClosedAndIndependent(t *testing.T) {
	guard := newSecretGuard([]byte("PRIVATE_PROVIDER_KEY"), []byte("PRIVATE_EXECUTION_TOKEN"))
	for _, test := range []struct {
		operation Operation
		body      string
		effort    string
	}{
		{ResponseCreate, `{"reasoning":{"effort":"high"},"service_tier":"priority","input":"PRIVATE_BODY"}`, "high"},
		{ResponseCompact, `{"reasoning":{"effort":"low"}}`, "low"},
		{ChatCompletion, `{"reasoning_effort":"medium","messages":[{"content":"PRIVATE_BODY"}]}`, "medium"},
		{MessageCreate, `{"output_config":{"effort":"max"}}`, "max"},
		{ResponseCreate, `{"reasoning":{"effort":"PRIVATE_PROVIDER_KEY"},"service_tier":"/private/path user@example.com"}`, ""},
		{ResponseCreate, `{"reasoning":{"effort":"future-value"}}`, ""},
	} {
		value := domain.RequestDiagnostic{}
		diagnosticRequestSettings([]byte(test.body), test.operation, &value, guard)
		if test.effort == "" && value.RequestedEffort != nil || test.effort != "" && (value.RequestedEffort == nil || *value.RequestedEffort != test.effort) || value.EffectiveEffort != nil {
			t.Fatal("requested settings were guessed or replaced", value)
		}
		raw, _ := json.Marshal(value)
		for _, secret := range []string{"PRIVATE_BODY", "PRIVATE_PROVIDER_KEY", "PRIVATE_EXECUTION_TOKEN", "/private/path", "user@example.com", "future-value"} {
			if strings.Contains(string(raw), secret) {
				t.Fatal("private request content entered diagnostics")
			}
		}
	}
}

func TestRequestDiagnosticStreamConflictsRemainUnavailable(t *testing.T) {
	guard := newSecretGuard(nil, nil)
	value := domain.RequestDiagnostic{}
	observations := diagnosticObservations{value: &value}
	observe := func(raw string) {
		t.Helper()
		var object map[string]json.RawMessage
		if json.Unmarshal([]byte(raw), &object) != nil {
			t.Fatal("invalid fixture")
		}
		diagnosticResponse(object, &observations, guard)
	}
	observe(`{"id":"resp_original","reasoning":{"effort":"high"},"service_tier":"priority"}`)
	observe(`{"id":"resp_original","reasoning":{"effort":"low"},"service_tier":"future-value"}`)
	observe(`{"id":"resp_original","reasoning":{"effort":"high"},"service_tier":"priority"}`)
	if value.NativeResponseID != "resp_original" || value.EffectiveEffort != nil || value.EffectiveServiceTier != nil {
		t.Fatal("later stream frame restored conflicting settings")
	}
	observe(`{"id":"resp_other"}`)
	observe(`{"id":"resp_original","reasoning":{"effort":"high"},"service_tier":"priority"}`)
	if value.NativeResponseID != "" || value.EffectiveEffort != nil || value.EffectiveServiceTier != nil {
		t.Fatal("later stream frame restored mixed response identity")
	}
}

func TestRequestDiagnosticUnsafeStreamIdentityCannotRestoreCertainty(t *testing.T) {
	for _, conflicting := range []string{"/private/response", "resp_PRIVATE_PROVIDER_KEY", ""} {
		t.Run(fmt.Sprintf("case-%d", len(conflicting)), func(t *testing.T) {
			guard := newSecretGuard([]byte("PRIVATE_PROVIDER_KEY"))
			value := domain.RequestDiagnostic{}
			observations := diagnosticObservations{value: &value}
			for _, id := range []string{"resp_original", conflicting, "resp_original"} {
				raw, _ := json.Marshal(id)
				diagnosticResponse(map[string]json.RawMessage{"id": raw, "service_tier": json.RawMessage(`"priority"`)}, &observations, guard)
			}
			if !observations.identityConflict || value.NativeResponseID != "" || value.EffectiveServiceTier != nil || value.EffectiveEffort != nil {
				t.Fatal("unsafe conflicting response restored stream certainty")
			}
			raw, _ := json.Marshal(value)
			if strings.Contains(string(raw), "PRIVATE_PROVIDER_KEY") || strings.Contains(string(raw), "/private/response") {
				t.Fatal("unsafe response identity entered diagnostic output")
			}
		})
	}
}
