// SPDX-License-Identifier: Apache-2.0
package apiproxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type delayedCancellationWriter struct {
	*httptest.ResponseRecorder
	started, release, completed chan struct{}
}

func (w *delayedCancellationWriter) SetReadDeadline(time.Time) error { return nil }

func (w *delayedCancellationWriter) SetWriteDeadline(deadline time.Time) error {
	if !deadline.IsZero() && !deadline.After(time.Now()) {
		close(w.started)
		<-w.release
		close(w.completed)
	}
	return nil
}

func TestProxyJoinsCancellationDeadlineBeforeReturning(t *testing.T) {
	upstreamStarted := make(chan struct{})
	f := newProxyFixture(t, domain.OpenAIChat, []Operation{ChatCompletion}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(upstreamStarted)
		<-r.Context().Done()
	})
	w := &delayedCancellationWriter{ResponseRecorder: httptest.NewRecorder(), started: make(chan struct{}), release: make(chan struct{}), completed: make(chan struct{})}
	returned := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(w.release) }) }
	t.Cleanup(func() {
		f.authority.cancel()
		release()
		select {
		case <-returned:
		case <-time.After(3 * time.Second):
			t.Error("relay handler did not join during fixture cleanup")
		}
	})
	req := httptest.NewRequest(http.MethodPost, Prefix+"/chat/completions", strings.NewReader(`{"model":"fixed-model"}`))
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Authorization", "Bearer "+fixtureToken)
	req.Header.Set("Content-Type", "application/json")
	go func() {
		defer close(returned)
		f.server.Config.Handler.ServeHTTP(w, req)
	}()
	select {
	case <-upstreamStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("provider request did not start")
	}
	f.authority.cancel()
	select {
	case <-w.started:
	case <-time.After(3 * time.Second):
		t.Fatal("revocation did not start downstream cancellation")
	}
	// Deliberately hold the original writer callback. net/http may reuse a
	// connection after ServeHTTP returns, so this work must remain joined.
	select {
	case <-returned:
		t.Fatal("relay returned while its cancellation callback still owned the response writer")
	case <-time.After(50 * time.Millisecond):
	}
	release()
	select {
	case <-returned:
	case <-time.After(3 * time.Second):
		t.Fatal("relay did not return after cancellation completed")
	}
	select {
	case <-w.completed:
	default:
		t.Fatal("relay returned without its original cancellation completion")
	}
	if f.calls.Load() != 1 || f.authority.keys.Load() != 1 || f.authority.releases.Load() != 1 {
		t.Fatal("cancellation changed original upstream or lease ownership")
	}
}
