// SPDX-License-Identifier: Apache-2.0
package inference

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func TestFastPoliciesSerializeAtActualSend(t *testing.T) {
	for _, tc := range []struct {
		name   string
		wire   Wire
		auth   AuthMode
		policy FastPolicy
		field  string
		value  any
	}{{"tier", Chat, APIKey, FastPolicy{Kind: FastServiceTier, Eligible: true, Priority: "priority"}, "service_tier", "priority"}, {"anthropic", Anthropic, APIKey, FastPolicy{Kind: FastAnthropicSpeed, Eligible: true, Priority: "fast", Betas: []string{"fast-mode-2026-02-01"}}, "speed", "fast"}, {"oauth-lane", Responses, OAuth, FastPolicy{Kind: FastModelVariant, Eligible: true, Model: "grok-4.7-build-fast"}, "model", "grok-4.7-build-fast"}} {
		t.Run(tc.name, func(t *testing.T) {
			h, _, _, _, u := harness(t, func(w http.ResponseWriter, r *http.Request) {
				var b object
				json.NewDecoder(r.Body).Decode(&b)
				if b[tc.field] != tc.value {
					t.Errorf("missing fast field: %v", b)
				}
				if tc.wire == Anthropic && r.Header.Get("anthropic-beta") != "fast-mode-2026-02-01" {
					t.Error("missing fast beta")
				}
				io.WriteString(w, fixture(tc.wire))
			}, tc.wire)
			router := h.c.Router.(testRouter)
			router.route.Targets[0].Fast = true
			router.route.Targets[0].FastPolicy = &tc.policy
			router.route.Targets[0].Auth = tc.auth
			h.c.Router = router
			w := call(h, "/v1/chat/completions", `{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
			if w.Code != 200 || u.records[0].ServiceTier != "" {
				t.Fatalf("%d %s guessed tier=%q", w.Code, w.Body, u.records[0].ServiceTier)
			}
		})
	}
}

type cursorFastFixture struct {
	endpoint string
	sawFast  bool
}

func (a *cursorFastFixture) Build(_ context.Context, in AdapterInput) (AdapterRequest, error) {
	b, e := decode(in.Body)
	if e != nil {
		return AdapterRequest{}, e
	}
	a.sawFast = b["cursor_fast"] == true
	return AdapterRequest{URL: a.endpoint, Body: []byte(`{}`), ResponseWire: Chat}, nil
}
func (a *cursorFastFixture) Decode(_ context.Context, r *http.Response) (*http.Response, error) {
	return r, nil
}
func TestCursorFastIntentArrivesBeforeBuilder(t *testing.T) {
	h, s, _, _, _ := harness(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, fixture(Chat)) }, Chat)
	a := &cursorFastFixture{endpoint: s.URL}
	router := h.c.Router.(testRouter)
	router.route.Targets[0].Wire = "cursor-agent"
	router.route.Targets[0].Fast = true
	router.route.Targets[0].FastPolicy = &FastPolicy{Kind: FastCursorVariant, Eligible: true, Priority: "fast"}
	h.c.Router = router
	h.c.Adapters = map[Wire]Adapter{"cursor-agent": a}
	w := call(h, "/v1/chat/completions", `{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	if w.Code != 200 || !a.sawFast {
		t.Fatalf("%d %s fast=%v", w.Code, w.Body, a.sawFast)
	}
}
