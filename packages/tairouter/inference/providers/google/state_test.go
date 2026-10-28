// SPDX-License-Identifier: Apache-2.0
package google

import (
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

type replayStore struct {
	data  map[string][]byte
	saves int
	ttl   time.Duration
}

func (s *replayStore) Load(_ context.Context, id string) ([]byte, error) {
	b, ok := s.data[id]
	if !ok {
		return nil, inference.ErrContinuation
	}
	return b, nil
}
func (s *replayStore) Save(_ context.Context, id string, b []byte, ttl time.Duration) error {
	if s.data == nil {
		s.data = map[string][]byte{}
	}
	s.data[id] = append([]byte(nil), b...)
	s.saves++
	s.ttl = ttl
	return nil
}

func TestScopedReplayRoundTrip(t *testing.T) {
	a := NewAPI(Options{})
	store := &replayStore{}
	in := input(`{"messages":[{"role":"user","content":"do it"}],"tools":[{"type":"function","function":{"name":"odd.tool","parameters":{"type":"object"}}}]}`)
	ctx, err := a.RestoreState(a.PrepareContext(context.Background()), in, store)
	if err != nil {
		t.Fatal(err)
	}
	req, err := a.Build(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := decodeJSON(req.Body)
	alias := text(obj(array(obj(array(body["tools"])[0])["functionDeclarations"])[0])["name"])
	native, _ := json.Marshal(object{"candidates": []any{object{"content": object{"parts": []any{object{"functionCall": object{"name": alias, "args": object{"x": 1}}, "thoughtSignature": signatureA}}}, "finishReason": "STOP"}}})
	response, err := a.DecodeWithInput(ctx, in, upstream(string(native), false))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if err = a.CaptureState(ctx, in, "response-one", store); err != nil {
		t.Fatal(err)
	}
	if store.saves != 1 || store.ttl != time.Hour {
		t.Fatal("state was not captured with bounded TTL")
	}
	in.PreviousResponseID = "response-one"
	in.Body = json.RawMessage(`{"messages":[{"role":"assistant","tool_calls":[{"id":"call_a","type":"function","function":{"name":"odd.tool","arguments":"{\"x\":1}"}}]},{"role":"tool","tool_call_id":"call_a","content":"done"}]}`)
	next, err := a.RestoreState(a.PrepareContext(context.Background()), in, store)
	if err != nil {
		t.Fatal(err)
	}
	req, err = a.Build(next, in)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = decodeJSON(req.Body)
	found := false
	for _, c := range array(body["contents"]) {
		for _, p := range array(obj(c)["parts"]) {
			if obj(p)["functionCall"] != nil {
				found = true
				if signature(obj(p)) != signatureA {
					t.Fatal("signature was not restored through name codec")
				}
			}
		}
	}
	if !found {
		t.Fatal("missing call")
	}
	in.Credential.AccountID = "other"
	if _, err = a.RestoreState(a.PrepareContext(context.Background()), in, store); !errors.Is(err, inference.ErrContinuation) {
		t.Fatal("cross-account replay accepted", err)
	}
}

func TestReplayRejectsIncompleteAndMalformedState(t *testing.T) {
	a := NewAPI(Options{})
	in := input(`{"messages":[{"role":"user","content":"hi"}]}`)
	for _, wire := range []string{`data: {"candidates":[{"content":{"parts":[{"functionCall":{"name":"tool","args":{}},"thoughtSignature":"sig-alpha-opaque-value"}]}}]}` + "\n\n", `event: error` + "\ndata: {}\n\n"} {
		ctx := a.PrepareContext(context.Background())
		if _, err := a.Build(ctx, in); err != nil {
			t.Fatal(err)
		}
		response, err := a.Decode(ctx, upstream(wire, true))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = io.ReadAll(response.Body); err == nil {
			t.Fatal("incomplete stream succeeded")
		}
		response.Body.Close()
		store := &replayStore{}
		if err = a.CaptureState(ctx, in, "partial", store); err != nil {
			t.Fatal(err)
		}
		if store.saves != 0 {
			t.Fatal("partial state saved")
		}
	}
	for _, b := range []string{`{}`, strings.Repeat("x", replayLimit+1)} {
		in.PreviousResponseID = "bad"
		store := &replayStore{data: map[string][]byte{"bad": []byte(b)}}
		if _, err := a.RestoreState(a.PrepareContext(context.Background()), in, store); !errors.Is(err, inference.ErrContinuation) {
			t.Fatal("malformed state accepted", err)
		}
	}
}

func TestReplayBoundedAndExact(t *testing.T) {
	s := &requestState{}
	for i := 0; i < 500; i++ {
		s.remember(replayKey(string(rune(i)), object{}), strings.Repeat("a", maxSignatureBytes))
	}
	b, _ := json.Marshal(replaySnapshot{1, strings.Repeat("b", 64), s.replay})
	if len(b) > replayLimit || len(s.replay) > replayEntries {
		t.Fatal("unbounded replay")
	}
	if replayKey("a", object{"x": 1, "y": 2}) != replayKey("a", object{"y": 2, "x": 1}) {
		t.Fatal("unstable canonical arguments")
	}
	if replayKey("a", object{"x": 1}) == replayKey("a", object{"x": 2}) {
		t.Fatal("argument identity lost")
	}
}

// Fixtures follow google-errors.ts and tests/adapters/google/google-errors.test.ts
// at upstream 88aaa754566d119b6b8fc898f8fb47602387d82e. Retry counts come from
// the authorized route policy, not the upstream process retry defaults.
func TestPinnedHTTPRecoveryClassification(t *testing.T) {
	a := NewAntigravity(Options{})
	in := input(`{}`)
	in.Target.Retry.RateLimitRetries = 1
	in.Target.Retry.TransientSends = 2
	for _, tc := range []struct {
		status, attempt int
		body            string
		action          inference.RecoveryAction
	}{
		{429, 1, `{"error":{"status":"RESOURCE_EXHAUSTED","message":"daily limit reached"}}`, inference.RecoveryStop},
		{429, 1, `{"error":{"message":"quota exceeded per minute"}}`, inference.RecoveryRetry},
		{429, 2, `{}`, inference.RecoveryStop},
		{503, 1, `{}`, inference.RecoveryRetry},
		{503, 2, `{}`, inference.RecoveryStop},
		{403, 1, `{"error":{"details":[{"reason":"VALIDATION_REQUIRED"}]}}`, inference.RecoveryStop},
		{400, 1, `{"error":{"status":"FAILED_PRECONDITION"}}`, inference.RecoveryDefault},
	} {
		d, err := a.RecoverHTTP(context.Background(), in, inference.HTTPFailure{Status: tc.status, Attempt: tc.attempt, Body: []byte(tc.body)})
		if err != nil || d.Action != tc.action {
			t.Fatalf("%+v: %+v %v", tc, d, err)
		}
	}
	d, err := a.RecoverHTTP(context.Background(), in, inference.HTTPFailure{Status: 503, Attempt: 1, Header: http.Header{"Retry-After": {"9999"}}})
	if err != nil || d.Action != inference.RecoveryStop {
		t.Fatal("excess delay accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = a.RecoverHTTP(ctx, in, inference.HTTPFailure{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestReplayStreamingCompletionAndCancellation(t *testing.T) {
	a := NewAPI(Options{})
	in := input(`{"messages":[{"role":"user","content":"hi"}],"stream":true}`)
	for _, cancelBeforeCapture := range []bool{false, true} {
		ctx, cancel := context.WithCancel(a.PrepareContext(context.Background()))
		if _, err := a.Build(ctx, in); err != nil {
			t.Fatal(err)
		}
		response, err := a.Decode(ctx, upstream("data: {\"candidates\":[{\"content\":{\"parts\":[{\"functionCall\":{\"name\":\"tool\",\"args\":{}},\"thoughtSignature\":\"sig-alpha-opaque-value\"}]},\"finishReason\":\"STOP\"}]}\n\n", true))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = io.ReadAll(response.Body); err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if cancelBeforeCapture {
			cancel()
		}
		store := &replayStore{}
		err = a.CaptureState(ctx, in, "complete", store)
		if cancelBeforeCapture {
			if !errors.Is(err, context.Canceled) || store.saves != 0 {
				t.Fatal("canceled capture persisted state", err)
			}
		} else {
			if err != nil || store.saves != 1 {
				t.Fatal("complete stream not captured", err)
			}
			var snapshot replaySnapshot
			if json.Unmarshal(store.data["complete"], &snapshot) != nil || len(snapshot.Entries) != 1 {
				t.Fatal("signature missing")
			}
		}
		cancel()
	}
}

// The pin aligns repeated identical calls from the end after history compaction.
func TestReplayRepeatedCallsAlignFromEnd(t *testing.T) {
	s := &requestState{}
	key := replayKey("tool", object{})
	s.remember(key, signatureA)
	s.remember(key, signatureB)
	first := object{"functionCall": object{"name": "tool", "args": object{}}}
	last := object{"functionCall": object{"name": "tool", "args": object{}}}
	body := object{"contents": []any{object{"role": "model", "parts": []any{first, last}}}}
	s.restoreCalls(body, nil)
	if signature(first) != signatureA || signature(last) != signatureB {
		t.Fatal("repeated calls lost chronology")
	}
	compacted := object{"functionCall": object{"name": "tool", "args": object{}}}
	body["contents"] = []any{object{"role": "model", "parts": []any{compacted}}}
	s.restoreCalls(body, nil)
	if signature(compacted) != signatureB {
		t.Fatal("compaction did not select newest signature")
	}
}
