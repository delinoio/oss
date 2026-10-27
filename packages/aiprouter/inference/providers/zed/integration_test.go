// SPDX-License-Identifier: Apache-2.0
package zed

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/packages/aiprouter/inference"
)

type fixtureState struct {
	data  map[string][]byte
	err   error
	saves int
	ttl   time.Duration
}

func (s *fixtureState) Load(ctx context.Context, id string) ([]byte, error) {
	if s.err != nil {
		return nil, s.err
	}
	if data := s.data[id]; data != nil {
		return append([]byte{}, data...), nil
	}
	return nil, inference.ErrContinuation
}
func (s *fixtureState) Save(ctx context.Context, id string, b []byte, ttl time.Duration) error {
	if s.err != nil {
		return s.err
	}
	if s.data == nil {
		s.data = map[string][]byte{}
	}
	s.data[id] = append([]byte{}, b...)
	s.saves++
	s.ttl = ttl
	return nil
}
func hookInput() inference.AdapterInput {
	return inference.AdapterInput{RequestID: "req-1", Principal: inference.Principal{OrganizationID: "org", UserID: "user", KeyID: "key", MembershipGeneration: 1}, Target: inference.Target{Provider: "zed", Wire: WireGoogle, Model: "gemini-3.5-flash", BaseURL: "https://cloud.zed.dev", Headers: make(http.Header)}, Credential: inference.Credential{AccountID: "acct", Secret: "llm-secret"}, Wire: inference.Responses, Operation: inference.Generate, Body: json.RawMessage(`{"input":"fixture","tools":[{"type":"function","name":"unsafe/tool","parameters":{"type":"object"}}]}`), ClientHeaders: http.Header{"Thread-Id": []string{"client-thread"}}}
}

func TestGenericStateLifecycle(t *testing.T) {
	a, _ := New(Google)
	in := hookInput()
	store := &fixtureState{}
	ctx, err := a.RestoreState(a.PrepareContext(context.Background()), in, store)
	if err != nil {
		t.Fatal(err)
	}
	built, err := a.Build(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := decodeObject(built.Body)
	thread := stringOf(body["thread_id"])
	if thread == "" || thread == "client-thread" {
		t.Fatal("unscoped client identifier")
	}
	if err := a.CaptureState(ctx, in, "resp-1", store); err != nil {
		t.Fatal(err)
	}
	if store.ttl != 24*time.Hour || len(store.data["resp-1"]) > MaxStateBytes || strings.Contains(string(store.data["resp-1"]), "fixture") || strings.Contains(string(store.data["resp-1"]), "llm-secret") {
		t.Fatal("state contains content or has wrong bounds")
	}
	next := in
	next.RequestID = "req-2"
	next.PreviousResponseID = "resp-1"
	next.ClientHeaders = nil
	continued, err := a.RestoreState(a.PrepareContext(context.Background()), next, store)
	if err != nil {
		t.Fatal(err)
	}
	built, err = a.Build(continued, next)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = decodeObject(built.Body)
	if body["thread_id"] != thread {
		t.Fatal("thread continuity lost")
	}
	if err := a.CaptureState(ctx, next, "resp-2", store); err == nil {
		t.Fatal("cross-attempt capture")
	}
	other := in
	other.Principal.OrganizationID = "other-org"
	otherCtx, err := a.RestoreState(a.PrepareContext(context.Background()), other, &fixtureState{})
	if err != nil {
		t.Fatal(err)
	}
	otherBuilt, err := a.Build(otherCtx, other)
	if err != nil {
		t.Fatal(err)
	}
	otherBody, _ := decodeObject(otherBuilt.Body)
	if otherBody["thread_id"] == thread {
		t.Fatal("cross-owner thread collision")
	}
}

func TestStateFailuresAndFences(t *testing.T) {
	a, _ := New(Google)
	in := hookInput()
	in.PreviousResponseID = "previous"
	for _, data := range []string{`{}`, `{"version":1,"family":"anthropic","thread_id":"thread"}`, `{"version":2,"family":"google","thread_id":"thread"}`, strings.Repeat("x", MaxStateBytes+1), `{"version":1,"family":"google","thread_id":"thread"} {}`} {
		_, err := a.RestoreState(context.Background(), in, &fixtureState{data: map[string][]byte{"previous": []byte(data)}})
		if !errors.Is(err, inference.ErrContinuation) {
			t.Fatalf("malformed accepted: %v", err)
		}
	}
	for _, denied := range []error{inference.ErrUnauthorized, inference.ErrUnavailable, inference.ErrContinuation} {
		_, err := a.RestoreState(context.Background(), in, &fixtureState{err: denied})
		if !errors.Is(err, denied) {
			t.Fatal("load failure replaced", err)
		}
	}
	in.PreviousResponseID = ""
	ctx, err := a.RestoreState(a.PrepareContext(context.Background()), in, &fixtureState{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Build(ctx, in); err != nil {
		t.Fatal(err)
	}
	store := &fixtureState{err: inference.ErrUnauthorized}
	if err = a.CaptureState(ctx, in, "response", store); !errors.Is(err, inference.ErrUnauthorized) || store.saves != 0 {
		t.Fatal("late save bypassed fence", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err = a.CaptureState(canceled, in, "response", store); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestGenericPolicyConsumptionAndContextualDecode(t *testing.T) {
	a, _ := New(Google)
	in := hookInput()
	policy, _ := json.Marshal(ModelPolicy{ReasoningEfforts: []string{}, ShowThinkingSummaries: true})
	in.Target.Headers.Set(PolicyHeader, string(policy))
	ctx, err := a.RestoreState(a.PrepareContext(context.Background()), in, &fixtureState{})
	if err != nil {
		t.Fatal(err)
	}
	options := requestOptions(ctx)
	if options.ReasoningEfforts == nil || len(options.ReasoningEfforts) != 0 {
		t.Fatal("explicit empty ladder lost")
	}
	built, err := a.Build(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	values, present := built.Headers[http.CanonicalHeaderKey(PolicyHeader)]
	if !present || len(values) != 0 {
		t.Fatal("policy header not removed")
	}
	state := ctx.Value(stateKey{}).(*requestState)
	alias := ""
	for k := range state.aliases {
		alias = k
	}
	response := func() *http.Response {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"` + alias + `","args":{}}}]},"finishReason":"STOP"}]}` + "\n"))}
	}
	r, err := a.DecodeWithInput(ctx, in, response())
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(r.Body)
	r.Body.Close()
	if err != nil || !strings.Contains(string(data), "unsafe/tool") {
		t.Fatalf("alias not restored: %s %v", data, err)
	}
	changed := in
	changed.Credential.AccountID = "other"
	if _, err = a.DecodeWithInput(ctx, changed, response()); err == nil {
		t.Fatal("cross-account decode allowed")
	}
	in.Target.Headers.Set(PolicyHeader, `{"thread_id":"injected"}`)
	if _, err = a.RestoreState(context.Background(), in, nil); err == nil {
		t.Fatal("policy supplied thread authority")
	}
}

func TestZedRecoveryRegistrationContract(t *testing.T) {
	a, _ := New(OpenAI)
	var policy inference.HTTPRecoveryPolicy = a
	for _, status := range []int{200, 401, 403} {
		h := make(http.Header)
		if status != 401 {
			h["X-Zed-Expired-Token"] = []string{""}
		}
		decision, err := policy.RecoverHTTP(context.Background(), hookInput(), inference.HTTPFailure{Status: status, Header: h})
		if err != nil || decision.Action != inference.RecoveryRefresh {
			t.Fatal(decision, err)
		}
		decision, err = policy.RecoverHTTP(context.Background(), hookInput(), inference.HTTPFailure{Status: status, Header: h, Refreshed: true})
		if err != nil || decision.Action != inference.RecoveryStop || decision.Error == nil {
			t.Fatal(decision, err)
		}
	}
	d, err := policy.RecoverHTTP(context.Background(), hookInput(), inference.HTTPFailure{Status: 429})
	if err != nil || d.Action != inference.RecoveryDefault {
		t.Fatal("invented retry policy")
	}
}
