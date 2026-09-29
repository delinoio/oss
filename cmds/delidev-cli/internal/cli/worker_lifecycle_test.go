package cli

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/server"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
)

func TestDetachedWorkerLifecyclePreservesRegistrationAcrossStopAndRestart(t *testing.T) {
	root := filepath.Join(t.TempDir(), "server")
	workerRoot := filepath.Join(root, "worker")
	ctx, cancel := context.WithCancel(context.Background())
	ready, done := make(chan struct{}), make(chan error, 1)
	go func() {
		done <- server.Serve(ctx, server.Config{DataDir: root, Listen: "127.0.0.1:0", Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}, func(server.Endpoint) { close(ready) })
	}()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		t.Fatal("server readiness")
	}
	if code, result := cliRun(t, root, []string{"worker", "pair-local"}, ""); code != 0 {
		t.Fatal(result)
	}
	credential, err := worker.LoadCredential(workerRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if status, err := worker.Status(workerRoot); err == nil && status.Lifecycle.Version != 0 {
			if _, err := stopLocalWorker(context.Background(), workerRoot, status.Lifecycle.Generation); err != nil {
				t.Error("Worker cleanup", err)
			}
		}
	}()
	start := func() worker.RuntimeStatus {
		t.Helper()
		value, err := startDetachedWorker(ctx, options{dataDir: root}, workerRoot)
		if err != nil {
			t.Fatal(value, err)
		}
		status := value.(worker.RuntimeStatus)
		if status.State != worker.StateRunning || !status.ControllerActive {
			t.Fatal(status)
		}
		return status
	}
	first, reused := start(), start()
	if first.Lifecycle.Generation != reused.Lifecycle.Generation {
		t.Fatal("duplicate Worker")
	}
	if _, err := stopLocalWorker(ctx, workerRoot, first.Lifecycle.Generation); err != nil {
		t.Fatal(err)
	}
	if _, err := stopLocalWorker(ctx, workerRoot, first.Lifecycle.Generation); err != nil {
		t.Fatal("stop retry", err)
	}
	// A replacement retains a new instance identity and waits for the server's
	// original 45-second lease; local process exit cannot shorten that authority.
	value, err := startDetachedWorker(ctx, options{dataDir: root}, workerRoot)
	if err != nil && domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal(value, err)
	}
	deadline := time.Now().Add(65 * time.Second)
	var second worker.RuntimeStatus
	for {
		second, err = worker.Status(workerRoot)
		// Status has a bounded metadata-lock wait. A busy native writer can
		// exhaust it on Windows; keep observing within this existing deadline
		// without replaying start or weakening the generation checks below.
		if err != nil && domain.SafeError(err).Code != domain.Conflict {
			t.Fatal(err)
		}
		if err == nil && second.State == worker.StateRunning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("replacement never connected", second, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if second.Lifecycle.Generation == first.Lifecycle.Generation {
		t.Fatal("no new generation")
	}
	if _, err := stopLocalWorker(ctx, workerRoot, first.Lifecycle.Generation); err == nil {
		t.Fatal("old stop canceled new Worker")
	}
	retained, err := worker.LoadCredential(workerRoot)
	if err != nil || retained != credential {
		t.Fatal("registration replaced", err)
	}
	// Stop remains usable after the selected server goes offline. It only joins
	// this Worker controller; session cleanup is still independently retained.
	cancel()
	if _, err := stopLocalWorker(context.Background(), workerRoot, second.Lifecycle.Generation); err != nil {
		t.Fatal(err)
	}
}
