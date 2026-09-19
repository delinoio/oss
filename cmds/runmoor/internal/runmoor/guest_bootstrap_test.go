package runmoor

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// Only virtualization and GitHub are simulated: bootstrap and its detached
// supervisor execute through the compiled production CLI with real run.sh files.
type localGuestCommand struct {
	CommandExecutor
	executable, temp string
}

func (f localGuestCommand) Run(ctx context.Context, name string, args, env []string, in io.Reader) ([]byte, error) {
	for i, arg := range args {
		if arg == "__guest-bootstrap" || arg == "__guest-status" {
			cmd := exec.CommandContext(ctx, f.executable, args[i:]...)
			cmd.Env = append(minimalEnv(), "TMPDIR="+f.temp)
			cmd.Stdin = in
			return cmd.Output()
		}
	}
	return f.CommandExecutor.Run(ctx, name, args, env, in)
}

func TestGuestBootstrapConfirmsRunnerBeforeResettingPreparationFailures(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix guest supervisor")
	}
	helper := filepath.Join(t.TempDir(), "runmoor")
	build := exec.Command("go", "build", "-o", helper, "../..")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build helper: %v: %s", err, output)
	}
	setup := func(t *testing.T) (*Manager, Config, *TartDriver, string, string) {
		t.Helper()
		m, c, _, _, pool := testManager(t)
		driver, _ := fakeTart(c)
		driver.GuestExecutable = helper
		driver.Exec = localGuestCommand{driver.Exec, helper, t.TempDir()}
		m.Drivers = func(Backend) (Driver, error) { return driver, nil }
		images := &ImageManager{Store: m.Store, Tart: driver}
		im, err := images.Operate(context.Background(), c, ImageRequest{Action: "create", Name: "guest", IPSW: "/fixture.ipsw", Resources: Resources{1, 512}})
		if err != nil {
			t.Fatal(err)
		}
		runnerPath := t.TempDir()
		im, err = images.Operate(context.Background(), c, ImageRequest{Action: "seal", ID: im.ID, RunnerPath: runnerPath, RunnerVersion: "2.337.0"})
		if err != nil {
			t.Fatal(err)
		}
		if err = m.Store.Update(func(s *Snapshot) error {
			p := &s.Pools[pool].Spec
			p.Backend, p.Image, p.RunnerPath = Tart, im.ID, runnerPath
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return m, c, driver, pool, runnerPath
	}
	seed := func(t *testing.T, m *Manager, pool string) string {
		t.Helper()
		id := seedRunner(t, m, pool, Preparing)
		if err := m.Store.Update(func(s *Snapshot) error { s.Runners[id].Backend = Tart; return nil }); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(guestStatePath(id))) })
		return id
	}
	readStatus := func(t *testing.T, id string) GuestStatus {
		t.Helper()
		b, err := readPrivate(guestStatePath(id), 4096)
		var status GuestStatus
		if err != nil || json.Unmarshal(b, &status) != nil {
			t.Fatalf("missing guest status: %v", err)
		}
		return status
	}
	t.Run("launch failures suspend the pool", func(t *testing.T) {
		m, _, _, pool, path := setup(t)
		for i, scenario := range []string{"missing", "not executable", "immediate failure"} {
			if scenario != "missing" {
				if err := os.WriteFile(filepath.Join(path, "run.sh"), []byte("#!/bin/sh\necho private-runner-output >&2\nexit 23\n"), 0600); err != nil {
					t.Fatal(err)
				}
				if scenario == "immediate failure" {
					if err := os.Chmod(filepath.Join(path, "run.sh"), 0700); err != nil {
						t.Fatal(err)
					}
				}
			}
			id := seed(t, m, pool)
			m.prepare(context.Background(), id)
			s := m.Store.View()
			if s.Runners[id].Phase != Cleaning || s.Pools[pool].PreparationFailures != i+1 {
				t.Fatalf("%s was acknowledged as successful preparation", scenario)
			}
			requireCode(t, s.Runners[id].Problem, ErrPreparation)
			status := readStatus(t, id)
			if status.Ready || !status.Finished || status.ExitCode == 0 {
				t.Fatal("failed startup published readiness")
			}
			m.cleanup(context.Background(), id)
		}
		if m.Store.View().Pools[pool].Phase != Suspended {
			t.Fatal("repeated startup failures bypassed the circuit breaker")
		}
	})
	t.Run("live runner survives bootstrap and remains inspectable", func(t *testing.T) {
		m, c, driver, pool, path := setup(t)
		// The bounded loop also ensures a failing test cannot leave a live child.
		script := "#!/bin/sh\ni=0\nwhile [ ! -f stop ] && [ $i -lt 200 ]; do sleep 0.05; i=$((i+1)); done\n"
		if err := os.WriteFile(filepath.Join(path, "run.sh"), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
		defer os.WriteFile(filepath.Join(path, "stop"), nil, 0600)
		id := seed(t, m, pool)
		if err := m.Store.Update(func(s *Snapshot) error { s.Pools[pool].PreparationFailures = 2; return nil }); err != nil {
			t.Fatal(err)
		}
		m.prepare(context.Background(), id)
		s := m.Store.View()
		status := readStatus(t, id)
		if !status.Ready || status.Finished || s.Runners[id].Phase != Idle || s.Pools[pool].PreparationFailures != 0 {
			t.Fatal("live runner did not complete the startup handshake")
		}
		obs, err := driver.Inspect(context.Background(), c, *s.Runners[id], s)
		if err != nil || !obs.Running {
			t.Fatal("runner did not survive bootstrap exit", err)
		}
		if err = os.WriteFile(filepath.Join(path, "stop"), nil, 0600); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for !readStatus(t, id).Finished && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
		}
		obs, err = driver.Inspect(context.Background(), c, *s.Runners[id], s)
		if err != nil || obs.Running || obs.ExitCode == nil || *obs.ExitCode != 0 {
			t.Fatal("finished runner was not recovered accurately", err)
		}
	})
}
