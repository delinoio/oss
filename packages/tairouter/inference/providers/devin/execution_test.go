// SPDX-License-Identifier: Apache-2.0
package devin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/packages/tairouter/inference"
)

type atomicStateFixture struct {
	mu      sync.Mutex
	entries map[string][]byte
	failure error
	updates int
}

func (s *atomicStateFixture) Load(_ context.Context, id string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return bytes.Clone(s.entries[id]), s.failure
}
func (s *atomicStateFixture) Save(_ context.Context, id string, b []byte, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure != nil {
		return s.failure
	}
	s.entries[id] = bytes.Clone(b)
	return nil
}
func (s *atomicStateFixture) Update(ctx context.Context, id string, ttl time.Duration, merge func([]byte) ([]byte, error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.failure != nil {
		return s.failure
	}
	if ttl != StateTTL {
		return errors.New("unexpected TTL")
	}
	b, err := merge(bytes.Clone(s.entries[id]))
	if err != nil {
		return err
	}
	s.entries[id] = bytes.Clone(b)
	s.updates++
	return nil
}
func atomicStore() *atomicStateFixture { return &atomicStateFixture{entries: map[string][]byte{}} }
func TestAtomicTrajectoryOverlapReleaseAndExpiry(t *testing.T) {
	store := atomicStore()
	in := input(`{"messages":[{"role":"user","content":"hi"}]}`)
	in.ClientHeaders = http.Header{"Thread-Id": {"conversation"}}
	first, release, err := claimTrajectory(context.Background(), in, "cascade", store)
	if err != nil {
		t.Fatal(err)
	}
	second, done, err := claimTrajectory(context.Background(), in, "cascade", store)
	if err != nil || first == second {
		t.Fatal("overlapping trajectory reused", err)
	}
	if err = done(); err != nil {
		t.Fatal(err)
	}
	if err = release(); err != nil {
		t.Fatal(err)
	}
	if err = release(); err != nil {
		t.Fatal(err)
	}
	reused, releaseNext, err := claimTrajectory(context.Background(), in, "cascade", store)
	if err != nil || reused != first {
		t.Fatal("inactive trajectory not reused", err)
	}
	// Simulate authoritative expiry: the replacement gets a new ID even if the
	// old active turn has not yet released. Its stale release cannot clear it.
	store.mu.Lock()
	store.entries = map[string][]byte{}
	store.mu.Unlock()
	replacement, releaseReplacement, err := claimTrajectory(context.Background(), in, "cascade", store)
	if err != nil || replacement == reused {
		t.Fatal("expired active ID reused", err)
	}
	if err = releaseNext(); err != nil {
		t.Fatal(err)
	}
	conflict, releaseConflict, err := claimTrajectory(context.Background(), in, "cascade", store)
	if err != nil || conflict == replacement {
		t.Fatal("stale release cleared replacement", err)
	}
	_ = releaseConflict()
	_ = releaseReplacement()
	in.ClientHeaders.Set("x-codex-parent-thread-id", "another-parent")
	child, releaseChild, err := claimTrajectory(context.Background(), in, "cascade", store)
	if err != nil || child == first || child == replacement {
		t.Fatal("parent scope aliased", err)
	}
	_ = releaseChild()
	for key, value := range store.entries {
		if strings.Contains(key, "conversation") || bytes.Contains(value, []byte("fixture-key")) || bytes.Contains(value, []byte("conversation")) {
			t.Fatal("raw identity persisted")
		}
	}
}

func TestTrajectoryFencesAndCancellationCleanup(t *testing.T) {
	store := atomicStore()
	in := input(`{}`)
	ctx, cancel := context.WithCancel(context.Background())
	_, release, err := claimTrajectory(ctx, in, "cascade", store)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if err = release(); err != nil {
		t.Fatal("canceled cleanup did not release", err)
	}
	_, release, err = claimTrajectory(context.Background(), in, "cascade", store)
	if err != nil {
		t.Fatal(err)
	}
	store.failure = errors.New("deleted membership")
	if err = release(); !errors.Is(err, store.failure) {
		t.Fatal("cleanup bypassed deletion", err)
	}
	if _, _, err = claimTrajectory(context.Background(), in, "cascade", store); !errors.Is(err, store.failure) {
		t.Fatal("claim bypassed deletion", err)
	}
}

func TestExecuteNormalizesOnlyPreoutputRefusal(t *testing.T) {
	for _, tc := range []struct {
		name      string
		wire      []byte
		status    int
		streamErr bool
	}{
		{"error only", eos(`{"error":{"code":"unauthenticated","message":"secret-canary"}}`), 401, false},
		{"heartbeat before refusal", append(frame(nil), eos(`{"error":{"code":"resource_exhausted"}}`)...), 429, false},
		{"visible then refusal", append(frame(textField(nil, 3, "visible")), eos(`{"error":{"code":"unauthenticated"}}`)...), 200, true},
		{"usage then refusal", append(frame(blob(nil, 7, number(nil, 2, 1))), eos(`{"error":{"code":"unauthenticated"}}`)...), 200, true},
		{"success", append(frame(textField(nil, 3, "ok")), eos(`{}`)...), 200, false},
		{"large prefix", append(frame(textField(nil, 3, strings.Repeat("x", 65<<10))), eos(`{}`)...), 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := New()
			in := input(`{"messages":[{"role":"user","content":"hi"}]}`)
			store := atomicStore()
			ctx, err := a.RestoreState(context.Background(), in, store)
			if err != nil {
				t.Fatal(err)
			}
			sends := 0
			x := executorFunc(func(_ context.Context, r inference.AdapterRequest) (*http.Response, error) {
				sends++
				_, proto, e := readFrame(bytes.NewReader(r.Body))
				if e != nil {
					t.Fatal(e)
				}
				trajectory := string(fields(t, fields(t, proto)[15][0].b)[1][0].b)
				if !uuidShape.MatchString(trajectory) {
					t.Fatal("missing trajectory")
				}
				return unary(tc.wire), nil
			})
			r, wire, err := a.Execute(ctx, in, x, store)
			if err != nil {
				t.Fatal(err)
			}
			if r.StatusCode != tc.status || wire != inference.Chat || sends != 1 {
				t.Fatal(r.StatusCode, wire, sends)
			}
			b, err := io.ReadAll(r.Body)
			_ = r.Body.Close()
			if (err != nil) != tc.streamErr || bytes.Contains(b, []byte("secret-canary")) {
				t.Fatal(string(b), err)
			}
			for _, data := range store.entries {
				var entry trajectoryState
				_ = json.Unmarshal(data, &entry)
				if entry.Token != "" {
					t.Fatal("lease retained after terminal read")
				}
			}
			if tc.status != 200 && attempt(ctx).complete {
				t.Fatal("refusal marked complete")
			}
		})
	}
}

func TestExecuteFailureReleasesWithoutRetry(t *testing.T) {
	a := New()
	in := input(`{"messages":[{"role":"user","content":"hi"}]}`)
	store := atomicStore()
	ctx, err := a.RestoreState(context.Background(), in, store)
	if err != nil {
		t.Fatal(err)
	}
	rejected := errors.New("shared budget exhausted")
	calls := 0
	_, _, err = a.Execute(ctx, in, executorFunc(func(context.Context, inference.AdapterRequest) (*http.Response, error) { calls++; return nil, rejected }), store)
	if !errors.Is(err, rejected) || calls != 1 {
		t.Fatal(err, calls)
	}
	for _, data := range store.entries {
		var entry trajectoryState
		_ = json.Unmarshal(data, &entry)
		if entry.Token != "" {
			t.Fatal("failed send retained lease")
		}
	}
}

func TestExecuteCancellationDuringPrefix(t *testing.T) {
	a := New()
	in := input(`{"messages":[{"role":"user","content":"hi"}]}`)
	store := atomicStore()
	ctx, cancel := context.WithCancel(context.Background())
	ctx, err := a.RestoreState(ctx, in, store)
	if err != nil {
		t.Fatal(err)
	}
	r, w := io.Pipe()
	defer w.Close()
	entered := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		_, _, err := a.Execute(ctx, in, executorFunc(func(context.Context, inference.AdapterRequest) (*http.Response, error) {
			close(entered)
			return &http.Response{StatusCode: 200, Body: r}, nil
		}), store)
		finished <- err
	}()
	<-entered
	cancel()
	select {
	case err = <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("prefix did not cancel")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, data := range store.entries {
		var entry trajectoryState
		_ = json.Unmarshal(data, &entry)
		if entry.Token != "" {
			t.Fatal("cancel retained lease")
		}
	}
}

func TestLeaseBodyCancellationAndReleaseError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r, w := io.Pipe()
	defer w.Close()
	released := make(chan struct{})
	b := newLeaseBody(ctx, r, func() error { close(released); return nil })
	cancel()
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("body cancellation leaked lease")
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	fence := errors.New("deleted")
	b = newLeaseBody(context.Background(), io.NopCloser(strings.NewReader("")), func() error { return fence })
	if _, err := io.ReadAll(b); !errors.Is(err, fence) {
		t.Fatal("release fence hidden", err)
	}
}

func TestStoreFalseNeverClaimsAndCorruptionFails(t *testing.T) {
	store := atomicStore()
	in := input(`{"store":false}`)
	first, done, err := claimTrajectory(context.Background(), in, "cascade", store)
	if err != nil {
		t.Fatal(err)
	}
	_ = done()
	second, done, err := claimTrajectory(context.Background(), in, "cascade", store)
	if err != nil || first == second || store.updates != 0 {
		t.Fatal("store:false retained trajectory", err)
	}
	_ = done()
	in.Body = []byte(`{}`)
	_, done, err = claimTrajectory(context.Background(), in, "cascade", store)
	if err != nil {
		t.Fatal(err)
	}
	_ = done()
	for key := range store.entries {
		store.entries[key] = []byte(`{"version":1,"trajectory":"invalid"}`)
	}
	if _, _, err = claimTrajectory(context.Background(), in, "cascade", store); !errors.Is(err, inference.ErrContinuation) {
		t.Fatal("corrupt lease accepted", err)
	}
}

func TestExecuteSearchUsesUnaryDecoder(t *testing.T) {
	a := New()
	in := input(`{"query":"fixture"}`)
	in.Operation = inference.Search
	calls := 0
	r, wire, err := a.Execute(a.PrepareContext(context.Background()), in, executorFunc(func(_ context.Context, req inference.AdapterRequest) (*http.Response, error) {
		calls++
		if !strings.HasSuffix(req.URL, searchPath) {
			t.Fatal(req.URL)
		}
		return unary(blob(nil, 1, textField(textField(nil, 3, "https://docs.example/search"), 7, "excerpt"))), nil
	}), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	b, err := io.ReadAll(r.Body)
	if err != nil || calls != 1 || wire != inference.Chat || !bytes.Contains(b, []byte(`"results"`)) {
		t.Fatal(string(b), err)
	}
}

type accountingExecutor struct {
	executorFunc
	unmeasured int
}

func (x *accountingExecutor) RecordUnmeasuredAttempt() { x.unmeasured++ }
func TestPrefixFailureKeepsUsageUnknown(t *testing.T) {
	a := New()
	in := input(`{"messages":[{"role":"user","content":"hi"}]}`)
	ctx := a.PrepareContext(context.Background())
	x := &accountingExecutor{executorFunc: func(context.Context, inference.AdapterRequest) (*http.Response, error) {
		return unary([]byte{0, 0}), nil
	}}
	if _, _, err := a.Execute(ctx, in, x, nil); err == nil || x.unmeasured != 1 {
		t.Fatal("discarded leg lost uncertainty", err, x.unmeasured)
	}
	d, err := (RecoveryPolicy{}).RecoverHTTP(ctx, in, inference.HTTPFailure{Status: 200})
	if err != nil || d.Action != inference.RecoveryDefault {
		t.Fatal("success treated as refusal", d, err)
	}
}
