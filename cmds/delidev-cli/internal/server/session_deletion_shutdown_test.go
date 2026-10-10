// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/desktopruntime"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

// Hold an actual maintenance pass at its classified scan failure. Cancellation
// is observed separately from returning so the test cannot mistake it for a join.
type deletionShutdownLog struct {
	slog.Handler
	entered  chan struct{}
	canceled chan struct{}
	release  chan struct{}
	stopped  chan struct{}
	scanOnce sync.Once
	stopOnce sync.Once
	stops    atomic.Int32
}

func (h *deletionShutdownLog) Handle(ctx context.Context, record slog.Record) error {
	if record.Message == "session_deletion_scan_failed" {
		h.scanOnce.Do(func() {
			close(h.entered)
			<-ctx.Done()
			close(h.canceled)
			<-h.release
		})
	}
	if record.Message == "server_stopped" {
		h.stops.Add(1)
		h.stopOnce.Do(func() { close(h.stopped) })
	}
	return h.Handler.Handle(ctx, record)
}

func TestServerStopJoinsSessionDeletionPassBeforeReportingStopped(t *testing.T) {
	for _, listenerFailure := range []bool{false, true} {
		name := "normal-stop"
		if listenerFailure {
			name = "listener-failure"
		}
		t.Run(name, func(t *testing.T) { testServerSessionDeletionShutdown(t, listenerFailure) })
	}
}

func testServerSessionDeletionShutdown(t *testing.T, listenerFailure bool) {
	root := filepath.Join(t.TempDir(), "server")
	h := &deletionShutdownLog{Handler: slog.NewTextHandler(io.Discard, nil), entered: make(chan struct{}), canceled: make(chan struct{}), release: make(chan struct{}), stopped: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	finished := make(chan struct{})
	ready := make(chan error, 1)
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(h.release) }) }
	config := Config{DataDir: root, Listen: "127.0.0.1:0", Logger: slog.New(h), DisableBackgroundMaintenanceForTesting: true}
	var listener net.Listener
	if listenerFailure {
		var err error
		listener, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { listener.Close() })
		key, err := security.RandomToken()
		if err != nil {
			t.Fatal(err)
		}
		config.Listener = listener
		config.Listen = listener.Addr().String()
		config.Desktop = &desktopruntime.Target{Version: 2, Endpoint: "http://" + config.Listen, Generation: domain.NewID(), Key: key}
	}
	go func() {
		defer close(finished)
		done <- Serve(ctx, config, func(Endpoint) {
			// Introduce invalid retained evidence after startup recovery. The live
			// controller must classify it as pending, never delete or adopt it.
			ready <- security.WriteAtomic(filepath.Join(root, "session-deletions", string(domain.NewID())+".json"), []byte(`{"version":99}`))
		})
	}()
	t.Cleanup(func() {
		cancel()
		release()
		select {
		case <-finished:
			err := <-done
			if listenerFailure {
				if domain.SafeError(err).Code != domain.Unavailable {
					t.Error("listener failure outcome", err)
				}
			} else if err != nil {
				t.Error("server cleanup", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("server cleanup did not join")
		}
	})
	select {
	case err := <-ready:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("server readiness timed out")
	}
	select {
	case <-h.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("session deletion did not reach its retained scan failure")
	}
	if listenerFailure {
		if err := listener.Close(); err != nil {
			t.Fatal(err)
		}
	} else {
		cancel()
	}
	select {
	case <-h.canceled:
	case <-time.After(10 * time.Second):
		t.Fatal("session deletion did not observe shutdown")
	}
	select {
	case <-h.stopped:
		t.Fatal("server_stopped preceded the session deletion join")
	case <-time.After(100 * time.Millisecond):
	}
	// The same original database scope stays owned while the pass is blocked.
	if replacement, err := store.Open(context.Background(), root); err == nil {
		replacement.Close()
		t.Fatal("database scope retired before the maintenance pass joined")
	}
	release()
	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal("server did not join deletion cleanup")
	}
	wantStops := int32(1)
	if listenerFailure {
		wantStops = 0
	}
	if h.stops.Load() != wantStops {
		t.Fatal("unexpected stopped record count", h.stops.Load())
	}
	entries, err := os.ReadDir(filepath.Join(root, "session-deletions"))
	if err != nil || len(entries) != 1 {
		t.Fatal("shutdown changed uncertain deletion evidence", entries, err)
	}
}

func TestServerStopWithoutSessionDeletionsReportsOnce(t *testing.T) {
	h := &deletionShutdownLog{Handler: slog.NewTextHandler(io.Discard, nil), stopped: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, Config{DataDir: filepath.Join(t.TempDir(), "server"), Listen: "127.0.0.1:0", Logger: slog.New(h), DisableBackgroundMaintenanceForTesting: true}, func(Endpoint) { close(ready) })
	}()
	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		t.Fatal("server readiness timed out")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("empty deletion controller did not join")
	}
	if h.stops.Load() != 1 {
		t.Fatal("unexpected stopped record count", h.stops.Load())
	}
}
