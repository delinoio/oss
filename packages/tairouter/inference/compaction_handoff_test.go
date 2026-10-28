// SPDX-License-Identifier: Apache-2.0
package inference

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type handoffFixture struct {
	captured []CompactionHandoff
	lookups  []int
	cleared  int
	next     *CompactionHandoff
	failure  error
}

func (f *handoffFixture) Capture(_ context.Context, _ Principal, t Target, c Credential) error {
	f.captured = append(f.captured, CompactionHandoff{t, c})
	return f.failure
}
func (f *handoffFixture) Lookup(_ context.Context, _ Principal, _ Target, h HTTPFailure) (*CompactionHandoff, error) {
	f.lookups = append(f.lookups, h.Status)
	return f.next, f.failure
}
func (f *handoffFixture) Clear(context.Context, Principal) error { f.cleared++; return f.failure }
func TestCompactionHandoffCaptureAndNativeClear(t *testing.T) {
	for _, wire := range []Wire{Chat, Responses} {
		h, _, _, _, _ := harness(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, fixture(wire)) }, wire)
		handoff := &handoffFixture{}
		h.c.CompactionHandoffs = handoff
		out := call(h, "/v1/responses/compact", `{"model":"m","input":"task"}`)
		if out.Code != 200 {
			t.Fatalf("%d %s", out.Code, out.Body)
		}
		if wire == Chat {
			if len(handoff.captured) != 1 || handoff.captured[0].Credential.AccountID != "a" || handoff.captured[0].Target.Wire != Chat {
				t.Fatal("missing physical identity")
			}
		} else if handoff.cleared != 1 || len(handoff.captured) != 0 {
			t.Fatal("native not cleared")
		}
	}
}
func TestCompactionQuotaHandoffSharesBudgetAndCredential(t *testing.T) {
	for _, cap := range []int{1, 2} {
		sends := 0
		h, s, _, _, u := harness(t, func(w http.ResponseWriter, r *http.Request) {
			sends++
			if strings.HasSuffix(r.URL.Path, "/compact") {
				w.WriteHeader(400)
				io.WriteString(w, `{"error":{"message":"quota exhausted"}}`)
				return
			}
			if r.Header.Get("Authorization") != "Bearer handoff-secret" {
				t.Error("wrong handoff credential")
			}
			io.WriteString(w, fixture(Chat))
		}, Responses)
		handoff := &handoffFixture{next: &CompactionHandoff{Target: Target{Provider: "concrete", BaseURL: s.URL, Model: "physical-model", Wire: Chat, Auth: APIKey}, Credential: Credential{AccountID: "bound", CredentialVersion: "v1", Secret: "handoff-secret"}}}
		h.c.CompactionHandoffs = handoff
		router := h.c.Router.(testRouter)
		router.route.MaxSends = cap
		h.c.Router = router
		out := call(h, "/v1/responses/compact", `{"model":"m","input":"task"}`)
		if cap == 1 {
			if out.Code != 400 || sends != 1 || len(handoff.lookups) != 0 {
				t.Fatal("budget escaped")
			}
		} else if out.Code != 200 || sends != 2 || u.records[0].Sends != 2 || len(handoff.lookups) != 1 || len(handoff.captured) != 0 {
			t.Fatalf("%d %s %+v sends%d", out.Code, out.Body, handoff, sends)
		}
	}
}
func TestCompaction404NeverLooksUpHandoff(t *testing.T) {
	h, _, _, _, _ := harness(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/compact") {
			w.WriteHeader(404)
			return
		}
		io.WriteString(w, fixture(Responses))
	}, Responses)
	handoff := &handoffFixture{}
	h.c.CompactionHandoffs = handoff
	router := h.c.Router.(testRouter)
	router.route.MaxSends = 2
	h.c.Router = router
	out := call(h, "/v1/responses/compact", `{"model":"m","input":"task"}`)
	if out.Code != 200 || len(handoff.lookups) != 0 || len(handoff.captured) != 1 {
		t.Fatalf("%d %s %+v", out.Code, out.Body, handoff)
	}
}

func TestCompactionHandoffPublicationFailureIsTerminal(t *testing.T) {
	for _, wire := range []Wire{Chat, Responses} {
		h, _, _, _, u := harness(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, fixture(wire)) }, wire)
		handoff := &handoffFixture{failure: ErrUnavailable}
		h.c.CompactionHandoffs = handoff
		out := call(h, "/v1/responses/compact", `{"model":"m","input":"task"}`)
		if out.Code != 503 || u.records[0].Sends != 1 || strings.Contains(out.Body.String(), "hello") {
			t.Fatalf("published despite state failure: %d %s", out.Code, out.Body)
		}
	}
}

func TestCanceledCompactionCannotCaptureHandoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h, _, _, _, _ := harness(t, func(w http.ResponseWriter, r *http.Request) { cancel(); io.WriteString(w, fixture(Chat)) }, Chat)
	handoff := &handoffFixture{}
	h.c.CompactionHandoffs = handoff
	request := httptest.NewRequest("POST", "/v1/responses/compact", strings.NewReader(`{"model":"m","input":"task"}`)).WithContext(ctx)
	request.Header.Set("Authorization", "Bearer client-secret")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if len(handoff.captured) != 0 || handoff.cleared != 0 || response.Code == 200 {
		t.Fatalf("canceled publication: %d %+v", response.Code, handoff)
	}
}

type failedCompactReceipts struct{ receiptMemory }

func (f failedCompactReceipts) SaveProviderState(context.Context, Principal, string, string, ProviderState) error {
	return ErrUnavailable
}
func TestNativeCompactionReceiptFailurePreservesHandoff(t *testing.T) {
	h, _, _, _, u := harness(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"output":[{"type":"compaction","encrypted_content":"opaque-replay"}]}`)
	}, Responses)
	handoff := &handoffFixture{}
	h.c.CompactionHandoffs = handoff
	h.c.ProviderStates = failedCompactReceipts{receiptMemory{}}
	out := call(h, "/v1/responses/compact", `{"model":"m","input":"task"}`)
	if out.Code != 503 || handoff.cleared != 0 || len(handoff.captured) != 0 || u.records[0].Sends != 1 {
		t.Fatalf("%d %s cleared%d", out.Code, out.Body, handoff.cleared)
	}
}

func TestCompactionHandoffComparesEffectiveFastModel(t *testing.T) {
	for _, tc := range []struct {
		name, currentLogical, nextLogical, currentPhysical, nextPhysical string
		wantSend                                                         bool
	}{
		{"same-logical-new-lane", "logical", "logical", "ordinary", "fast", true},
		{"different-logical-same-lane", "old-alias", "new-alias", "same", "same", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sends := 0
			h, s, _, _, _ := harness(t, func(w http.ResponseWriter, r *http.Request) {
				sends++
				var body object
				json.NewDecoder(r.Body).Decode(&body)
				if body["model"] != tc.nextPhysical {
					t.Errorf("wrong physical model %v", body["model"])
				}
				io.WriteString(w, fixture(Chat))
			}, Chat)
			current := Target{Provider: "provider", BaseURL: s.URL, Model: tc.currentLogical, Wire: Chat, Auth: OAuth, Fast: true, FastPolicy: &FastPolicy{Kind: FastModelVariant, Eligible: true, Model: tc.currentPhysical}}
			next := current
			next.Model = tc.nextLogical
			next.FastPolicy = &FastPolicy{Kind: FastModelVariant, Eligible: true, Model: tc.nextPhysical}
			h.c.CompactionHandoffs = &handoffFixture{next: &CompactionHandoff{Target: next, Credential: Credential{AccountID: "bound", CredentialVersion: "v1", Secret: "secret"}}}
			out, err := h.tryCompactionHandoff(context.Background(), Principal{}, current, HTTPFailure{Status: 429}, object{"model": "public", "input": "task"}, Route{MaxSends: 2}, &UsageRecord{Sends: 1})
			if err != nil {
				t.Fatal(err)
			}
			if (out != nil) != tc.wantSend || (sends == 1) != tc.wantSend {
				t.Fatalf("out=%v sends=%d", out != nil, sends)
			}
		})
	}
}
