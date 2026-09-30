// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/server"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func TestDesktopLaunchAdmissionPreservesRelativeEnsureScope(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private")
	working, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(working, root)
	if err != nil {
		t.Fatal(err)
	}
	if code := Run(context.Background(), []string{"--data-dir", relative, "server", "ensure"}, IO{In: strings.NewReader(""), Out: io.Discard, Err: io.Discard}); code != 0 {
		t.Fatal("relative ensure scope rejected", code)
	}
	if intent, err := server.ReadLifecycle(root); err != nil || intent.Version != 0 {
		t.Fatal("unconfigured ensure gained restart intent", intent, err)
	}
	if _, err := os.Stat(filepath.Join(root, "owner.json")); !os.IsNotExist(err) {
		t.Fatal("unconfigured ensure initialized an owner")
	}
}

func TestDesktopLaunchJoinsStillAnsweringStoppedServer(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private")
	if err := security.PrivateDir(root); err != nil {
		t.Fatal(err)
	}
	owner, err := security.CreateIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	config := server.Config{DataDir: root, Listen: "127.0.0.1:0"}
	if _, err := server.WriteRunning(root, config); err != nil {
		t.Fatal(err)
	}
	if err := server.SuppressLocalRestart(root, domain.NewID()); err != nil {
		t.Fatal(err)
	}
	original, err := server.ReadLifecycle(root)
	if err != nil {
		t.Fatal(err)
	}
	observed := make(chan struct{}, 4)
	var peer *httptest.Server
	peer = httptest.NewServer(connect.NewUnaryHandler(delidevv1connect.SystemServiceGetStatusProcedure,
		func(_ context.Context, req *connect.Request[pb.GetStatusRequest]) (*connect.Response[pb.GetStatusResponse], error) {
			if req.Header().Get("Authorization") != "Bearer "+owner.Token {
				return nil, connect.NewError(connect.CodeUnauthenticated, nil)
			}
			observed <- struct{}{}
			return connect.NewResponse(&pb.GetStatusResponse{Version: rpc.Version, ProtocolVersion: rpc.ProtocolVersion, ServerId: string(owner.ServerID), Listener: peer.URL, Stopping: true}), nil
		}))
	defer peer.Close()
	endpoint := server.Endpoint{URL: peer.URL, ServerID: owner.ServerID, Version: rpc.Version, ProtocolVersion: rpc.ProtocolVersion, StartedAt: time.Now().UTC()}
	raw, _ := json.Marshal(endpoint)
	if err := security.WriteAtomic(filepath.Join(root, "server.json"), raw); err != nil {
		t.Fatal(err)
	}
	ownership, err := security.TryLock(filepath.Join(root, "server.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer ownership.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	o := options{dataDir: root}
	streams := IO{In: strings.NewReader(""), Out: io.Discard, Err: io.Discard}
	for _, mode := range []startupMode{startupDesktopRetry, startupEnsure, startupObservation} {
		result, err := detachedStartup(ctx, o, config, streams, mode)
		if err != nil || result.(map[string]any)["state"] != "stopped" {
			t.Fatal("Stop was not preserved", mode, result, err)
		}
	}
	if _, err := startDetached(ctx, o, config, streams); domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("ordinary Start lost live-stopping conflict", err)
	}
	<-observed
	done := make(chan error, 1)
	go func() { _, err := detachedStartup(ctx, o, config, streams, startupDesktopLaunch); done <- err }()
	select {
	case <-observed:
	case err := <-done:
		t.Fatal("fresh launch did not inspect the stopping server", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case err := <-done:
		t.Fatal("fresh launch did not join pending cleanup", err)
	case <-time.After(100 * time.Millisecond):
	}
	retained, err := server.ReadLifecycle(root)
	if err != nil || retained != original {
		t.Fatal("pending cleanup replaced Stop intent", err)
	}
	if _, err := os.Stat(filepath.Join(root, "server.log")); !os.IsNotExist(err) {
		t.Fatal("pending cleanup spawned a replacement")
	}
	peer.Close()
	if err := os.Remove(filepath.Join(root, "server.json")); err != nil {
		t.Fatal(err)
	}
	if err := ownership.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal("fresh launch did not resume after cleanup", err)
	}
	c, err := connectClient(o, streams.In)
	if err != nil {
		t.Fatal(err)
	}
	defer c.transport.CloseIdleConnections()
	defer c.system.StopServer(context.Background(), request(c, &pb.StopServerRequest{RequestId: string(domain.NewID())}))
	next, err := server.ReadLifecycle(root)
	if err != nil || next.State != server.DesiredRunning || next.Generation == original.Generation {
		t.Fatal("fresh launch did not publish a replacement generation", next, err)
	}
}

func TestDesktopLaunchPreservesLegacyListenerAndAbsentConfiguration(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private")
	config := server.Config{DataDir: root, Listen: "127.0.0.1:0"}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	foreground, exit := context.WithCancel(ctx)
	defer exit()
	ready, done := make(chan server.Endpoint, 1), make(chan error, 1)
	go func() { done <- server.Serve(foreground, config, func(e server.Endpoint) { ready <- e }) }()
	var endpoint server.Endpoint
	select {
	case endpoint = <-ready:
	case err := <-done:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// Simulate a live pre-lifecycle server without changing its actual listener,
	// owner identity, database or running process.
	if err := os.Remove(filepath.Join(root, "server-lifecycle.json")); err != nil {
		t.Fatal(err)
	}
	o := options{dataDir: root}
	streams := IO{In: strings.NewReader(""), Out: io.Discard, Err: io.Discard}
	desktop := config
	desktop.Listen = "127.0.0.1:46310"
	for _, mode := range []startupMode{startupDesktopLaunch, startupDesktopRetry} {
		if _, err := detachedStartup(ctx, o, desktop, streams, mode); domain.SafeError(err).Code != domain.Unsupported {
			t.Fatal("desktop accepted a different legacy listener", mode, err)
		}
		if retained, err := server.ReadLifecycle(root); err != nil || retained.Version != 0 {
			t.Fatal("desktop invented legacy configuration", retained, err)
		}
		if retained, err := server.LoadEndpoint(root); err != nil || retained != endpoint {
			t.Fatal("desktop changed the legacy endpoint", retained, err)
		}
	}
	desktop.Listen = strings.TrimPrefix(endpoint.URL, "http://")
	desktop.AllowedOrigins = []string{"tauri://localhost"}
	if result, err := detachedStartup(ctx, o, desktop, streams, startupDesktopLaunch); err != nil || result.(map[string]any)["reused"] != true {
		t.Fatal("compatible legacy listener not reused", result, err)
	}
	if retained, err := server.ReadLifecycle(root); err != nil || retained.Version != 0 {
		t.Fatal("reuse invented unproved origin configuration", retained, err)
	}
	exit()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if result, err := ensureDetached(ctx, o, desktop, streams, true); err != nil || result.(map[string]any)["state"] != "stopped" {
		t.Fatal("legacy exit acquired desktop restart intent", result, err)
	}
}

func TestDesktopLaunchInitializesReusesAndPreservesStop(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private")
	config := server.Config{DataDir: root, Listen: "127.0.0.1:0"}
	o := options{dataDir: root}
	streams := IO{In: strings.NewReader(""), Out: io.Discard, Err: io.Discard}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	defer func() {
		c, err := connectClient(o, streams.In)
		if err == nil {
			defer c.transport.CloseIdleConnections()
			_, _ = c.system.StopServer(ctx, request(c, &pb.StopServerRequest{RequestId: string(domain.NewID())}))
		}
	}()
	// Two fresh desktop processes contend for the same scope admission.
	results := make(chan error, 2)
	for range 2 {
		go func() { _, err := detachedStartup(ctx, o, config, streams, startupDesktopLaunch); results <- err }()
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal("concurrent launch", err)
		}
	}
	first, err := server.LoadEndpoint(root)
	if err != nil {
		t.Fatal(err)
	}
	intent, err := server.ReadLifecycle(root)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		result, err := detachedStartup(ctx, o, config, streams, startupObservation)
		if err != nil || result.(map[string]any)["reused"] != true {
			t.Fatal("readiness observation", result, err)
		}
	}
	c, err := connectClient(o, streams.In)
	if err != nil {
		t.Fatal(err)
	}
	defer c.transport.CloseIdleConnections()
	if _, err := c.system.StopServer(ctx, request(c, &pb.StopServerRequest{RequestId: string(domain.NewID())})); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []startupMode{startupObservation, startupDesktopRetry, startupEnsure} {
		result, err := detachedStartup(ctx, o, config, streams, mode)
		if err != nil || result.(map[string]any)["state"] != "stopped" {
			t.Fatal("Stop undone by observer/retry", mode, result, err)
		}
	}
	for {
		lock, err := security.TryLock(filepath.Join(root, "server.lock"))
		if err == nil {
			lock.Close()
			break
		}
		if ctx.Err() != nil {
			t.Fatal("original cleanup did not join")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := detachedStartup(ctx, o, config, streams, startupDesktopLaunch); err != nil {
		t.Fatal("fresh launch after cleanup", err)
	}
	second, err := server.LoadEndpoint(root)
	if err != nil || second.ServerID != first.ServerID || second.StartedAt == first.StartedAt {
		t.Fatal("fresh launch identity", second, err)
	}
	next, err := server.ReadLifecycle(root)
	if err != nil || next.Generation == intent.Generation {
		t.Fatal("fresh launch generation", err)
	}
}

func TestDesktopLaunchPreservesMalformedServiceAndIncompleteCleanup(t *testing.T) {
	for _, mode := range []string{"service", "cleanup"} {
		t.Run(mode, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "private")
			if err := security.PrivateDir(root); err != nil {
				t.Fatal(err)
			}
			config := server.Config{DataDir: root, Listen: "127.0.0.1:0"}
			var held *security.Lock
			original := []byte("{malformed-registration}")
			if mode == "service" {
				if err := security.WriteAtomic(filepath.Join(root, "user-service-server.json"), original); err != nil {
					t.Fatal(err)
				}
			} else {
				var err error
				held, err = security.TryLock(filepath.Join(root, "server.lock"))
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
			}
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			_, err := detachedStartup(ctx, options{dataDir: root}, config, IO{In: strings.NewReader(""), Out: io.Discard, Err: io.Discard}, startupDesktopLaunch)
			if err == nil {
				t.Fatal("unsafe launch admitted")
			}
			if _, err := os.Stat(filepath.Join(root, "server-lifecycle.json")); !os.IsNotExist(err) {
				t.Fatal("failed admission published intent")
			}
			if _, err := os.Stat(filepath.Join(root, "server.log")); !os.IsNotExist(err) {
				t.Fatal("failed admission spawned")
			}
			if mode == "service" {
				bytes, err := os.ReadFile(filepath.Join(root, "user-service-server.json"))
				if err != nil || string(bytes) != string(original) {
					t.Fatal("registration changed")
				}
			}
		})
	}
}
func TestDesktopPairingJoinsOriginalControllerAndPreservesRecoveryEvidence(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private")
	if err := security.PrivateDir(root); err != nil {
		t.Fatal(err)
	}
	client := filepath.Join(root, "desktop-client")
	lock, err := security.TryLock(filepath.Join(root, "desktop-recovery.lock"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := joinDesktopClient(ctx, root, client); err == nil || ctx.Err() == nil {
		t.Fatal("joined controller ignored cancellation", err)
	}
	lock.Close()
	joined, err := joinDesktopClient(context.Background(), root, client)
	if err != nil || joined == nil {
		t.Fatal("joined controller did not acquire original lock", err)
	}
	joined.Close()
	original := []byte("{unresolved-original-recovery}")
	path := filepath.Join(root, "desktop-recovery.json")
	if err := security.WriteAtomic(path, original); err != nil {
		t.Fatal(err)
	}
	if _, err := joinDesktopClient(context.Background(), root, client); err == nil {
		t.Fatal("joined controller replaced unresolved recovery")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(original) {
		t.Fatal("joined controller changed original evidence", err)
	}
}
