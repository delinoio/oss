// SPDX-License-Identifier: Apache-2.0
package tokenprices

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func private(t *testing.T) string {
	t.Helper()
	p := t.TempDir()
	if e := os.Chmod(p, 0700); e != nil {
		t.Fatal(e)
	}
	return p
}
func TestCoalesceAndRetainStale(t *testing.T) {
	var calls atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	fail := false
	tr := transport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != URL || r.Header.Get("Accept") != "application/json" {
			t.Error("unpinned request")
		}
		calls.Add(1)
		if fail {
			return nil, errors.New("secret transport detail")
		}
		close(entered)
		<-release
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(fixture))}, nil
	})
	root := private(t)
	m := New(root, tr, nil, nil)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if e := m.Refresh(context.Background()); e != nil {
			t.Error(e)
		}
	}()
	<-entered
	wg.Add(1)
	go func() {
		defer wg.Done()
		if e := m.Refresh(context.Background()); e != nil {
			t.Error(e)
		}
	}()
	time.Sleep(10 * time.Millisecond)
	close(release)
	wg.Wait()
	if calls.Load() != 1 || m.Snapshot().State != Current {
		t.Fatal("not coalesced")
	}
	fail = true
	if e := m.Refresh(context.Background()); e == nil {
		t.Fatal("failure missing")
	}
	if m.Snapshot().State != Stale || len(m.Snapshot().Catalog.References) != 2 {
		t.Fatal("last valid lost")
	}
	restored := New(root, tr, nil, nil)
	if restored.Snapshot().State != Stale || restored.Snapshot().Failure == "" {
		t.Fatal("restart lost failure backoff")
	}
	before := calls.Load()
	if e := m.Refresh(context.Background()); e == nil || calls.Load() != before {
		t.Fatal("failure backoff bypassed")
	}
}
func TestPrivateCacheAndColdFailure(t *testing.T) {
	root := private(t)
	tr := transport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(fixture))}, nil
	})
	m := New(root, tr, nil, nil)
	if e := m.Refresh(context.Background()); e != nil {
		t.Fatal(e)
	}
	restored := New(root, tr, nil, nil)
	if restored.Snapshot().State != Current || restored.Snapshot().Catalog.Digest != m.Snapshot().Catalog.Digest {
		t.Fatal("valid cache not restored")
	}
	cold := New(private(t), nil, nil, nil)
	if e := cold.Refresh(context.Background()); e == nil || cold.Snapshot().State != Unavailable {
		t.Fatal("cold failure inferred price")
	}
}

func TestShutdownCancelsAndJoinsExplicitRefresh(t *testing.T) {
	entered := make(chan struct{})
	done := make(chan struct{})
	m := New(private(t), transport(func(r *http.Request) (*http.Response, error) {
		close(entered)
		<-r.Context().Done()
		return nil, r.Context().Err()
	}), nil, nil)
	go func() { defer close(done); _ = m.Refresh(context.Background()) }()
	<-entered
	m.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("refresh not joined")
	}
	if e := m.Refresh(context.Background()); e == nil {
		t.Fatal("closed manager admitted work")
	}
}

func TestExactSnapshotIsIndependentAndBounded(t *testing.T) {
	m := &Manager{state: Snapshot{State: Current, Catalog: Catalog{Digest: strings.Repeat("a", 64), References: map[string]Reference{
		"openai\x00exact":    {Provider: "openai", Model: "exact", Costs: []byte(`{"input":0}`)},
		"anthropic\x00exact": {Provider: "anthropic", Model: "exact"},
	}}}}
	snapshot := m.SnapshotFor("openai\x00exact")
	if len(snapshot.Catalog.References) != 1 {
		t.Fatal("cross-source or catalog data copied")
	}
	r := snapshot.Catalog.References["openai\x00exact"]
	r.Costs[0] = 'x'
	snapshot.Catalog.References["openai\x00exact"] = r
	if m.SnapshotFor("openai\x00exact").Catalog.References["openai\x00exact"].Costs[0] != '{' {
		t.Fatal("caller changed cache")
	}
	if len(m.SnapshotFor("openai\x00missing").Catalog.References) != 0 {
		t.Fatal("non-exact fallback")
	}
}

func TestScheduledStartupDailyAndFailureBackoff(t *testing.T) {
	var clock atomic.Int64
	clock.Store(time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC).Unix())
	var failure atomic.Bool
	calls := make(chan bool, 8)
	m := New(private(t), transport(func(*http.Request) (*http.Response, error) {
		fail := failure.Load()
		calls <- fail
		if fail {
			return nil, errors.New("fixture failure")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(fixture))}, nil
	}), nil, nil)
	m.now = func() time.Time { return time.Unix(clock.Load(), 0).UTC() }
	ctx, cancel := context.WithCancel(context.Background())
	joined := make(chan struct{})
	go func() { defer close(joined); m.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case <-joined:
		case <-time.After(time.Second):
			t.Fatal("scheduled refresh did not join")
		}
	}()
	await := func(want bool) {
		t.Helper()
		select {
		case got := <-calls:
			if got != want {
				t.Fatal("wrong scheduled outcome", got)
			}
		case <-time.After(time.Second):
			t.Fatal("due refresh did not run")
		}
	}
	awaitPublished := func(ready func(Snapshot) bool) {
		t.Helper()
		deadline := time.Now().Add(time.Second)
		for !ready(m.Snapshot()) {
			if time.Now().After(deadline) {
				t.Fatal("scheduled publication did not settle")
			}
			time.Sleep(time.Millisecond)
		}
	}
	await(false)
	// Wait until publication finishes before waking the next scheduler decision.
	awaitPublished(func(s Snapshot) bool { return !s.Checked.IsZero() })
	m.wake <- struct{}{}
	select {
	case <-calls:
		t.Fatal("successful refresh ignored daily interval")
	case <-time.After(10 * time.Millisecond):
	}
	clock.Add(int64(SuccessInterval / time.Second))
	m.wake <- struct{}{}
	await(false)
	awaitPublished(func(s Snapshot) bool { return s.Checked.Unix() == clock.Load() })
	failure.Store(true)
	clock.Add(int64(SuccessInterval / time.Second))
	m.wake <- struct{}{}
	await(true)
	awaitPublished(func(s Snapshot) bool { return s.Failure != "" })
	if err := m.Refresh(context.Background()); err == nil {
		t.Fatal("explicit refresh bypassed failure interval")
	}
	select {
	case <-calls:
		t.Fatal("failure backoff made another request")
	default:
	}
	clock.Add(int64(FailureInterval / time.Second))
	m.wake <- struct{}{}
	await(true)
}

func TestFixedUpstreamRejectsRedirectWithoutSecondRequest(t *testing.T) {
	var calls atomic.Int32
	m := New(private(t), transport(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		if r.URL.String() != URL {
			t.Fatal("redirect changed upstream")
		}
		return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{"https://unreviewed.example/prices"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	}), nil, nil)
	defer m.Close()
	if err := m.Refresh(context.Background()); err == nil || calls.Load() != 1 || m.Snapshot().State != Unavailable {
		t.Fatal("redirect granted price authority", err, calls.Load())
	}
}
