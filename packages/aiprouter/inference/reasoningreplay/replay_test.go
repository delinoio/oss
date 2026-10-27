// SPDX-License-Identifier: Apache-2.0
package reasoningreplay

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// encryptedFixture models the hook contract only. Production SQL registration,
// transactions, cleanup, and deletion fences require the composed server fixture.
type encryptedFixture struct {
	mu       sync.Mutex
	aead     cipher.AEAD
	rows     map[string]fixtureRow
	sequence int
	now      time.Time
	denied   error
}
type fixtureRow struct {
	cipher      []byte
	expires     time.Time
	order, size int
}

func newFixture(t *testing.T) *encryptedFixture {
	t.Helper()
	block, err := aes.NewCipher(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	return &encryptedFixture{aead: aead, rows: make(map[string]fixtureRow), now: time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)}
}
func (f *encryptedFixture) hooks() Hooks {
	return Hooks{
		Lookup: func(ctx context.Context, key string) (State, bool, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.denied != nil {
				return State{}, false, f.denied
			}
			row, ok := f.rows[key]
			if !ok || !row.expires.After(f.now) {
				return State{}, false, nil
			}
			n := f.aead.NonceSize()
			data, err := f.aead.Open(nil, row.cipher[:n], row.cipher[n:], []byte(key))
			return State{data, row.expires}, true, err
		},
		Save: func(ctx context.Context, key string, state State) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.denied != nil {
				return f.denied
			}
			if len(state.Data) > MaxBytes || !state.ExpiresAt.After(f.now) || state.ExpiresAt.After(f.now.Add(TTL)) {
				return ErrState
			}
			nonce := make([]byte, f.aead.NonceSize())
			if _, err := rand.Read(nonce); err != nil {
				return err
			}
			f.sequence++
			f.rows[key] = fixtureRow{f.aead.Seal(nonce, nonce, state.Data, []byte(key)), state.ExpiresAt, f.sequence, len(state.Data)}
			total := 0
			for k, row := range f.rows {
				if !row.expires.After(f.now) {
					delete(f.rows, k)
				} else {
					total += row.size
				}
			}
			for len(f.rows) > MaxEntries || total > MaxBytes {
				oldest := ""
				order := int(^uint(0) >> 1)
				for k, row := range f.rows {
					if row.order < order {
						oldest, order = k, row.order
					}
				}
				total -= f.rows[oldest].size
				delete(f.rows, oldest)
			}
			return nil
		}}
}
func fixtureScope() Scope {
	return Scope{"org", "user", "conversation", "provider", "account", "deepseek-v3.2-pro", "https://provider.invalid/v1", "openai-chat", "generation-1"}
}
func fixtureReplay(t *testing.T, f *encryptedFixture, scope Scope) *Replay {
	t.Helper()
	r, err := New(f.hooks(), scope)
	if err != nil {
		t.Fatal(err)
	}
	r.now = func() time.Time { f.mu.Lock(); defer f.mu.Unlock(); return f.now }
	return r
}
func decode(t *testing.T, s string) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatal(err)
	}
	return v
}
func TestStrippedThinkingToolResultRestoredAcrossInstances(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	r := fixtureReplay(t, f, fixtureScope())
	first := decode(t, `{"choices":[{"message":{"role":"assistant","reasoning_content":"raw thought\nwith quote \" and UTF-8 한","tool_calls":[{"id":"call_1"},{"id":"call_2"}]}}]}`)
	if err := r.CaptureChat(ctx, first); err != nil {
		t.Fatal(err)
	}
	for _, row := range f.rows {
		if bytes.Contains(row.cipher, []byte("raw thought")) {
			t.Fatal("plaintext retained")
		}
	}
	// A new Replay proves that lookup uses hooks rather than process state.
	r = fixtureReplay(t, f, fixtureScope())
	body := decode(t, `{"messages":[{"role":"assistant","tool_calls":[{"id":"call_1"},{"id":"call_2"}]},{"role":"tool","tool_call_id":"call_1","content":"result one"},{"role":"tool","tool_call_id":"call_2","content":"result two"}]}`)
	messages := body["messages"].([]any)
	before, _ := json.Marshal(messages)
	repaired, err := r.RepairChat(ctx, messages, DefaultPolicy(fixtureScope().Model, true), MaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	want := "raw thought\nwith quote \" and UTF-8 한"
	if repaired[0].(map[string]any)["reasoning_content"] != want {
		t.Fatalf("wrong restored text: %v", repaired)
	}
	after, _ := json.Marshal(messages)
	if !bytes.Equal(before, after) {
		t.Fatal("mutated request")
	}
	if !reflect.DeepEqual(messages[1:], repaired[1:]) {
		t.Fatal("changed tool results")
	}
	for i := 0; i < 2; i++ {
		if text, err := r.Peek(ctx, "call_1"); err != nil || text != want {
			t.Fatalf("non-consuming read: %q %v", text, err)
		}
	}
}
func TestMissingExpiryScopeAndRevocation(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	scope := fixtureScope()
	r := fixtureReplay(t, f, scope)
	if _, err := r.Peek(ctx, "missing"); !errors.Is(err, ErrMissing) {
		t.Fatal(err)
	}
	if err := r.Capture(ctx, "call", "thought"); err != nil {
		t.Fatal(err)
	}
	v := reflect.ValueOf(scope)
	for i := 0; i < v.NumField(); i++ {
		t.Run(reflect.TypeOf(scope).Field(i).Name, func(t *testing.T) {
			other := scope
			field := reflect.ValueOf(&other).Elem().Field(i)
			field.SetString(field.String() + "-other")
			if _, err := fixtureReplay(t, f, other).Peek(ctx, "call"); !errors.Is(err, ErrMissing) {
				t.Fatal(err)
			}
		})
	}
	denied := errors.New("revoked")
	f.denied = denied
	if _, err := r.Peek(ctx, "call"); !errors.Is(err, denied) {
		t.Fatal(err)
	}
	if err := r.Capture(ctx, "other", "new"); !errors.Is(err, denied) {
		t.Fatal(err)
	}
	f.denied = nil
	f.now = f.now.Add(TTL)
	if _, err := r.Peek(ctx, "call"); !errors.Is(err, ErrMissing) {
		t.Fatal(err)
	}
	messages := []any{map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"id": "call"}}}}
	if _, err := r.RepairChat(ctx, messages, DefaultPolicy(scope.Model, true), MaxBytes); !errors.Is(err, ErrMissing) {
		t.Fatalf("fabricated missing context: %v", err)
	}
}
func TestBoundsAndEmptyCapture(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	r := fixtureReplay(t, f, fixtureScope())
	for i := 0; i < 65; i++ {
		if err := r.Capture(ctx, fmt.Sprint(i), "x"); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.rows) != 64 {
		t.Fatal(len(f.rows))
	}
	if _, err := r.Peek(ctx, "0"); !errors.Is(err, ErrMissing) {
		t.Fatal(err)
	}
	if err := r.Capture(ctx, "64", ""); err != nil {
		t.Fatal(err)
	}
	if text, err := r.Peek(ctx, "64"); err != nil || text != "x" {
		t.Fatal(text, err)
	}
	if err := r.Capture(ctx, "large", strings.Repeat("한", MaxBytes/3+1)); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Peek(ctx, "large"); !errors.Is(err, ErrMissing) {
		t.Fatal(err)
	}
	if err := r.Capture(ctx, "full", strings.Repeat("a", MaxBytes)); err != nil {
		t.Fatal(err)
	}
	if len(f.rows) != 1 {
		t.Fatal("byte bound not applied", len(f.rows))
	}
	if text, err := r.Peek(ctx, "full"); err != nil || len(text) != MaxBytes {
		t.Fatal(len(text), err)
	}
}
func TestRepairPolicyAndEscapedBudget(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	r := fixtureReplay(t, f, fixtureScope())
	text := "\"\n\\<한"
	if err := r.Capture(ctx, "call", text); err != nil {
		t.Fatal(err)
	}
	m := map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"id": "call"}}}
	value, _ := json.Marshal(text)
	budget := int64(len(value) + len(`"reasoning_content":`) + 1)
	if _, err := r.RepairChat(ctx, []any{m}, DefaultPolicy(fixtureScope().Model, true), budget-1); !errors.Is(err, ErrBudget) {
		t.Fatal(err)
	}
	if _, err := r.RepairChat(ctx, []any{m}, DefaultPolicy(fixtureScope().Model, true), budget); err != nil {
		t.Fatal(err)
	}
	for _, p := range []Policy{{}, {Preserve: true, Details: true}} {
		out, err := r.RepairChat(ctx, []any{m}, p, 0)
		if err != nil || !reflect.DeepEqual(out[0], m) {
			t.Fatal(out, err)
		}
	}
	for _, model := range []string{"deepseek-v3.2-pro", "host/deepseek-flash:suffix", "reflection/beam", "stealth-1"} {
		p := DefaultPolicy(model, true)
		out, err := r.RepairChat(ctx, []any{map[string]any{"role": "assistant", "content": "answer"}}, p, 100)
		if err != nil || out[0].(map[string]any)["reasoning_content"] != " " {
			t.Fatal(model, out, err)
		}
	}
	for _, model := range []string{"deepseek-chat", "deepseek-reasoner", "deepseek-r1", "notdeepseek-pro", "reflectionish"} {
		if DefaultPolicy(model, true).Preserve {
			t.Fatal(model)
		}
	}
	original := map[string]any{"role": "assistant", "reasoning_content": "explicit", "reasoning": "alias", "tool_calls": []any{map[string]any{"id": "missing"}}}
	out, err := r.RepairChat(ctx, []any{original}, DefaultPolicy(fixtureScope().Model, true), 0)
	if err != nil || out[0].(map[string]any)["reasoning_content"] != "explicit" {
		t.Fatal(out, err)
	}
	delete(original, "reasoning_content")
	out, err = r.RepairChat(ctx, []any{original}, DefaultPolicy(fixtureScope().Model, true), 100)
	if err != nil || out[0].(map[string]any)["reasoning_content"] != "alias" {
		t.Fatal(out, err)
	}
}
func TestInvalidStateAndNoExpiryExtension(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	r := fixtureReplay(t, f, fixtureScope())
	if _, err := New(f.hooks(), Scope{}); !errors.Is(err, ErrScope) {
		t.Fatal(err)
	}
	if _, err := New(Hooks{}, fixtureScope()); !errors.Is(err, ErrState) {
		t.Fatal(err)
	}
	if err := r.Capture(ctx, "call", "thought"); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(59 * time.Minute)
	if _, err := r.Peek(ctx, "call"); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(time.Minute)
	if _, err := r.Peek(ctx, "call"); !errors.Is(err, ErrMissing) {
		t.Fatal(err)
	}
	r.hooks.Lookup = func(context.Context, string) (State, bool, error) {
		return State{[]byte{0xff}, f.now.Add(TTL)}, true, nil
	}
	if _, err := r.Peek(ctx, "call"); !errors.Is(err, ErrState) {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := r.Peek(canceled, "call"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestConcurrentHooks(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	r := fixtureReplay(t, f, fixtureScope())
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprint(i)
			if err := r.Capture(ctx, id, "thought"); err != nil {
				t.Error(err)
			}
			if _, err := r.Peek(ctx, id); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
}
