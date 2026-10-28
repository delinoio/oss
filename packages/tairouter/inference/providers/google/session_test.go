// SPDX-License-Identifier: Apache-2.0
package google

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/packages/tairouter/inference"
)

// This fixture models the scoped backend contract, not a production memory cache.
type sessionStore struct {
	mu     sync.Mutex
	data   map[string][]byte
	denied bool
}

func (s *sessionStore) Lookup(_ context.Context, id string) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.denied {
		return nil, false, inference.ErrContinuation
	}
	b, ok := s.data[id]
	return append([]byte(nil), b...), ok, nil
}
func (s *sessionStore) Load(ctx context.Context, id string) ([]byte, error) {
	b, ok, e := s.Lookup(ctx, id)
	if e != nil {
		return nil, e
	}
	if !ok {
		return nil, inference.ErrContinuation
	}
	return b, nil
}
func (s *sessionStore) Save(_ context.Context, id string, b []byte, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.denied {
		return inference.ErrContinuation
	}
	if s.data == nil {
		s.data = map[string][]byte{}
	}
	s.data[id] = append([]byte(nil), b...)
	return nil
}
func (s *sessionStore) Update(_ context.Context, id string, _ time.Duration, merge func([]byte) ([]byte, error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.denied {
		return inference.ErrContinuation
	}
	b, e := merge(s.data[id])
	if e != nil {
		return e
	}
	if s.data == nil {
		s.data = map[string][]byte{}
	}
	s.data[id] = b
	return nil
}
func sessionInput() inference.AdapterInput {
	in := input(`{"messages":[{"role":"user","content":"first prompt"}]}`)
	in.Target.Auth = inference.OAuth
	in.Credential.Headers = http.Header{ProjectHeader: {"project"}}
	in.ClientHeaders = http.Header{"X-Codex-Parent-Thread-Id": {"parent"}, "Thread-Id": {"child"}}
	return in
}
func TestAuthoritativeSessionReplay(t *testing.T) {
	a := NewAntigravity(Options{})
	in := sessionInput()
	store := &sessionStore{}
	ctx, err := a.RestoreState(a.PrepareContext(context.Background()), in, store)
	if err != nil {
		t.Fatal(err)
	}
	req, err := a.Build(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := decodeJSON(req.Body)
	session := text(obj(body["request"])["sessionId"])
	response, err := a.Decode(ctx, upstream(`{"response":{"candidates":[{"content":{"parts":[{"functionCall":{"name":"tool","args":{"x":1}},"thoughtSignature":"sig-alpha-opaque-value"}]},"finishReason":"STOP"}]}}`, false))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if err = a.CaptureState(ctx, in, "first", store); err != nil {
		t.Fatal(err)
	}
	// A new adapter instance proves there is no process-local association.
	a = NewAntigravity(Options{})
	in.Body = json.RawMessage(`{"messages":[{"role":"user","content":"compacted prompt"},{"role":"assistant","tool_calls":[{"id":"call_one","type":"function","function":{"name":"tool","arguments":"{\"x\":1}"}}]},{"role":"tool","tool_call_id":"call_one","content":"done"}]}`)
	next, err := a.RestoreState(a.PrepareContext(context.Background()), in, store)
	if err != nil {
		t.Fatal(err)
	}
	req, err = a.Build(next, in)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = decodeJSON(req.Body)
	root := obj(body["request"])
	if text(root["sessionId"]) != session {
		t.Fatal("compaction changed stable session")
	}
	found := false
	for _, c := range array(root["contents"]) {
		for _, p := range array(obj(c)["parts"]) {
			if obj(p)["functionCall"] != nil {
				found = signature(obj(p)) == signatureA
			}
		}
	}
	if !found {
		t.Fatal("durable signature not restored")
	}
	store.denied = true
	if _, err = a.RestoreState(a.PrepareContext(context.Background()), in, store); !errors.Is(err, inference.ErrContinuation) {
		t.Fatal("denial treated as absent", err)
	}
}
func TestSessionAtomicMergeAndIsolation(t *testing.T) {
	a := NewAntigravity(Options{})
	in := sessionInput()
	store := &sessionStore{}
	var contexts []context.Context
	for i := 0; i < 2; i++ {
		ctx, e := a.RestoreState(a.PrepareContext(context.Background()), in, store)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = a.Build(ctx, in); e != nil {
			t.Fatal(e)
		}
		s := ctx.Value(requestKey{}).(*requestState)
		key := replayKey(string(rune('a'+i)), object{})
		s.remember(key, signatureA)
		s.observed = append([]replayEntry(nil), s.replay...)
		s.complete = true
		contexts = append(contexts, ctx)
	}
	var wg sync.WaitGroup
	for i, ctx := range contexts {
		wg.Add(1)
		go func(i int, ctx context.Context) {
			defer wg.Done()
			if e := a.CaptureState(ctx, in, string(rune('a'+i)), store); e != nil {
				t.Error(e)
			}
		}(i, ctx)
	}
	wg.Wait()
	ctx, e := a.RestoreState(a.PrepareContext(context.Background()), in, store)
	if e != nil {
		t.Fatal(e)
	}
	if len(ctx.Value(requestKey{}).(*requestState).replay) != 2 {
		t.Fatal("concurrent session update lost signatures")
	}
	in.ClientHeaders.Set("Thread-Id", "sibling")
	ctx, e = a.RestoreState(a.PrepareContext(context.Background()), in, store)
	if e != nil {
		t.Fatal(e)
	}
	if len(ctx.Value(requestKey{}).(*requestState).replay) != 0 {
		t.Fatal("sibling shared replay")
	}
	in.ClientHeaders.Del("X-Codex-Parent-Thread-Id")
	if headerAnchor(in) != "" {
		t.Fatal("parentless root changed pinned anchor")
	}
}
func TestSessionRequiresAuthoritativeCapabilities(t *testing.T) {
	a := NewAntigravity(Options{})
	in := sessionInput()
	if _, err := a.RestoreState(a.PrepareContext(context.Background()), in, &replayStore{}); err == nil {
		t.Fatal("legacy load/save accepted for session replay")
	}
}
