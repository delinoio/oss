// SPDX-License-Identifier: Apache-2.0
package google

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/packages/tairouter/inference"
	"github.com/delinoio/oss/packages/tairouter/inference/policy"
)

// Pinned google.ts keeps CCA defaults but gates includeThoughts on the selected
// explicit toggle, Gemini family, image exclusion and direct-mode eligibility.
func TestResolvedThinkingSummaryToggle(t *testing.T) {
	for _, tc := range []struct {
		name, model, override string
		cca, show             bool
	}{
		{"cca absent", "gemini-3.7-flash", `{}`, true, true},
		{"cca false", "gemini-3.7-flash", `{"showThinkingSummary":false}`, true, false},
		{"cca true", "gemini-3.7-flash", `{"showThinkingSummary":true}`, true, true},
		{"direct absent", "gemini-3.7-flash", `{}`, false, false},
		{"direct false", "gemini-3.7-flash", `{"showThinkingSummary":false}`, false, false},
		{"direct true", "gemini-3.7-flash", `{"showThinkingSummary":true}`, false, true},
		{"direct ineligible", "gemini-unknown", `{"showThinkingSummary":true}`, false, false},
		{"cca image", "gemini-3.1-flash-image", `{"showThinkingSummary":true}`, true, false},
		{"cca other family", "claude-sonnet-4-6", `{"showThinkingSummary":true}`, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := NewAPI(Options{})
			in := input(`{"messages":[{"role":"user","content":"hi"}]}`)
			in.Target.Model = tc.model
			if tc.cca {
				a = NewAntigravity(Options{})
				in.Target.Auth = inference.OAuth
				in.Credential.Headers = map[string][]string{ProjectHeader: {"project"}}
			}
			p, err := policy.Resolve(policy.Config{ProviderID: "google", Model: tc.model, BaseURL: "https://example.test", Overrides: json.RawMessage(tc.override)})
			if err != nil {
				t.Fatal(err)
			}
			in.Target.Policy = &p
			req, err := a.Build(a.PrepareContext(context.Background()), in)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := decodeJSON(req.Body)
			if tc.cca {
				b = obj(b["request"])
			}
			thoughts := obj(obj(b["generationConfig"])["thinkingConfig"])["includeThoughts"]
			if (thoughts == true) != tc.show {
				t.Fatalf("includeThoughts=%v want=%v body=%s", thoughts, tc.show, req.Body)
			}
			// An override cannot mutate the shared constructor's default for another request.
			in.Target.Policy = nil
			req, err = a.Build(a.PrepareContext(context.Background()), in)
			if err != nil {
				t.Fatal(err)
			}
			b, _ = decodeJSON(req.Body)
			if tc.cca {
				b = obj(b["request"])
			}
			if tc.model == "gemini-3.7-flash" && (obj(obj(b["generationConfig"])["thinkingConfig"])["includeThoughts"] == true) != tc.cca {
				t.Fatal("policy leaked into next request")
			}
		})
	}
}
