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
	"github.com/delinoio/oss/cmds/delidev-cli/internal/userservice"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func launchFixture(t *testing.T) (options, server.Config, IO) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "scope")
	if err := security.PrivateDir(root); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	o := options{dataDir: root}
	t.Cleanup(func() {
		c, err := connectClient(o, strings.NewReader(""))
		if err != nil {
			return
		}
		defer c.transport.CloseIdleConnections()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = c.system.StopServer(ctx, request(c, &pb.StopServerRequest{RequestId: string(domain.NewID())}))
		for ctx.Err() == nil {
			if gate, err := security.TryLock(filepath.Join(root, "server.lock")); err == nil {
				gate.Close()
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Error("launch fixture cleanup did not join")
	})
	return o, server.Config{DataDir: root, Listen: "127.0.0.1:0"}, IO{In: strings.NewReader(""), Out: io.Discard, Err: io.Discard}
}

func TestDesktopLaunchFreshConcurrentReuseAndStopBoundary(t *testing.T) {
	o, config, streams := launchFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	outcomes := make(chan error, 2)
	for range 2 {
		go func() { _, err := startDetachedMode(ctx, o, config, streams, desktopLaunch); outcomes <- err }()
	}
	for range 2 {
		if err := <-outcomes; err != nil {
			t.Fatal(err)
		}
	}
	first, err := server.LoadEndpoint(o.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := startDetachedMode(ctx, o, config, streams, desktopLaunch); err != nil || result.(map[string]any)["reused"] != true {
		t.Fatal(result, err)
	}
	changed := config
	changed.Listen = "127.0.0.1:1"
	if _, err := startDetachedMode(ctx, o, changed, streams, desktopLaunch); domain.SafeError(err).Code != domain.Unsupported {
		t.Fatal("desktop adopted foreign listener", err)
	}
	second, err := server.LoadEndpoint(o.dataDir)
	if err != nil || first != second {
		t.Fatal("reuse changed identity", err)
	}
	c, err := connectClient(o, streams.In)
	if err != nil {
		t.Fatal(err)
	}
	defer c.transport.CloseIdleConnections()
	if _, err := c.system.StopServer(ctx, request(c, &pb.StopServerRequest{RequestId: string(domain.NewID())})); err != nil {
		t.Fatal(err)
	}
	for {
		lock, err := security.TryLock(filepath.Join(o.dataDir, "server.lock"))
		if err == nil {
			lock.Close()
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	stopped, err := server.ReadLifecycle(o.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []startupMode{intentRecovery, desktopRetry} {
		value, err := startDetachedMode(ctx, o, config, streams, mode)
		if err != nil || value.(map[string]any)["state"] != "stopped" {
			t.Fatal("same-lifetime Stop was reopened", value, err)
		}
	}
	retained, _ := server.ReadLifecycle(o.dataDir)
	if retained != stopped {
		t.Fatal("Stop intent changed")
	}
	if _, err := startDetachedMode(ctx, o, config, streams, desktopLaunch); err != nil {
		t.Fatal("fresh launch failed", err)
	}
	reopened, err := server.LoadEndpoint(o.dataDir)
	if err != nil || reopened.ServerID != first.ServerID {
		t.Fatal("fresh launch lost identity", err)
	}
}

type launchServiceBackend struct {
	present bool
	writes  int
}

func (b *launchServiceBackend) Inspect(context.Context, userservice.Spec) (userservice.Observation, error) {
	return userservice.Observation{Present: b.present}, nil
}
func (b *launchServiceBackend) Install(context.Context, userservice.Spec) error {
	b.present = true
	b.writes++
	return nil
}
func (b *launchServiceBackend) Enable(context.Context, userservice.Spec) error {
	b.writes++
	return nil
}
func (b *launchServiceBackend) Start(context.Context, userservice.Spec) error { b.writes++; return nil }
func (b *launchServiceBackend) Disable(context.Context, userservice.Spec) error {
	b.writes++
	return nil
}
func (b *launchServiceBackend) Remove(context.Context, userservice.Spec) error {
	b.present = false
	b.writes++
	return nil
}

func TestDesktopLaunchNativeServiceAdmissionAndMalformedState(t *testing.T) {
	for _, live := range []bool{false, true} {
		t.Run(map[bool]string{false: "stopped", true: "live"}[live], func(t *testing.T) {
			o, config, streams := launchFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if live {
				if _, err := startDetached(ctx, o, config, streams); err != nil {
					t.Fatal(err)
				}
			}
			m := userservice.New(o.dataDir, userservice.Server, nil)
			backend := &launchServiceBackend{}
			m.Backend = backend
			installed, err := m.Control(ctx, userservice.Install, domain.NewID(), 0, "fixture")
			if err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(filepath.Join(o.dataDir, "user-service-server.json"))
			if err != nil {
				t.Fatal(err)
			}
			value, err := startDetachedMode(ctx, o, config, streams, desktopLaunch)
			if err != nil {
				t.Fatal(err)
			}
			key := "state"
			expected := any("service-managed")
			if live {
				key = "reused"
				expected = true
			}
			if value.(map[string]any)[key] != expected {
				t.Fatal("incorrect service launch", value)
			}
			after, _ := os.ReadFile(filepath.Join(o.dataDir, "user-service-server.json"))
			if string(before) != string(after) || backend.writes != 1 {
				t.Fatal("launch mutated service")
			}
			if !live {
				if _, err := os.Stat(filepath.Join(o.dataDir, "server-lifecycle.json")); !os.IsNotExist(err) {
					t.Fatal("service-owned launch published ordinary intent")
				}
				// Concurrent install/start/remove cannot pass the pinned launch admission.
				gate, err := userservice.LockStartupAdmission(ctx, o.dataDir, userservice.Server)
				if err != nil {
					t.Fatal(err)
				}
				for _, action := range []userservice.Action{userservice.Install, userservice.Start, userservice.Remove} {
					if _, err := m.Control(ctx, action, domain.NewID(), installed.Status.Revision, "fixture"); domain.SafeError(err).Code != domain.Conflict {
						t.Fatal("service control passed launch admission", action, err)
					}
				}
				gate.Close()
				// Ordinary explicit Start still grants its existing user action;
				// Ensure may reuse it without changing native-service intent.
				if _, err := startDetached(ctx, o, config, streams); err != nil {
					t.Fatal("explicit Start changed", err)
				}
				if value, err := ensureDetached(ctx, o, config, streams, true); err != nil || value.(map[string]any)["reused"] != true {
					t.Fatal("Ensure changed", value, err)
				}
			}
			if err := security.WriteAtomic(filepath.Join(o.dataDir, "user-service-server.json"), []byte(`{"broken":true}`)); err != nil {
				t.Fatal(err)
			}
			if _, err := startDetachedMode(ctx, o, config, streams, desktopLaunch); domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatal("malformed registration admitted", err)
			}
		})
	}
}
