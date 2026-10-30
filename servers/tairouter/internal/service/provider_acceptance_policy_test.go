// SPDX-License-Identifier: Apache-2.0
package service

import (
	v1 "github.com/delinoio/oss/protos/tairouter/v1"
	"net/http"
	"sync/atomic"
	"testing"
)

func TestProviderAcceptanceZAIPresetEndpoint(t *testing.T) {
	var calls atomic.Int32
	f := newProviderAcceptance(t, "zai", "glm-5.3", func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Host != "api.z.ai" || r.URL.Path != "/api/v1/responses" || r.URL.RawQuery != "" {
			t.Error("ZAI explicit Responses endpoint lost in composition")
		}
		providerAcceptanceResponse(w, providerAcceptanceResponses)
	})
	providerAcceptanceStatus(t, f.call("/v1/responses", `{"model":"fixture","input":"hello"}`, nil), 200)
	if calls.Load() != 1 {
		t.Fatal("ZAI physical send missing")
	}
}
func TestProviderAcceptanceVolcengineExplicitFalse(t *testing.T) {
	for _, override := range []bool{false, true} {
		t.Run(map[bool]string{false: "default", true: "explicit-false"}[override], func(t *testing.T) {
			var calls atomic.Int32
			f := newProviderAcceptance(t, "volcengine-coding-plan", "ark-code-latest", func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				b := providerAcceptanceBody(t, r)
				items, _ := b["input"].([]any)
				found := false
				for _, item := range items {
					m, _ := item.(map[string]any)
					if m["type"] == "reasoning" {
						found = true
					}
				}
				if found != override {
					t.Error("tri-state reasoning policy not applied to same-wire request")
				}
				providerAcceptanceResponse(w, providerAcceptanceResponses)
			})
			if override {
				v := false
				f.data.Metadata.Configuration = &v1.ProviderConfiguration{Policy: &v1.ProviderPolicyOverrides{DropResponsesReasoningItems: &v}}
				f.save()
			}
			providerAcceptanceStatus(t, f.call("/v1/responses", `{"model":"fixture","input":[{"type":"reasoning","summary":[{"type":"summary_text","text":"fixture thought"}]},{"role":"user","content":"hello"}]}`, nil), 200)
			if calls.Load() != 1 {
				t.Fatal("Volcengine physical send missing")
			}
		})
	}
}
