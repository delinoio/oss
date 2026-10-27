// SPDX-License-Identifier: Apache-2.0
package cursor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/packages/aiprouter/inference"
)

type recoveringExecutor struct {
	*authorizedExecutor
	permits, unknown int
	gateErr          error
}

func (x *recoveringExecutor) AuthorizeRecovery(context.Context, inference.AdapterRecoveryReason) error {
	x.permits++
	return x.gateErr
}
func (x *recoveringExecutor) RecordUnmeasuredAttempt() { x.unknown++ }
func (m *memoryAdapterStore) Update(ctx context.Context, key string, ttl time.Duration, merge func([]byte) ([]byte, error)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.items == nil {
		m.items = map[string][]byte{}
	}
	next, err := merge(m.items[key])
	if err != nil {
		return err
	}
	m.items[key] = next
	return nil
}
func TestCoreCursorSemanticRecovery(t *testing.T) {
	for _, tc := range []struct {
		name                string
		refusals            []string
		budget, sends, runs int
		success             bool
	}{
		{"one_shot", []string{"invalid_argument"}, 6, 6, 2, true},
		{"second_refusal", []string{"invalid_argument", "invalid_argument"}, 258, 4, 2, false},
		{"no_budget", []string{"invalid_argument"}, 2, 2, 1, false},
		{"not_precondition", []string{"failed_precondition"}, 258, 2, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newDuplexFixture(t)
			f.refusals = tc.refusals
			close(f.ackInitial)
			usage := &integrationUsage{}
			auth := &integrationAuth{}
			target := input(simpleRequest).Target
			target.BaseURL = f.server.URL
			h, err := inference.New(inference.Config{Adapters: map[inference.Wire]inference.Adapter{Wire: New()}, Authorizer: auth, Router: integrationRouter{inference.Route{Targets: []inference.Target{target}, MaxSends: tc.budget}}, Credentials: integrationCredentials{}, Usage: usage, ProviderStates: &integrationState{}, Destinations: inference.DestinationPolicy{ApprovedOrigins: []string{f.server.URL}}})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"fixture","stream":true,"store":false,"messages":[{"role":"user","content":"hi"}]}`))
			req.Header.Set("Authorization", "Bearer fixture")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			f.mu.Lock()
			runs := len(f.runs)
			ids := map[string]bool{}
			appends := uint64(0)
			for _, run := range f.runs {
				ids[run.conversationID] = true
				appends += run.sequence
			}
			f.mu.Unlock()
			if runs != tc.runs || len(ids) != tc.runs || int(appends)+runs != tc.sends || usage.record.Sends != tc.sends {
				t.Fatalf("runs=%d IDs=%d appends=%d sends=%d: %s", runs, len(ids), appends, usage.record.Sends, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "[DONE]") != tc.success {
				t.Fatalf("terminal: %s", rec.Body.String())
			}
			if tc.success && usage.record.UnmeasuredAttempts < 1 {
				t.Fatal("failed leg not accounted as unknown")
			}
		})
	}
}
func TestOverflowFirstSurfaceAndRetainedCap(t *testing.T) {
	f := newDuplexFixture(t)
	f.refusals = []string{"resource_exhausted", "resource_exhausted", "resource_exhausted", "resource_exhausted", "resource_exhausted", "resource_exhausted"}
	close(f.ackInitial)
	x := &recoveringExecutor{authorizedExecutor: &authorizedExecutor{client: f.server.Client(), max: 258}}
	store := &memoryAdapterStore{}
	in := input(`{"cursor_thread_id":"fixture-thread","messages":[{"role":"user","content":"hi"}]}`)
	in.Target.BaseURL = f.server.URL
	for index := 0; index < 2; index++ {
		r, _, err := New().Execute(context.Background(), in, x, store)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(r.Body)
		r.Body.Close()
		requireCode(t, err, "cursor_context_limit_exceeded")
		if bytes.Contains(body, []byte("[DONE]")) {
			t.Fatal("overflow succeeded")
		}
		if index == 0 && (x.permits != 0 || x.sends() != 2) {
			t.Fatal("first overflow was retried")
		}
	}
	if x.permits != 3 || x.unknown != 3 || x.sends() != 10 {
		t.Fatalf("permits=%d unknown=%d sends=%d", x.permits, x.unknown, x.sends())
	}
	store.mu.Lock()
	raw := store.items[recoveryScope(in)]
	store.mu.Unlock()
	var retained retainedRecovery
	if json.Unmarshal(raw, &retained) != nil || !retained.OverflowSurfaced || retained.OverflowRemints != 3 {
		t.Fatalf("retained %+v", retained)
	}
}
func TestOutputGuardAndRecoveryExclusions(t *testing.T) {
	s := newSession("gpt-5.4", nil)
	s.guardEcho = true
	s.observeReplay = true
	for _, part := range []string{"[Tool", " Result", "]"} {
		events, err := s.textEvents(part)
		if part == "]" {
			requireCode(t, err, "cursor_tool_result_echo")
		} else if err != nil || len(events) != 0 {
			t.Fatal("quarantine leaked", events, err)
		}
	}
	if s.visible {
		t.Fatal("echo became visible")
	}
	s = newSession("gpt-5.4", []tool{{Name: "exec_command", WireName: "exec_command"}})
	_, err := s.textEvents("Shell is blocked, switching to exec_command")
	requireCode(t, err, "cursor_routing_commentary_hallucination")
	s = newSession("gpt-5.4", nil)
	s.recoverySafe = true
	s.invalidArgument = true
	in := input(simpleRequest)
	for _, blocked := range []string{"visible", "tool", "usage", "ack", "cancel"} {
		t.Run(blocked, func(t *testing.T) {
			copy := *s
			ctx := context.Background()
			switch blocked {
			case "visible":
				copy.visible = true
			case "tool":
				copy.sawRealTool = true
			case "usage":
				copy.output = new(int64)
			case "ack":
				copy.recoverySafe = false
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			x := &recoveringExecutor{authorizedExecutor: &authorizedExecutor{}}
			_, _, retry, err := New().recoveryInput(ctx, in, x, nil, &copy, ClassifyError("invalid_argument", "", nil))
			if retry || err != nil || x.permits != 0 {
				t.Fatalf("unsafe recovery %t %v", retry, err)
			}
		})
	}
	x := &recoveringExecutor{authorizedExecutor: &authorizedExecutor{}, gateErr: inference.ErrUnauthorized}
	_, _, retry, err := New().recoveryInput(context.Background(), in, x, nil, s, ClassifyError("invalid_argument", "", nil))
	if retry || !errors.Is(err, inference.ErrUnauthorized) || x.unknown != 0 {
		t.Fatal("gate refusal ignored", retry, err)
	}
}

func TestNextTurnRotationDoesNotReplayVisibleTurn(t *testing.T) {
	store := &memoryAdapterStore{}
	in := input(`{"cursor_thread_id":"visible-thread","messages":[{"role":"user","content":"hi"}]}`)
	ctx, err := retainedConversation(context.Background(), in, store, "")
	if err != nil {
		t.Fatal(err)
	}
	original := ctx.Value(recoveryConversationKey{}).(string)
	s := newSession("gpt-5.4", nil)
	s.guardEcho = true
	s.observeReplay = true
	events, err := s.textEvents("A real answer first.\n[Tool ")
	if err != nil || len(events) == 0 {
		t.Fatal(events, err)
	}
	events, err = s.textEvents("Result]\nsensitive observation")
	if err != nil || len(events) != 0 || !s.midstreamEcho {
		t.Fatal("observer did not retain finding", err)
	}
	for i := 0; i < 4; i++ {
		if err := rotateNextConversation(context.Background(), in, store, s, nil); err != nil {
			t.Fatal(err)
		}
	}
	store.mu.Lock()
	raw := append([]byte(nil), store.items[recoveryScope(in)]...)
	store.mu.Unlock()
	var state retainedRecovery
	json.Unmarshal(raw, &state)
	if state.EchoRemints != 3 || state.ConversationID == original {
		t.Fatal(state)
	}
	// Incomplete tool failures have their own three-turn allowance.
	for i := 0; i < 4; i++ {
		if err := rotateNextConversation(context.Background(), in, store, s, protocolError("cursor_incomplete_tool_call")); err != nil {
			t.Fatal(err)
		}
	}
	store.mu.Lock()
	json.Unmarshal(store.items[recoveryScope(in)], &state)
	store.mu.Unlock()
	if state.EchoRemints != 3 || state.IncompleteRemints != 3 {
		t.Fatal(state)
	}
}
