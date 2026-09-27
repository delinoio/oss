package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func TestStopCommitsSuppressionBeforeAcknowledgement(t *testing.T) {
	root := filepath.Join(t.TempDir(), "server")
	endpoint, identity, cancel, done := runTestServer(t, root)
	defer cancel()
	client := delidevv1connect.NewSystemServiceClient(http.DefaultClient, endpoint.URL)
	ctx, timeout := context.WithTimeout(context.Background(), 5*time.Second)
	defer timeout()
	before, err := ReadLifecycle(root)
	if err != nil || before.State != DesiredRunning {
		t.Fatal("running intent", before, err)
	}
	// Invalid/unauthorized stop attempts must not change the durable intent.
	for _, request := range []*connect.Request[pb.StopServerRequest]{
		connect.NewRequest(&pb.StopServerRequest{RequestId: string(domain.NewID())}),
		ownerRequest(identity, &pb.StopServerRequest{RequestId: "invalid"}),
	} {
		if _, err := client.StopServer(ctx, request); err == nil {
			t.Fatal("invalid stop accepted")
		}
	}
	if after, err := ReadLifecycle(root); err != nil || before != after {
		t.Fatal("rejected stop changed intent", after, err)
	}
	id := domain.NewID()
	if _, err := client.StopServer(ctx, ownerRequest(identity, &pb.StopServerRequest{RequestId: string(id)})); err != nil {
		t.Fatal(err)
	}
	after, err := ReadLifecycle(root)
	if err != nil || after.State != DesiredStopped || after.Generation != id || before.Configuration != after.Configuration {
		t.Fatal("stop acknowledged before intent", after, err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("stop did not complete")
	}
	if err := Serve(ctx, Config{DataDir: root, Listen: "127.0.0.1:0", AllowedOrigins: []string{"tauri://localhost"}, StartupID: before.Generation}, nil); domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("late start ignored stop", err)
	}
	restarted, owner, shutdown, finished := runTestServer(t, root)
	defer func() {
		shutdown()
		if err := <-finished; err != nil {
			t.Error(err)
		}
	}()
	current, err := ReadLifecycle(root)
	if err != nil || current.State != DesiredRunning {
		t.Fatal("explicit new start", err)
	}
	client = delidevv1connect.NewSystemServiceClient(http.DefaultClient, restarted.URL)
	if _, err := client.StopServer(ctx, ownerRequest(owner, &pb.StopServerRequest{RequestId: string(id)})); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("old stop receipt affected new process", err)
	}
	if after, err := ReadLifecycle(root); err != nil || after != current {
		t.Fatal("old stop changed new intent", after, err)
	}
}

func TestLifecycleRejectsCorruptionAndSupersededStartup(t *testing.T) {
	root := filepath.Join(t.TempDir(), "server")
	if err := security.PrivateDir(root); err != nil {
		t.Fatal(err)
	}
	config := Config{DataDir: root, Listen: "127.0.0.1:0"}
	first, err := WriteRunning(root, config)
	if err != nil {
		t.Fatal(err)
	}
	second, err := WriteRunning(root, config)
	if err != nil || first.Generation == second.Generation {
		t.Fatal("new start generation", err)
	}
	config.StartupID = first.Generation
	if err := Serve(context.Background(), config, nil); domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("superseded startup accepted", err)
	}
	if err := os.WriteFile(filepath.Join(root, "server-lifecycle.json"), []byte(`{"version":99}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteRunning(root, config); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("corruption overwritten", err)
	}
	if err := Serve(context.Background(), config, nil); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("corruption ignored", err)
	}
	if _, err := os.Stat(filepath.Join(root, "state.sqlite")); !os.IsNotExist(err) {
		t.Fatal("rejected startup created a database", err)
	}
}

func TestLifecycleLockPreventsConcurrentIntentChange(t *testing.T) {
	root := filepath.Join(t.TempDir(), "server")
	if err := security.PrivateDir(root); err != nil {
		t.Fatal(err)
	}
	if _, err := security.CreateIdentity(root); err != nil {
		t.Fatal(err)
	}
	lock, err := LockLifecycle(root)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := SuppressLocalRestart(root, domain.NewID()); domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("overlapping native barrier", err)
	}
}

type blockedReadyLog struct {
	slog.Handler
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (h *blockedReadyLog) Handle(ctx context.Context, record slog.Record) error {
	if record.Message == "server_ready" {
		h.once.Do(func() { close(h.entered) })
		<-h.release
	}
	return h.Handler.Handle(ctx, record)
}

func TestHTTPReadinessReleasesStartupLifecycleOwnership(t *testing.T) {
	root := filepath.Join(t.TempDir(), "server")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	handler := &blockedReadyLog{Handler: slog.NewTextHandler(io.Discard, nil), entered: make(chan struct{}), release: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, Config{DataDir: root, Listen: "127.0.0.1:0", Logger: slog.New(handler), disableCatalogMaintenance: true}, nil)
	}()
	defer func() {
		close(handler.release)
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error("server cleanup", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("server cleanup did not join")
		}
	}()
	select {
	case <-handler.entered:
	case err := <-done:
		done <- err
		t.Fatal("server did not reach readiness logging", err)
	case <-ctx.Done():
		t.Fatal("server did not reach readiness logging")
	}
	endpoint, err := LoadEndpoint(root)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := security.LoadIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	client := delidevv1connect.NewSystemServiceClient(http.DefaultClient, endpoint.URL)
	if _, err := client.GetStatus(ctx, ownerRequest(identity, &pb.GetStatusRequest{})); err != nil {
		t.Fatal("authenticated readiness", err)
	}
	// A successful readiness probe must permit immediate reuse and Stop, even
	// if the startup goroutine is still blocked writing its informational log.
	lock, err := LockLifecycle(root)
	if err != nil {
		t.Fatal("HTTP readiness preceded startup ownership release", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	request := domain.NewID()
	if _, err := client.StopServer(ctx, ownerRequest(identity, &pb.StopServerRequest{RequestId: string(request)})); err != nil {
		t.Fatal("ready server could not accept explicit Stop", err)
	}
	intent, err := ReadLifecycle(root)
	if err != nil || intent.State != DesiredStopped || intent.Generation != request {
		t.Fatal("ready server did not persist explicit stop intent", err)
	}
}
