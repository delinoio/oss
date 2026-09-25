package cli

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/server"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestServerLifecycleNeverTargetsPairedRemoteScope(t *testing.T) {
	root := filepath.Join(t.TempDir(), "client")
	if err := security.PrivateDir(root); err != nil {
		t.Fatal(err)
	}
	token, err := worker.RandomToken()
	if err != nil {
		t.Fatal(err)
	}
	credential := worker.Credential{Version: 1, Type: domain.ClientDevice, Endpoint: "https://remote.example.test", ServerID: domain.NewID(), DeviceID: domain.NewID(), PairingID: domain.NewID(), Token: token}
	raw, _ := json.Marshal(credential)
	if err := security.WriteAtomic(filepath.Join(root, "device.json"), raw); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"start", "ensure", "run"} {
		if code := Run(context.Background(), []string{"--data-dir", root, "server", action}, IO{In: strings.NewReader(""), Out: io.Discard, Err: io.Discard}); code != (&domain.Error{Code: domain.PermissionDenied}).ExitCode() {
			t.Fatal("paired scope lifecycle", action, code)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "server-lifecycle.json")); !os.IsNotExist(err) {
		t.Fatal("paired scope gained server intent")
	}
}

func TestAutomaticStartupRecoversOnlyRunningOriginalConfiguration(t *testing.T) {
	root := filepath.Join(t.TempDir(), "server")
	o := options{dataDir: root}
	config := server.Config{DataDir: root, Listen: "127.0.0.1:0", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	streams := IO{In: strings.NewReader(""), Out: io.Discard, Err: io.Discard}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if result, err := ensureDetached(ctx, o, config, streams, true); err != nil || result.(map[string]any)["state"] != "stopped" {
		t.Fatal("unconfigured automatic startup", result, err)
	}
	if _, err := os.Stat(filepath.Join(root, "owner.json")); !os.IsNotExist(err) {
		t.Fatal("automatic startup initialized an owner")
	}
	// Context loss stands in for an abnormal controller exit: no explicit stop
	// intent is committed, and actual retained SQLite/identity must survive.
	foreground, exit := context.WithCancel(ctx)
	ready := make(chan server.Endpoint, 1)
	done := make(chan error, 1)
	go func() { done <- server.Serve(foreground, config, func(e server.Endpoint) { ready <- e }) }()
	var first server.Endpoint
	select {
	case first = <-ready:
	case err := <-done:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal("readiness timeout")
	}
	exit()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	defer func() {
		c, err := connectClient(o, streams.In)
		if err != nil {
			return
		}
		defer c.transport.CloseIdleConnections()
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		_, _ = c.system.StopServer(stopCtx, request(c, &pb.StopServerRequest{RequestId: string(domain.NewID())}))
		for stopCtx.Err() == nil {
			if _, err := os.Stat(filepath.Join(root, "server.json")); os.IsNotExist(err) {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	changed := config
	changed.AllowedOrigins = []string{"tauri://localhost"}
	if _, err := ensureDetached(ctx, o, changed, streams, true); domain.SafeError(err).Code != domain.Unsupported {
		t.Fatal("automatic replacement configuration", err)
	}
	result, err := ensureDetached(ctx, o, config, streams, true)
	if err != nil || result.(map[string]any)["started"] != true {
		t.Fatal("abnormal exit not recovered", result, err)
	}
	second, err := server.LoadEndpoint(root)
	if err != nil || second.ServerID != first.ServerID || second.StartedAt == first.StartedAt {
		t.Fatal("recovery scope", second, err)
	}
	if result, err = ensureDetached(ctx, o, config, streams, true); err != nil || result.(map[string]any)["reused"] != true {
		t.Fatal("compatible server not reused", result, err)
	}
	c, err := connectClient(o, streams.In)
	if err != nil {
		t.Fatal(err)
	}
	defer c.transport.CloseIdleConnections()
	if _, err := c.system.StopServer(ctx, request(c, &pb.StopServerRequest{RequestId: string(domain.NewID())})); err != nil {
		t.Fatal(err)
	}
	if result, err = ensureDetached(ctx, o, config, streams, true); err != nil || result.(map[string]any)["state"] != "stopped" {
		t.Fatal("explicit stop restarted", result, err)
	}
	for {
		if _, err := os.Stat(filepath.Join(root, "server.json")); os.IsNotExist(err) {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("stop timeout")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// The ordinary local stop also suppresses restart when no endpoint remains,
	// while truthfully refusing to confirm process cleanup.
	if code := Run(ctx, []string{"--data-dir", root, "server", "stop"}, streams); code != (&domain.Error{Code: domain.RecoveryRequired}).ExitCode() {
		t.Fatal("offline stop exit", code)
	}
	intent, err := server.ReadLifecycle(root)
	if err != nil || intent.State != server.DesiredStopped {
		t.Fatal("offline suppression", intent, err)
	}
	if result, err = startDetached(ctx, o, config, streams); err != nil || result.(map[string]any)["started"] != true {
		t.Fatal("explicit restart failed", result, err)
	}
	lock, err := security.TryLock(filepath.Join(root, "startup.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if _, err := ensureDetached(ctx, o, config, streams, true); domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("concurrent controller not excluded", err)
	}
}
