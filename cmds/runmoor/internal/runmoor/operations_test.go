package runmoor

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"
)

func TestServiceDefinitionsAndCLIRejectCrossCommandFlags(t *testing.T) {
	for _, platform := range []string{"darwin", "linux"} {
		definition, err := serviceDefinition(platform, "/user/a & b/runmoor", "/user/config%$name.toml")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(definition, "RUNMOOR_PAT") || strings.Contains(definition, "Environment=") {
			t.Fatal("service copied credential environment")
		}
		if platform == "darwin" && (!strings.Contains(definition, "SuccessfulExit") || !strings.Contains(definition, "AbandonProcessGroup") || !strings.Contains(definition, "a &amp; b")) {
			t.Fatal("launchd lifecycle/escaping contract")
		}
		if platform == "linux" && (!strings.Contains(definition, "KillMode=process") || !strings.Contains(definition, "TimeoutStopSec=infinity") || !strings.Contains(definition, "config%%$$name")) {
			t.Fatal("systemd lifecycle/escaping contract")
		}
	}
	if _, err := serviceDefinition("linux", "/bin/runmoor\nInjected=value", "/config"); err == nil {
		t.Fatal("control characters allowed")
	}
	for _, args := range [][]string{{"version", "--from", "unexpected"}, {"run", "--force"}, {"pause", "--runner-version", "2.337.0"}} {
		var output bytes.Buffer
		if Execute(args, &output, &output) == 0 {
			t.Fatal("ignored inappropriate CLI option", args)
		}
	}
}
func TestStopPoolAndResumeDoNotStopUnrelatedPools(t *testing.T) {
	m, c, _, _, pool := testManager(t)
	other := "other-pool"
	if err := m.Store.Update(func(s *Snapshot) error {
		v := *s.Pools[pool]
		v.ID = other
		v.Spec.Name = "other"
		s.Pools[other] = &v
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	first := seedRunner(t, m, pool, Busy)
	second := seedRunner(t, m, other, Busy)
	if err := m.StopPool(c.Pools[0].Name, true); err != nil {
		t.Fatal(err)
	}
	s := m.Store.View()
	if s.Stopping || !s.Runners[first].Forced || s.Runners[second].Forced || s.Pools[other].Phase != Ready {
		t.Fatal("pool stop escaped its target")
	}
	if err := m.Pause(""); err != nil {
		t.Fatal(err)
	}
	if err := m.Resume(context.Background(), c.Pools[0].Name); err != nil {
		t.Fatal(err)
	}
	s = m.Store.View()
	if s.Paused || s.Pools[pool].Phase != Ready || s.Pools[other].Phase != Paused {
		t.Fatal("selective resume enabled unrelated pools")
	}
}
func TestReloadRejectsWholeCandidateAtomically(t *testing.T) {
	m, c, remote, _, _ := testManager(t)
	before := m.Store.View().Generation
	path := filepath.Join(c.Storage.State, "candidate.toml")
	m.ConfigPath = path
	c.Pools[0].MaxRunners++
	data, err := toml.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	remote.failure = problem(ErrAuth, "Revoked.", "Replace credential.")
	requireCode(t, m.Reload(context.Background()), ErrAuth)
	if m.Store.View().Generation != before {
		t.Fatal("rejected candidate partially published")
	}
	remote.failure = nil
	if err = m.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.Store.View().Generation == before {
		t.Fatal("valid candidate was not committed")
	}
}
func TestDrainedBackupRelocationAndActiveRejection(t *testing.T) {
	c, s := fixtureStore(t)
	installation := s.View().Installation
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	moved := fixtureConfig(t)
	if err := privateDir(moved.Storage.State); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(c.Storage.State, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(moved.Storage.State, "state.sqlite"), data, 0600); err != nil {
		t.Fatal(err)
	}
	restored, err := OpenStore(moved)
	if err != nil {
		t.Fatal(err)
	}
	if restored.View().Installation != installation || restored.View().Config.Storage != moved.Storage {
		t.Fatal("backup identity changed")
	}
	if err = restored.Update(func(v *Snapshot) error { v.Runners["active"] = &Runner{Phase: Busy}; return nil }); err != nil {
		t.Fatal(err)
	}
	restored.Close()
	moved.Storage.Data += "-different"
	_, err = OpenStore(moved)
	requireCode(t, err, ErrConfig)
}
func TestPersistedJobTimeoutAndIdleLifetime(t *testing.T) {
	m, _, _, driver, pool := testManager(t)
	busy := seedRunner(t, m, pool, Busy)
	idle := seedRunner(t, m, pool, Idle)
	driver.live[busy] = true
	driver.live[idle] = true
	m.Store.Update(func(s *Snapshot) error {
		s.Runners[busy].Deadline = time.Now().Add(-time.Minute)
		s.Runners[idle].Deadline = time.Now().Add(-time.Minute)
		return nil
	})
	m.inspect(context.Background(), busy)
	m.inspect(context.Background(), idle)
	v := m.Store.View()
	if !v.Runners[busy].Forced || v.Runners[busy].Phase != Cleaning || v.Runners[idle].Phase != Idle {
		t.Fatal("persisted deadlines were reset or warm capacity timed out")
	}
}

type failingPower struct{ active bool }

func (p *failingPower) Set(active bool) *Problem {
	p.active = active
	if active {
		return problem(ErrPower, "Unavailable.", "Check OS permissions.")
	}
	return nil
}
func (p *failingPower) Release() {}
func TestPowerFailureRemainsWarningAndJobsKeepRunning(t *testing.T) {
	m, _, _, driver, pool := testManager(t)
	id := seedRunner(t, m, pool, Busy)
	driver.live[id] = true
	p := &failingPower{}
	m.Power = p
	if err := m.step(); err != nil {
		t.Fatal(err)
	}
	if !p.active || m.Store.View().PowerProblem == nil || m.Store.View().Runners[id].Forced {
		t.Fatal("power failure interrupted work or disappeared")
	}
}

type failCreateCommand struct{ CommandExecutor }

func (f failCreateCommand) Run(ctx context.Context, name string, args, env []string, in io.Reader) ([]byte, error) {
	if len(args) > 0 && args[0] == "create" {
		return nil, errors.New("private upstream error")
	}
	return f.CommandExecutor.Run(ctx, name, args, env, in)
}
func TestFailedImageCreationReleasesConfirmedUnusedCapacity(t *testing.T) {
	c, s := fixtureStore(t)
	driver, _ := fakeTart(c)
	driver.Exec = failCreateCommand{driver.Exec}
	images := &ImageManager{Store: s, Tart: driver}
	_, err := images.Operate(context.Background(), c, ImageRequest{Action: "create", Name: "failed", IPSW: "/operator/missing.ipsw", Resources: Resources{1, 512}})
	if err == nil || strings.Contains(err.Error(), "private upstream") {
		t.Fatal("create failure missing or unsafe")
	}
	snap := s.View()
	_, count, _ := usage(snap)
	if count != 0 {
		t.Fatal("failed reaped create leaked reservation")
	}
	for id := range snap.Images {
		if _, err = images.Operate(context.Background(), c, ImageRequest{Action: "remove", ID: id}); err != nil {
			t.Fatal(err)
		}
	}
	if len(s.View().Images) != 0 {
		t.Fatal("failed image record cannot be removed")
	}
}
func TestGuestStateUsesCanonicalPrivateTemporaryDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix guest helper")
	}
	parent := filepath.Dir(filepath.Dir(filepath.Dir(guestStatePath(newID()))))
	resolved, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil || parent != resolved {
		t.Fatal("guest private state crosses an OS symlink")
	}
}

type serviceFixture struct{ commands [][]string }

func (f *serviceFixture) Run(_ context.Context, name string, args, env []string, _ io.Reader) ([]byte, error) {
	f.commands = append(f.commands, append([]string{name}, args...))
	for _, value := range env {
		if strings.Contains(value, "fixture-service-secret") {
			return nil, errors.New("credential copied into service command")
		}
	}
	if name == "launchctl" && len(args) > 0 && args[0] == "print" {
		return nil, errors.New("not loaded")
	}
	return nil, nil
}
func (f *serviceFixture) Start(string, []string, []string) (int, error) {
	return 0, errors.New("unexpected service spawn")
}
func TestUserServiceLifecycleUsesIsolatedUserDirectory(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("supported user services")
	}
	c, s := fixtureStore(t)
	s.Close()
	home := filepath.Dir(c.Storage.State)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("RUNMOOR_PAT", "fixture-service-secret")
	fixture := &serviceFixture{}
	for _, action := range []string{"install", "start", "stop", "uninstall"} {
		if err := Service(context.Background(), action, filepath.Join(home, "config.toml"), c, fixture); err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		if action == "install" {
			if err := Service(context.Background(), action, filepath.Join(home, "config.toml"), c, fixture); err == nil {
				t.Fatal("service overwrite allowed")
			}
		}
	}
	if _, err := os.Stat(servicePath()); !os.IsNotExist(err) {
		t.Fatal("service definition remains after uninstall")
	}
	if len(fixture.commands) < 3 {
		t.Fatal("service manager lifecycle not exercised")
	}
}
func TestCapacityWaitIsVisibleWithoutPreemptingWork(t *testing.T) {
	m, _, _, driver, pool := testManager(t)
	id := seedRunner(t, m, pool, Busy)
	driver.live[id] = true
	m.poolLoops[pool] = func() {}
	m.Store.Update(func(s *Snapshot) error { s.Config.Host.MaxRunners = 1; s.Pools[pool].Demand = 2; return nil })
	if err := m.step(); err != nil {
		t.Fatal(err)
	}
	snap := m.Store.View()
	if snap.Pools[pool].Problem == nil || snap.Pools[pool].Problem.Code != ErrCapacity || snap.Runners[id].Forced {
		t.Fatal("capacity shortage is hidden or preempts work")
	}
}

func TestForceStopCancelsInFlightPreparationImmediately(t *testing.T) {
	m, _, _, _, pool := testManager(t)
	id := seedRunner(t, m, pool, Preparing)
	started := make(chan struct{})
	cancelled := make(chan struct{})
	m.startWork(id, func(ctx context.Context) { close(started); <-ctx.Done(); close(cancelled) })
	<-started
	if err := m.Stop(true); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("force stop left preparation waiting for its original timeout")
	}
}

func TestConfigurationAcceptancePreservesConcurrentStop(t *testing.T) {
	m, c, _, _, _ := testManager(t)
	if err := m.Stop(false); err != nil {
		t.Fatal(err)
	}
	c.Logging.Level = "debug"
	if err := m.accept(c, false); err != nil {
		t.Fatal(err)
	}
	if !m.Store.View().Stopping {
		t.Fatal("reload cleared a committed stop request")
	}
}

func TestPrivateControlSocketAndVersionedUncoloredStatus(t *testing.T) {
	m, c, _, _, pool := testManager(t)
	t.Setenv("RUNMOOR_TEST_CREDENTIAL", "host-only-test-credential")
	seedRunner(t, m, pool, Busy)
	server, err := m.ServeControl()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	info, err := os.Stat(filepath.Join(c.Storage.State, "control.sock"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("control socket is not private")
	}
	response, err := SendControl(context.Background(), c, ControlRequest{Action: "status"})
	if err != nil {
		t.Fatal(err)
	}
	if response.SchemaVersion != 1 || response.Status.SchemaVersion != 1 || !response.Status.Running {
		t.Fatal("invalid status envelope")
	}
	var output bytes.Buffer
	printStatus(&output, response.Status, true)
	if strings.Contains(output.String(), "\x1b") || strings.Contains(output.String(), "host-only-test-credential") {
		t.Fatal("status leaks color or credentials")
	}
	invalid := m.Control(context.Background(), ControlRequest{Action: "pause", Force: true})
	requireCode(t, invalid.Problem, ErrConfig)
	if m.Store.View().Paused {
		t.Fatal("invalid control mutated state")
	}
}
