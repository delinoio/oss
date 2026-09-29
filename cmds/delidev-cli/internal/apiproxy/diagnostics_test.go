package apiproxy

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

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
