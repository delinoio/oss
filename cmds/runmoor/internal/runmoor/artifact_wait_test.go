package runmoor

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"
)

func TestGlobalDrainWaitsForManagedArtifacts(t *testing.T) {
	for _, online := range []bool{false, true} {
		for _, tc := range []struct {
			phase             ArtifactPhase
			reserved, pending bool
		}{
			{ArtifactPreparing, true, true}, {ArtifactRemoving, false, true}, {ArtifactReady, false, false},
		} {
			t.Run(fmt.Sprintf("online=%t/%s", online, tc.phase), func(t *testing.T) {
				m, c, _, _, _ := testManager(t)
				if err := m.Store.Update(func(s *Snapshot) error {
					s.Stopping = true
					s.Artifacts["artifact"] = &RunnerArtifact{ID: "artifact", Backend: Docker, Phase: tc.phase, Reserved: tc.reserved}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if allTerminated(m.Store.View()) == tc.pending {
					t.Fatal("incorrect manager completion boundary")
				}
				if online {
					server, err := m.ServeControl()
					if err != nil {
						t.Fatal(err)
					}
					defer server.Close()
				} else if err := m.Store.Close(); err != nil {
					t.Fatal(err)
				}
				for _, pool := range []string{"", "linux"} {
					ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
					err := waitStopped(ctx, c, pool)
					cancel()
					if pool == "" && tc.pending {
						requireCode(t, err, ErrControl)
					} else if err != nil {
						t.Fatal(err)
					}
				}
				if !tc.pending {
					return
				}
				store := m.Store
				if !online {
					var err error
					store, err = OpenStore(c)
					if err != nil {
						t.Fatal(err)
					}
				}
				if err := store.Update(func(s *Snapshot) error { delete(s.Artifacts, "artifact"); return nil }); err != nil {
					t.Fatal(err)
				}
				if !online {
					if err := store.Close(); err != nil {
						t.Fatal(err)
					}
				}
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if err := waitStopped(ctx, c, ""); err != nil {
					t.Fatal("completed cleanup still blocked", err)
				}
			})
		}
	}
}

type artifactServiceFixture struct {
	pid int
	serviceFixture
	shutdown chan bool
	store    *Store
}

func (f *artifactServiceFixture) Run(ctx context.Context, name string, args, env []string, in io.Reader) ([]byte, error) {
	if f.pid > 0 {
		if name == "launchctl" && len(args) == 1 && args[0] == "list" {
			return []byte("PID\tStatus\tLabel\n" + strconv.Itoa(f.pid) + "\t0\t" + serviceLabel + "\n"), nil
		}
		if name == "systemctl" && len(args) > 1 && args[1] == "show" {
			return []byte(strconv.Itoa(f.pid)), nil
		}
	}
	for _, arg := range args {
		if arg == "bootout" || arg == "disable" {
			f.shutdown <- allTerminated(f.store.View())
		}
	}
	return f.serviceFixture.Run(ctx, name, args, env, in)
}

func TestServiceShutdownWaitsForManagedArtifacts(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("supported user services")
	}
	for _, action := range []string{"stop", "uninstall"} {
		for _, phase := range []ArtifactPhase{ArtifactPreparing, ArtifactRemoving, ArtifactReady} {
			t.Run(action+"/"+string(phase), func(t *testing.T) {
				m, c, _, _, _ := testManager(t)
				home := filepath.Dir(c.Storage.State)
				t.Setenv("HOME", home)
				t.Setenv("XDG_CONFIG_HOME", home)
				configPath := filepath.Join(home, "config.toml")
				fixture := &artifactServiceFixture{shutdown: make(chan bool, 1), store: m.Store}
				if err := Service(context.Background(), "install", configPath, c, fixture); err != nil {
					t.Fatal(err)
				}
				if err := m.Store.Update(func(s *Snapshot) error {
					s.Artifacts["artifact"] = &RunnerArtifact{ID: "artifact", Backend: Docker, Phase: phase, Reserved: phase == ArtifactPreparing}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				server, err := m.ServeControl()
				if err != nil {
					t.Fatal(err)
				}
				defer server.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				done := make(chan error, 1)
				finished := make(chan struct{})
				binary, err := os.Executable()
				if err != nil {
					t.Fatal(err)
				}
				fixture.pid = os.Getpid()
				admission := matchingServiceManagerAdapter(t, binary, configPath)
				go func() {
					defer close(finished)
					done <- serviceWithManagerAdapter(ctx, action, configPath, c, fixture, admission)
				}()
				defer func() { cancel(); <-finished }()
				// Observe the durable Stop request before checking the pending native action.
				for !m.Store.View().Stopping {
					select {
					case err := <-done:
						t.Fatalf("service returned before Stop: %v", err)
					case <-ctx.Done():
						t.Fatal("Stop request not observed")
					case <-time.After(time.Millisecond):
					}
				}
				if phase != ArtifactReady {
					select {
					case safe := <-fixture.shutdown:
						t.Fatalf("native shutdown preceded artifact cleanup: safe=%t", safe)
					case err := <-done:
						t.Fatalf("service completed while artifact pending: %v", err)
					case <-time.After(100 * time.Millisecond):
					}
				}
				if phase != ArtifactReady {
					if _, err := os.Stat(servicePath()); err != nil {
						t.Fatal("pending cleanup removed service definition", err)
					}
				}
				if err := m.Store.Update(func(s *Snapshot) error {
					if phase == ArtifactPreparing {
						s.Artifacts["artifact"].Phase = ArtifactReady
						s.Artifacts["artifact"].Reserved = false
					} else if phase == ArtifactRemoving {
						delete(s.Artifacts, "artifact")
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				select {
				case err := <-done:
					if err != nil {
						t.Fatal(err)
					}
				case <-ctx.Done():
					t.Fatal("service did not finish after cleanup")
				}
				select {
				case safe := <-fixture.shutdown:
					if !safe {
						t.Fatal("native shutdown saw unfinished cleanup")
					}
				default:
					t.Fatal("native shutdown not invoked")
				}
				_, err = os.Stat(servicePath())
				if action == "uninstall" && !os.IsNotExist(err) {
					t.Fatal("completed uninstall retained definition", err)
				}
				if action == "stop" && err != nil {
					t.Fatal("stop removed definition", err)
				}
			})
		}
	}
}
