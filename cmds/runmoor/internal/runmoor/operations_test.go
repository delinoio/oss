package runmoor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
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

func TestWaitStoppedScopesLiveAndOfflineStateToPool(t *testing.T) {
	for _, online := range []bool{false, true} {
		for _, tc := range []struct {
			name, pool                    string
			localCleaned, oldActive, done bool
		}{
			{"target drained", "linux", true, false, true},
			{"unrelated active", "other", true, false, false},
			{"global drain", "", true, false, false},
			{"local cleanup pending", "linux", false, false, false},
			{"old generation active", "linux", true, true, false},
		} {
			t.Run(fmt.Sprintf("online=%t/%s", online, tc.name), func(t *testing.T) {
				m, c, _, _, pool := testManager(t)
				id := seedRunner(t, m, pool, Cleaning)
				if err := m.Store.Update(func(s *Snapshot) error {
					s.Runners[id].Terminated = true
					s.Runners[id].LocalCleaned = tc.localCleaned
					s.Pools["other"] = &PoolState{ID: "other", Spec: Pool{Name: "other"}, Phase: Ready}
					s.Runners["other-job"] = &Runner{ID: "other-job", PoolID: "other", Phase: Busy}
					if tc.oldActive {
						s.Pools["old"] = &PoolState{ID: "old", Spec: Pool{Name: "linux"}, Phase: Draining}
						s.Runners["old-job"] = &Runner{ID: "old-job", PoolID: "old", Phase: Busy}
					}
					return nil
				}); err != nil {
					t.Fatal(err)
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
				ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
				defer cancel()
				err := waitStopped(ctx, c, tc.pool)
				if tc.done && err != nil {
					t.Fatal("drained pool waited for unrelated work", err)
				}
				if !tc.done {
					requireCode(t, err, ErrControl)
				}
			})
		}
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
func TestPendingImageRemovalKeepsSleepInhibitionUntilDurableCompletion(t *testing.T) {
	_, s := fixtureStore(t)
	m := NewManager(s, "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer m.cancel()
	power := &failingPower{}
	m.Power = power
	if err := s.Update(func(s *Snapshot) error {
		s.Images["removing"] = &Image{ID: "removing", Phase: ImageRemoving}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.step(); err != nil {
		t.Fatal(err)
	}
	if !power.active || s.View().PowerProblem == nil {
		t.Fatal("pending image mutation lost its sleep inhibitor")
	}
	if err := s.Update(func(s *Snapshot) error { delete(s.Images, "removing"); return nil }); err != nil {
		t.Fatal(err)
	}
	if err := m.step(); err != nil {
		t.Fatal(err)
	}
	if power.active || s.View().PowerProblem != nil {
		t.Fatal("completed removal retained its inhibitor or warning")
	}
}
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

func TestQuarantinedExecutionKeepsSleepInhibitionUntilConfirmedTermination(t *testing.T) {
	_, s := fixtureStore(t)
	m := NewManager(s, "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer m.cancel()
	power := &failingPower{}
	m.Power = power
	if err := s.Update(func(s *Snapshot) error {
		s.Runners["uncertain"] = &Runner{ID: "uncertain", Phase: Quarantined, Resources: Resources{1, 128}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.step(); err != nil {
		t.Fatal(err)
	}
	if !power.active || s.View().Runners["uncertain"].Forced {
		t.Fatal("quarantine lost sleep protection or forced unverified termination")
	}
	if _, count, _ := usage(s.View()); count != 1 {
		t.Fatal("uncertain execution lost its reservation")
	}
	if err := s.Update(func(s *Snapshot) error { s.Runners["uncertain"].Terminated = true; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := m.step(); err != nil {
		t.Fatal(err)
	}
	if power.active {
		t.Fatal("confirmed termination retained sleep inhibition")
	}
}

func TestImageMutationsRequireManagerBeforeOfflineSideEffects(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix control socket contract")
	}
	for _, action := range []string{"create", "open", "seal", "remove"} {
		t.Run(action, func(t *testing.T) {
			c := fixtureConfig(t)
			c.Pools, c.Connections = nil, nil
			path := filepath.Join(filepath.Dir(c.Storage.State), "config.toml")
			data, err := toml.Marshal(c)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if Execute([]string{"--config", path, "image", action}, &out, &out) != 1 || !strings.Contains(out.String(), string(ErrControl)) {
				t.Fatal("image mutation did not require its lifetime owner", out.String())
			}
			if _, err = os.Stat(c.Storage.State); !os.IsNotExist(err) {
				t.Fatal("offline image mutation created state")
			}
		})
	}
}

func TestImageOnlyManagerKeepsOpenSetupInhibitedAfterCLIReturns(t *testing.T) {
	c, s := fixtureStore(t)
	c.Pools, c.Connections = nil, nil
	c, err := NormalizeConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(c.Storage.State, "config.toml")
	data, err := toml.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	m := NewManager(s, path, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { m.cancel(); m.wg.Wait() })
	driver, _ := fakeTart(c)
	m.Images.Tart = driver
	power := &failingPower{}
	m.Power = power
	if err = m.activate(c); err != nil {
		t.Fatal(err)
	}
	server, err := m.ServeControl()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	cli := func(args ...string) {
		t.Helper()
		var out bytes.Buffer
		if Execute(append([]string{"--config", path, "image"}, args...), &out, &out) != 0 {
			t.Fatal(out.String())
		}
	}
	cli("create", "--name", "setup", "--ipsw", "/operator/local.ipsw", "--cpu", "1", "--memory-mib", "512")
	var id string
	for key := range s.View().Images {
		id = key
	}
	cli("open", "--id", id)
	if err = m.step(); err != nil {
		t.Fatal(err)
	}
	if !power.active || s.View().Images[id].Phase != ImageOpen {
		t.Fatal("setup lifetime lost its sleep inhibitor when the CLI returned")
	}
	if err = m.Stop(true); err != nil {
		t.Fatal(err)
	}
	if err = m.step(); err != nil {
		t.Fatal(err)
	}
	if m.readyToStop() || !power.active {
		t.Fatal("stop released lifetime ownership of open setup")
	}
	for _, action := range []string{"create", "open"} {
		_, err = SendControl(context.Background(), c, ControlRequest{Action: "image", Image: &ImageRequest{Action: action, ID: id}})
		requireCode(t, err, ErrControl)
	}
	cli("seal", "--id", id, "--runner-version", "2.337.0")
	if err = m.step(); err != nil {
		t.Fatal(err)
	}
	if power.active {
		t.Fatal("sealed, stopped image retained its sleep inhibitor")
	}
	if !m.readyToStop() {
		t.Fatal("finished setup blocked shutdown")
	}
	m.imageMu.Lock()
	ready := m.readyToStop()
	m.imageMu.Unlock()
	if ready {
		t.Fatal("shutdown raced an in-flight image operation")
	}
}

func TestShutdownWaitsForImagesThroughLiveAndOfflineState(t *testing.T) {
	for _, online := range []bool{false, true} {
		for _, phase := range []ImagePhase{ImagePreparing, ImageOpen, ImageSealed, ImageRemoving} {
			t.Run(fmt.Sprintf("online=%t/%s", online, phase), func(t *testing.T) {
				m, c, _, _, _ := testManager(t)
				if err := m.Store.Update(func(s *Snapshot) error {
					s.Stopping = true
					s.Images["setup"] = &Image{ID: "setup", Phase: phase}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				pending := phase == ImageOpen || phase == ImageRemoving
				if m.readyToStop() == pending {
					t.Fatal("incorrect image shutdown boundary")
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
					if pending && pool == "" {
						requireCode(t, err, ErrControl)
					} else if err != nil {
						t.Fatal(err)
					}
				}
			})
		}
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
	resolved, err := filepath.EvalSymlinks("/tmp")
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

type cancelledPreparationDriver struct {
	Driver
	started  chan string
	succeeds bool
}

func (d *cancelledPreparationDriver) Prepare(ctx context.Context, _ Config, _ Pool, r Runner, _ Snapshot, _ string, _ func(Handle) error) error {
	d.started <- r.ID
	<-ctx.Done()
	if d.succeeds {
		return nil
	}
	return ctx.Err()
}

func TestForceCancelledPreparationsDoNotChangeFailureCircuit(t *testing.T) {
	for _, succeeds := range []bool{false, true} {
		for _, failures := range []int{0, 2} {
			t.Run(fmt.Sprintf("success=%t/prior=%d", succeeds, failures), func(t *testing.T) {
				m, c, _, base, pool := testManager(t)
				driver := &cancelledPreparationDriver{Driver: base, started: make(chan string, 3), succeeds: succeeds}
				m.Drivers = func(Backend) (Driver, error) { return driver, nil }
				if err := m.Store.Update(func(s *Snapshot) error { s.Pools[pool].PreparationFailures = failures; return nil }); err != nil {
					t.Fatal(err)
				}
				for i := 0; i < 3; i++ {
					id := seedRunner(t, m, pool, Preparing)
					m.startWork(id, func(ctx context.Context) { m.prepare(ctx, id) })
				}
				for i := 0; i < 3; i++ {
					select {
					case <-driver.started:
					case <-time.After(5 * time.Second):
						t.Fatal("preparation did not start")
					}
				}
				if err := m.Stop(true); err != nil {
					t.Fatal(err)
				}
				m.wg.Wait()
				if err := m.activate(c); err != nil {
					t.Fatal(err)
				}
				s := m.Store.View()
				if s.Pools[pool].Phase != Ready || s.Pools[pool].PreparationFailures != failures || s.Pools[pool].Problem != nil {
					t.Fatal("force cancellation changed the failure circuit across restart")
				}
				for _, r := range s.Runners {
					if r.Phase != Cleaning || r.Problem != nil {
						t.Fatal("cancelled preparation was reported as image failure or readiness")
					}
				}
			})
		}
	}
}

type preparationCountingRemote struct {
	*fakeRemote
	jitCalls int
}

func (r *preparationCountingRemote) JIT(ctx context.Context, p PoolState, v Runner) (int, string, error) {
	r.mu.Lock()
	r.jitCalls++
	r.mu.Unlock()
	return r.fakeRemote.JIT(ctx, p, v)
}

func TestPreparationRechecksStateAfterSchedulingBeforeSideEffects(t *testing.T) {
	for _, boundary := range []string{"before worker registration", "after worker registration", "already cleaning"} {
		t.Run(boundary, func(t *testing.T) {
			m, _, baseRemote, driver, pool := testManager(t)
			remote := &preparationCountingRemote{fakeRemote: baseRemote}
			m.RemoteFactory = func(Connection) (Remote, error) { return remote, nil }
			id := seedRunner(t, m, pool, Preparing)
			release := make(chan struct{})
			start := func() { m.startWork(id, func(ctx context.Context) { <-release; m.prepare(ctx, id) }) }
			if boundary == "after worker registration" {
				start()
			}
			if boundary == "already cleaning" {
				if err := m.Store.Update(func(s *Snapshot) error { s.Runners[id].Phase = Cleaning; return nil }); err != nil {
					t.Fatal(err)
				}
			} else if err := m.Stop(true); err != nil {
				t.Fatal(err)
			}
			if boundary != "after worker registration" {
				start()
			}
			close(release)
			m.wg.Wait()
			remote.mu.Lock()
			calls := remote.jitCalls
			remote.mu.Unlock()
			driver.mu.Lock()
			live := len(driver.live)
			driver.mu.Unlock()
			s := m.Store.View()
			if calls != 0 || live != 0 || s.Runners[id].Phase != Cleaning || s.Pools[pool].PreparationFailures != 0 {
				t.Fatal("stale scheduled preparation reached external side effects")
			}
		})
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

func TestDetachedCommandSurvivesParentExitAndOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix detached host processes")
	}
	if os.Getenv("RUNMOOR_DETACH_FIXTURE") == "1" {
		_, err := (OSCommand{}).Start("/bin/sh", []string{"-c", `sleep 0.2; printf 'probe'; printf 'complete' > "$1"`, "runmoor-fixture", os.Getenv("RUNMOOR_DETACH_RESULT")}, minimalEnv())
		if err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	result := filepath.Join(t.TempDir(), "completed")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	parent := exec.Command(executable, "-test.run=^TestDetachedCommandSurvivesParentExitAndOutput$")
	parent.Env = append(os.Environ(), "RUNMOOR_DETACH_FIXTURE=1", "RUNMOOR_DETACH_RESULT="+result)
	if err := parent.Run(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(result); err == nil && string(data) == "complete" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("detached child lost its output descriptors when the parent exited")
}
