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
