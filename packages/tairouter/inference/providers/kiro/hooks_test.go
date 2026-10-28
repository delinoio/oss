// SPDX-License-Identifier: Apache-2.0
// Synthetic protocol scenarios derived from pinned opencodex; see LICENSE.upstream.
package kiro

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/packages/tairouter/inference"
)

type fixtureStore struct {
	data  []byte
	ttl   time.Duration
	err   error
	saves int
}

func (s *fixtureStore) Load(context.Context, string) ([]byte, error) { return s.data, s.err }
func (s *fixtureStore) Save(_ context.Context, _ string, b []byte, ttl time.Duration) error {
	s.saves++
	s.data = append([]byte(nil), b...)
	s.ttl = ttl
	return s.err
}

func TestStateLifecycleAndIsolation(t *testing.T) {
	ctx, a, _ := build(t, plainRequest)
	_, err := readDecoded(t, ctx, a, event("assistantResponseEvent", object{"content": "ok"}), event("metadataEvent", object{"stopReason": "END_TURN"}))
	if err != nil {
		t.Fatal(err)
	}
	store := &fixtureStore{}
	in := input(plainRequest)
	if err = a.CaptureState(ctx, in, "resp_1", store); err != nil {
		t.Fatal(err)
	}
	if store.ttl != StateTTL || store.saves != 1 {
		t.Fatal(store)
	}
	in.PreviousResponseID = "resp_1"
	restored, err := a.RestoreState(a.PrepareContext(context.Background()), in, store)
	if err != nil {
		t.Fatal(err)
	}
	req, err := a.Build(restored, in)
	if err != nil {
		t.Fatal(err)
	}
	m, _ := MetadataFor(ctx, in.Principal)
	if !bytes.Contains(req.Body, []byte(m.ConversationID)) {
		t.Fatal("conversation lost")
	}
	for _, mutate := range []func(*inference.AdapterInput){func(i *inference.AdapterInput) { i.Principal.UserID = "other" }, func(i *inference.AdapterInput) { i.Principal.MembershipGeneration++ }, func(i *inference.AdapterInput) { i.Credential.AccountID = "other" }, func(i *inference.AdapterInput) { i.Target.Model = "other" }, func(i *inference.AdapterInput) { i.Target.BaseURL = "https://other.invalid" }} {
		changed := in
		mutate(&changed)
		if _, err = a.RestoreState(a.PrepareContext(context.Background()), changed, store); !errors.Is(err, inference.ErrContinuation) {
			t.Fatal(err)
		}
	}
	original := store.data
	for _, bad := range [][]byte{[]byte("{"), bytes.Repeat([]byte("x"), maxStateBytes+1), bytes.Replace(original, []byte(`"Version":1`), []byte(`"Version":9`), 1)} {
		store.data = bad
		if _, err = a.RestoreState(context.Background(), in, store); err == nil {
			t.Fatal("invalid state accepted")
		}
	}
	store.data = original
	store.err = inference.ErrUnauthorized
	if _, err = a.RestoreState(context.Background(), in, store); !errors.Is(err, inference.ErrUnauthorized) {
		t.Fatal(err)
	}
	if err = a.CaptureState(ctx, in, "resp_2", store); !errors.Is(err, inference.ErrUnauthorized) {
		t.Fatal(err)
	}
}

func TestNoStateAfterFailedOrCancelledResponse(t *testing.T) {
	ctx, a, _ := build(t, plainRequest)
	_, _ = readDecoded(t, ctx, a, event("assistantResponseEvent", object{"content": "partial"}))
	store := &fixtureStore{}
	if err := a.CaptureState(ctx, input(plainRequest), "resp_bad", store); err == nil || store.saves != 0 {
		t.Fatal(err)
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()
	if err := a.CaptureState(cancelCtx, input(plainRequest), "resp_bad", store); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

type fixtureExecutor struct {
	responses  []*http.Response
	requests   []inference.AdapterRequest
	unmeasured int
	limit      int
}

func (e *fixtureExecutor) RecordUnmeasuredAttempt() { e.unmeasured++ }
func (e *fixtureExecutor) Do(ctx context.Context, r inference.AdapterRequest) (*http.Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(e.requests) >= e.limit {
		return nil, inference.ErrUnauthorized
	}
	e.requests = append(e.requests, r)
	response := e.responses[0]
	e.responses = e.responses[1:]
	return response, nil
}
func fixtureResponse(frames ...[]byte) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(bytes.Join(frames, nil)))}
}
func textResponse(text string) *http.Response {
	return fixtureResponse(event("assistantResponseEvent", object{"content": text}), event("metadataEvent", object{"stopReason": "END_TURN"}))
}

func TestCompletionFallbackUsesOriginalBudgetAndCapturesFinalState(t *testing.T) {
	a := New()
	ctx := a.PrepareContext(context.Background())
	in := input(toolRequest)
	executor := &fixtureExecutor{limit: 2, responses: []*http.Response{textResponse("Working"), textResponse("Final answer")}}
	response, wire, err := a.Execute(ctx, in, executor, nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if wire != inference.Chat || len(executor.requests) != 2 || executor.unmeasured != 1 || strings.Contains(string(data), "Working") || !strings.Contains(string(data), "Final answer") || !bytes.Contains(data, []byte("[DONE]")) {
		t.Fatalf("bad fallback: %s", data)
	}
	var retry object
	json.Unmarshal(executor.requests[1].Body, &retry)
	conversation := obj(retry["conversationState"])
	history := arr(conversation["history"])
	if len(history) != 2 || str(obj(obj(history[1])["assistantResponseMessage"])["content"]) != "Working" {
		t.Fatal(history)
	}
	if str(obj(obj(conversation["currentMessage"])["userInputMessage"])["content"]) != completionRetry {
		t.Fatal(conversation)
	}
	m, ok := MetadataFor(ctx, in.Principal)
	if !ok || !m.Terminal {
		t.Fatal(m)
	}
	if err = a.CaptureState(ctx, in, "resp_fallback", &fixtureStore{}); err != nil {
		t.Fatal(err)
	}
}

func TestFallbackBudgetRefusalAndHTTPFailureNeverRestartFirstLeg(t *testing.T) {
	for _, limit := range []int{1, 2} {
		a := New()
		ctx := a.PrepareContext(context.Background())
		executor := &fixtureExecutor{limit: limit, responses: []*http.Response{textResponse("Working"), {StatusCode: 503, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"message":"unavailable"}`))}}}
		response, _, err := a.Execute(ctx, input(toolRequest), executor, nil)
		if err == nil || response != nil || len(executor.requests) != limit || executor.unmeasured != 1 {
			t.Fatalf("unexpected replay %v", err)
		}
	}
}

func TestRegisteredRecoveryPolicy(t *testing.T) {
	a := New()
	in := input(plainRequest)
	d, err := a.RecoverHTTP(context.Background(), in, inference.HTTPFailure{Status: 404, Attempt: 1})
	if err != nil || d.Endpoint != "https://q.us-east-1.amazonaws.com/" || d.Action != inference.RecoveryRetry {
		t.Fatal(d, err)
	}
	in.Target.Auth = inference.OAuth
	in.Target.Retry.RefreshOn401 = true
	d, _ = a.RecoverHTTP(context.Background(), in, inference.HTTPFailure{Status: 401, Attempt: 1})
	if d.Action != inference.RecoveryRefresh {
		t.Fatal(d)
	}
	d, _ = a.RecoverHTTP(context.Background(), in, inference.HTTPFailure{Status: 401, Attempt: 2, Refreshed: true})
	if d.Action != inference.RecoveryStop {
		t.Fatal(d)
	}
	d, _ = a.RecoverHTTP(context.Background(), in, inference.HTTPFailure{Status: 429, Attempt: 3})
	if d.Action != inference.RecoveryStop {
		t.Fatal(d)
	}
}

func TestDeliveredFinalAnswerReturnsWithoutSend(t *testing.T) {
	a := New()
	ctx := a.PrepareContext(context.Background())
	executor := &fixtureExecutor{limit: 0}
	in := input(`{"messages":[{"role":"user","content":"Hello"},{"role":"assistant","content":"Done","phase":"final_answer"}]}`)
	response, _, err := a.Execute(ctx, in, executor, nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || len(executor.requests) != 0 || !bytes.Contains(data, []byte("[DONE]")) {
		t.Fatal(string(data), err)
	}
	m, _ := MetadataFor(ctx, in.Principal)
	if m.Usage.InputTokens == nil || *m.Usage.InputTokens != 0 || !m.Terminal {
		t.Fatal(m)
	}
}

func TestContextualDecodeRejectsAnotherOwner(t *testing.T) {
	ctx, a, _ := build(t, plainRequest)
	in := input(plainRequest)
	in.Principal.UserID = "other"
	response := textResponse("secret")
	defer response.Body.Close()
	if _, err := a.DecodeWithInput(ctx, in, response); !errors.Is(err, inference.ErrUnauthorized) {
		t.Fatal(err)
	}
}

func TestFallbackPreservesReasoningWithoutReplayingIt(t *testing.T) {
	a := New()
	ctx := a.PrepareContext(context.Background())
	first := fixtureResponse(event("assistantResponseEvent", object{"content": "<thinking>Private reasoning</thinking>Working"}), event("reasoningContentEvent", object{"signature": "opaque"}), event("metadataEvent", object{"stopReason": "END_TURN"}))
	executor := &fixtureExecutor{limit: 2, responses: []*http.Response{first, textResponse("Final")}}
	response, _, err := a.Execute(ctx, input(toolRequest), executor, nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("Private reasoning")) || !bytes.Contains(data, []byte("signature:opaque")) {
		t.Fatal(string(data))
	}
	if bytes.Contains(executor.requests[1].Body, []byte("Private reasoning")) || bytes.Contains(executor.requests[1].Body, []byte("opaque")) {
		t.Fatal("reasoning replayed")
	}
	ids := map[string]bool{}
	for _, line := range bytes.Split(data, []byte("\n")) {
		if !bytes.HasPrefix(line, []byte("data: ")) {
			continue
		}
		var event object
		if json.Unmarshal(line[6:], &event) == nil && str(event["id"]) != "" {
			ids[str(event["id"])] = true
		}
	}
	if len(ids) != 1 {
		t.Fatal(ids)
	}
}

func TestExecuteCancellationJoinsProducerWithoutFallback(t *testing.T) {
	a := New()
	ctx, cancel := context.WithCancel(a.PrepareContext(context.Background()))
	body := &blockingBody{closed: make(chan struct{})}
	executor := &fixtureExecutor{limit: 2, responses: []*http.Response{{StatusCode: 200, Header: http.Header{}, Body: body}}}
	done := make(chan error, 1)
	go func() { _, _, err := a.Execute(ctx, input(toolRequest), executor, nil); done <- err }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("producer not joined")
	}
	if len(executor.requests) > 1 {
		t.Fatal("replayed cancellation")
	}
}

func TestVisibleTextFailureIsNeverReplayed(t *testing.T) {
	a := New()
	ctx := a.PrepareContext(context.Background())
	executor := &fixtureExecutor{limit: 2, responses: []*http.Response{fixtureResponse(event("assistantResponseEvent", object{"content": "visible"}))}}
	response, _, err := a.Execute(ctx, input(plainRequest), executor, nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err == nil || !bytes.Contains(data, []byte("visible")) || len(executor.requests) != 1 {
		t.Fatal(string(data), err)
	}
}

func TestNativeToolCarriesAdmittedMetadata(t *testing.T) {
	ctx, a, _ := build(t, `{"messages":[{"role":"user","content":"go"}],"tools":[{"type":"custom","name":"freeform"}]}`)
	state := ctx.Value(stateKey{}).(*requestState)
	alias := ""
	for name, definition := range state.definitions {
		if definition.Kind == CustomTool {
			alias = name
		}
	}
	output, err := readDecoded(t, ctx, a, event("toolUseEvent", object{"name": alias, "toolUseId": "call_1", "input": `{"input":"raw"}`, "stop": true}), event("metadataEvent", object{"stopReason": "TOOL_USE"}))
	if err != nil || !strings.Contains(output, `"tool_kind":"custom"`) || !strings.Contains(output, `"tool_name":"freeform"`) {
		t.Fatal(output, err)
	}
}

func TestRecoverySuccessfulHeaderProbeIsNeutral(t *testing.T) {
	d, err := New().RecoverHTTP(context.Background(), input(plainRequest), inference.HTTPFailure{Status: 200})
	if err != nil || d.Action != inference.RecoveryDefault || d.Error != nil {
		t.Fatal(d, err)
	}
}
