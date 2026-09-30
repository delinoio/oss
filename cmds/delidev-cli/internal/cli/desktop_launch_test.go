// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/server"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

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
