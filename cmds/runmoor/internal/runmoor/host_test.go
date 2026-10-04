package runmoor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/actions/scaleset"
)

type fixtureHost struct {
	mu            sync.Mutex
	alive         map[int]HostProcess
	members       map[int][]HostProcess
	roots         map[int]*os.Root
	statuses      map[int]HostExecutionStatus
	launched      int
	stopped       int
	phase         HostExecutionPhase
	beforePublish func()
	launchErr     error
	writeStatus   func(*os.Root, HostExecutionStatus) error
}

func newFixtureHost() *fixtureHost {
	return &fixtureHost{alive: map[int]HostProcess{}, members: map[int][]HostProcess{}, roots: map[int]*os.Root{}, statuses: map[int]HostExecutionStatus{}, phase: HostRunning}
}
func (*fixtureHost) Platform(context.Context) error                  { return nil }
func (*fixtureHost) Version(context.Context, *os.Root, string) error { return nil }
func (f *fixtureHost) Launch(ctx context.Context, root *os.Root, in HostBootstrap, publish func(HostProcess) error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if f.beforePublish != nil {
		f.beforePublish()
	}
	f.mu.Lock()
	f.launched++
	process := HostProcess{PID: 1000 + f.launched, Start: "fixture-supervisor-" + in.Directory.ID, Group: 1000 + f.launched}
	f.mu.Unlock()
	if err := publish(process); err != nil {
		return err
	}
	if f.launchErr != nil {
		return f.launchErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	worker := HostProcess{PID: 2000 + f.launched, Start: "fixture-worker-" + in.Directory.ID, Group: 2000 + f.launched}
	status := HostExecutionStatus{ID: in.Directory.ID, Token: in.Directory.Token, Supervisor: process, Worker: worker, Phase: f.phase}
	clone, err := root.OpenRoot(".")
	if err != nil {
		return err
	}
	f.roots[process.PID] = clone
	f.statuses[process.PID] = status
	if f.phase == HostRunning || f.phase == HostStarting {
		f.alive[process.PID] = process
		f.alive[worker.PID] = worker
		f.members[worker.Group] = []HostProcess{worker}
	}
	if f.writeStatus != nil {
		return f.writeStatus(clone, status)
	}
	return hostRootWrite(root, "status.json", status)
}
func (f *fixtureHost) Alive(p HostProcess) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return sameHostProcess(f.alive[p.PID], p), nil
}
func (f *fixtureHost) Group(p HostProcess) ([]HostProcess, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if current, ok := f.alive[p.PID]; ok && !sameHostProcess(current, p) {
		return nil, hostOwnership()
	}
	return slices.Clone(f.members[p.Group]), nil
}
func (f *fixtureHost) Stop(p HostProcess) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !sameHostProcess(f.alive[p.PID], p) {
		return hostOwnership()
	}
	f.stopped++
	status := f.statuses[p.PID]
	delete(f.alive, p.PID)
	delete(f.alive, status.Worker.PID)
	delete(f.members, status.Worker.Group)
	status.Phase = HostFinished
	return hostRootWrite(f.roots[p.PID], "status.json", status)
}
func (f *fixtureHost) close() {
	for _, root := range f.roots {
		root.Close()
	}
}
func hostFixture(t *testing.T) (Config, *Store, *fixtureHost, Pool) {
	t.Helper()
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("Unix storage fixtures")
	}
	c, store := fixtureStore(t)
	p := c.Pools[0]
	p.Name = "macos-host"
	p.Backend = Host
	p.Image = ""
	p.RunnerPath = ""
	p.Arch = "arm64"
	p.RunnerVersion = LatestRunner
	c.Pools = []Pool{p}
	native := newFixtureHost()
	t.Cleanup(native.close)
	return c, store, native, p
}
func buildHostFixture(t *testing.T, c Config, store *Store, native *fixtureHost, p Pool) Pool {
	t.Helper()
	release, body := fixtureRelease(t, Tart, "arm64")
	a := RunnerArtifact{ID: newID(), Backend: Host, Pool: p.Name, Phase: ArtifactPreparing, Resources: p.Resources, Reserved: true}
	if err := store.Update(func(s *Snapshot) error { s.Artifacts[a.ID] = &a; return nil }); err != nil {
		t.Fatal(err)
	}
	builder := &ManagedImageBuilder{Store: store, Host: native, Client: archiveClient(body), Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	resolved, err := builder.Prepare(context.Background(), c, p, a, release)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}
func seedHostFixture(t *testing.T, c Config, store *Store, p Pool) Runner {
	t.Helper()
	r := Runner{ID: newID(), Backend: Host, Image: p.Image, Resources: p.Resources, Phase: Preparing, CreatedAt: nowUTC(), Deadline: time.Now().Add(time.Minute)}
	if err := store.Update(func(s *Snapshot) error { s.Runners[r.ID] = &r; return nil }); err != nil {
		t.Fatal(err)
	}
	return r
}
func prepareHostFixture(t *testing.T, c Config, store *Store, native *fixtureHost, p Pool) Runner {
	t.Helper()
	r := seedHostFixture(t, c, store, p)
	driver := HostDriver{Store: store, Native: native}
	if err := driver.Prepare(context.Background(), c, p, r, store.View(), "PRIVATE-JIT-FIXTURE", func(Handle) error { return nil }); err != nil {
		t.Fatal(err)
	}
	return r
}
func TestHostConfigurationClosedPlatformAndFields(t *testing.T) {
	c := fixtureConfig(t)
	p := &c.Pools[0]
	p.Backend = Host
	p.Arch = "arm64"
	p.Image = ""
	p.RunnerPath = ""
	normalized, err := normalizeConfig(c, "darwin", "arm64")
	if err != nil || normalized.Timeouts.HostPreparation != "5m" || normalized.Pools[0].RunnerPath != "" {
		t.Fatalf("host configuration: %+v %v", normalized, err)
	}
	for _, platform := range [][2]string{{"linux", "arm64"}, {"darwin", "amd64"}, {"windows", "arm64"}} {
		candidate := c
		candidate.Pools = slices.Clone(c.Pools)
		candidate.Pools[0].Arch = platform[1]
		_, err = normalizeConfig(candidate, platform[0], platform[1])
		requireCode(t, err, ErrPlatform)
	}
	for name, edit := range map[string]func(*Config){
		"image":             func(c *Config) { c.Pools[0].Image = newID() },
		"source":            func(c *Config) { c.Pools[0].ImageSource = &ImageSource{From: "base"} },
		"path":              func(c *Config) { c.Pools[0].RunnerPath = "/owned/runner" },
		"dind":              func(c *Config) { c.Pools[0].Mode = DinD },
		"daemon":            func(c *Config) { c.Pools[0].DaemonImage = "sha256:" + strings.Repeat("a", 64) },
		"timeout":           func(c *Config) { c.Timeouts.HostPreparation = "0s" },
		"explicit capacity": func(c *Config) { c.Pools[0].Resources.CPU = c.Host.CPU + 1 },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := c
			candidate.Pools = slices.Clone(c.Pools)
			edit(&candidate)
			_, err := normalizeConfig(candidate, "darwin", "arm64")
			requireCode(t, err, ErrConfig)
		})
	}
	name, labels, err := initialPoolLabels(Host, "arm64")
	if err != nil || name != "macos-host" || !slices.Equal(labels, []string{"runmoor-macos-host", "macOS", "ARM64"}) {
		t.Fatal(name, labels, err)
	}
	if !slices.Equal(normalized.Pools[0].Labels, c.Pools[0].Labels) {
		t.Fatal("authored labels changed")
	}
}
func TestHostInitExplicitAndNoImageSetup(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("host init platform")
	}
	c := fixtureConfig(t)
	path := filepath.Join(filepath.Dir(c.Storage.State), "host.toml")
	var out bytes.Buffer
	opts := InitOptions{Backend: "host", Target: "https://github.com/example/repo", CredentialEnv: "HOST_TEST_PAT", Storage: c.Storage}
	if err := initialize(path, opts, strings.NewReader(""), &out, false); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	p := loaded.Pools[0]
	if p.Name != "macos-host" || p.ScaleSet != "runmoor-macos-host" || p.Backend != Host || p.MinIdle != 0 || loaded.Host.MinFreeDiskMiB != 10240 || p.MaxRunners > loaded.Host.CPU/p.Resources.CPU {
		t.Fatalf("unexpected host init: %+v", loaded)
	}
	for _, edit := range []func(*InitOptions){func(o *InitOptions) { o.ImageOnly = true }, func(o *InitOptions) { o.Image = newID() }, func(o *InitOptions) { o.SourceHome = "/private/source" }} {
		invalid := opts
		edit(&invalid)
		requireCode(t, initialize(path+newID(), invalid, strings.NewReader(""), &out, false), ErrConfig)
	}
}
func TestHostAutomaticDefaultsWithoutVMConcurrencyCeiling(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("host configuration platform")
	}
	body := strings.ReplaceAll(minimalConfig, `backend = "docker"`, `backend = "host"`)
	c, err := decodeDefaults(t, body, Resources{12, 32768})
	if err != nil || c.Pools[0].Resources != (Resources{2, 4096}) || c.Pools[0].MaxRunners != 6 || c.Host.MaxRunners != 6 || c.Host.MinFreeDiskMiB != 10240 || c.Pools[0].MinIdle != 0 {
		t.Fatalf("host defaults: %+v %v", c, err)
	}
	c, err = decodeDefaults(t, body, Resources{1, 1024})
	if err != nil || c.Pools[0].Resources != (Resources{1, 1024}) || c.Pools[0].MaxRunners != 1 {
		t.Fatalf("small host defaults: %+v %v", c, err)
	}
	_, err = decodeDefaults(t, body, Resources{1, 512})
	requireCode(t, err, ErrCapacity)
	_, err = decodeDefaults(t, body+"resources = {cpu = 2, memory_mib = 4096}\n", Resources{1, 1024})
	if err == nil {
		t.Fatal("explicit host allocation was silently reduced")
	}
}
func TestHostSharedAdmissionAboveTwoAndUncertainReservation(t *testing.T) {
	c := fixtureConfig(t)
	c.Host = Budget{CPU: 16, MemoryMiB: 32768, MaxRunners: 8, MinFreeDiskMiB: 1}
	p := c.Pools[0]
	p.Backend = Host
	p.Resources = Resources{2, 4096}
	p.MaxRunners = 8
	state := Snapshot{Config: c, Pools: map[string]*PoolState{"host": {ID: "host", Spec: p, Phase: Ready, ScaleSetID: 1, Session: "s", Demand: 4}}, Runners: map[string]*Runner{}}
	selected := Schedule(state)
	if len(selected) != 4 {
		t.Fatalf("host inherited Tart ceiling: %v", selected)
	}
	for i := 0; i < 4; i++ {
		state.Runners[newID()] = &Runner{Backend: Host, Resources: p.Resources, Phase: Quarantined}
	}
	used, count, vms := usage(state)
	if used != (Resources{8, 16384}) || count != 4 || vms != 0 {
		t.Fatal(used, count, vms)
	}
	tart := p
	tart.Name = "tart"
	tart.Backend = Tart
	tart.Resources = Resources{4, 8192}
	state.Pools["tart"] = &PoolState{ID: "tart", Spec: tart, Phase: Ready, ScaleSetID: 2, Session: "s", Demand: 3}
	selected = Schedule(state)
	if len(selected) > 2 {
		t.Fatal("mixed allocations exceeded shared CPU budget", selected)
	}
	state.Config.Host.MinFreeDiskMiB = 1 << 40
	if err := diskCheck(state.Config); err == nil {
		t.Fatal("low disk admitted execution")
	}
}
func TestHostDistributionAndThreeDisposableExecutions(t *testing.T) {
	c, store, native, p := hostFixture(t)
	p = buildHostFixture(t, c, store, native, p)
	driver := HostDriver{Store: store, Native: native}
	ids := map[string]bool{}
	for i := 0; i < 3; i++ {
		r := prepareHostFixture(t, c, store, native, p)
		ids[r.ID] = true
		d := store.View().HostDirectories[r.ID]
		for _, part := range []string{"runner", "home", "tmp", "work"} {
			if _, err := os.Stat(filepath.Join(hostDirectoryPath(c, *d), part)); err != nil {
				t.Fatal(err)
			}
		}
		target, err := os.Readlink(filepath.Join(hostDirectoryPath(c, *d), "runner/_work"))
		if err != nil || target != "../work" {
			t.Fatal(target, err)
		}
		restarted := HostDriver{Store: store, Native: native}
		obs, err := restarted.Inspect(context.Background(), c, r, store.View())
		if err != nil || !obs.Running {
			t.Fatal("restart lost surviving execution", obs, err)
		}
	}
	if len(ids) != 3 || native.launched != 3 {
		t.Fatal("executions were reused")
	}
	state := store.View()
	body, _ := json.Marshal(state)
	if bytes.Contains(body, []byte("PRIVATE-JIT-FIXTURE")) {
		t.Fatal("persisted private bootstrap")
	}
	public, _ := json.Marshal(statusOf(state, false))
	for _, value := range []string{"fixture-supervisor", "fixture-worker", c.Storage.Data, "host_directories", "host_executions"} {
		if bytes.Contains(public, []byte(value)) {
			t.Fatal("private identity leaked to public status", value)
		}
	}
	if err := driver.Validate(context.Background(), c, p, state); err != nil {
		t.Fatal("active distribution changed", err)
	}
}
func TestHostCancellationAndImmediateFailure(t *testing.T) {
	for _, scenario := range []string{"before preparation", "during launch publication", "immediate exit", "missing supervisor"} {
		t.Run(scenario, func(t *testing.T) {
			c, store, native, p := hostFixture(t)
			p = buildHostFixture(t, c, store, native, p)
			r := seedHostFixture(t, c, store, p)
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			switch scenario {
			case "before preparation":
				cancel()
			case "during launch publication":
				native.beforePublish = func() { _ = store.Update(func(s *Snapshot) error { s.Runners[r.ID].Forced = true; return nil }) }
			case "immediate exit":
				native.phase = HostFailed
			case "missing supervisor":
				native.launchErr = errors.New("fixture launch outcome unavailable")
			}
			driver := HostDriver{Store: store, Native: native}
			if err := driver.Prepare(ctx, c, p, r, store.View(), "jit", func(Handle) error { return nil }); err == nil {
				t.Fatal("accepted unsuccessful preparation")
			}
			if scenario == "before preparation" && native.launched != 0 {
				t.Fatal("launched cancelled work")
			}
			if scenario == "during launch publication" && len(native.alive) > 0 {
				t.Fatal("launched after durable force")
			}
			if scenario == "immediate exit" {
				if err := driver.Stop(context.Background(), c, r, store.View()); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "missing supervisor" {
				requireCode(t, driver.Stop(context.Background(), c, r, store.View()), ErrCleanup)
			}
		})
	}
}
func TestHostPreparationWaitsForDetachedStatus(t *testing.T) {
	for _, scenario := range []string{"delayed publication", "deadline", "cancellation"} {
		t.Run(scenario, func(t *testing.T) {
			c, store, native, p := hostFixture(t)
			p = buildHostFixture(t, c, store, native, p)
			r := seedHostFixture(t, c, store, p)
			type publication struct {
				root   *os.Root
				status HostExecutionStatus
			}
			launched := make(chan publication, 1)
			native.writeStatus = func(root *os.Root, status HostExecutionStatus) error {
				launched <- publication{root, status}
				return nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			driver := HostDriver{Store: store, Native: native}
			result := make(chan error, 1)
			go func() {
				result <- driver.Prepare(ctx, c, p, r, store.View(), "jit", func(Handle) error { return nil })
			}()
			var published publication
			select {
			case published = <-launched:
			case err := <-result:
				t.Fatalf("preparation ended before launch: %v", err)
			case <-ctx.Done():
				t.Fatal("launch did not complete")
			}
			_, _, err := driver.observe(ctx, c, r, store.View())
			requireCode(t, err, ErrCleanup)
			select {
			case err := <-result:
				t.Fatalf("missing initial status failed preparation: %v", err)
			case <-time.After(100 * time.Millisecond):
			}
			switch scenario {
			case "delayed publication":
				if err := hostRootWrite(published.root, "status.json", published.status); err != nil {
					t.Fatal(err)
				}
			case "cancellation":
				cancel()
			}
			err = <-result
			if scenario == "delayed publication" {
				if err != nil || store.View().HostExecutions[r.ID].LaunchPending {
					t.Fatalf("delayed startup was not accepted: %v", err)
				}
			} else {
				want := context.DeadlineExceeded
				if scenario == "cancellation" {
					want = context.Canceled
				}
				if !errors.Is(err, want) || !store.View().HostExecutions[r.ID].LaunchPending {
					t.Fatalf("uncertain launch lost its deadline or reservation: %v", err)
				}
			}
		})
	}
}

func TestHostOwnershipPreservesForeignAndUncertainResources(t *testing.T) {
	for _, scenario := range []string{"PID reuse", "lost supervisor", "symlink", "replaced directory", "marker changed", "missing directory", "lost launch journal", "lost worker publication"} {
		t.Run(scenario, func(t *testing.T) {
			c, store, native, p := hostFixture(t)
			p = buildHostFixture(t, c, store, native, p)
			r := prepareHostFixture(t, c, store, native, p)
			state := store.View()
			d := state.HostDirectories[r.ID]
			e := state.HostExecutions[r.ID]
			path := hostDirectoryPath(c, *d)
			switch scenario {
			case "lost launch journal":
				if err := store.Update(func(v *Snapshot) error { delete(v.HostExecutions, r.ID); return nil }); err != nil {
					t.Fatal(err)
				}
				state = store.View()
			case "lost worker publication":
				status := native.statuses[e.Supervisor.PID]
				status.Worker = HostProcess{}
				status.Phase = HostStarting
				if err := hostRootWrite(native.roots[e.Supervisor.PID], "status.json", status); err != nil {
					t.Fatal(err)
				}
				delete(native.alive, e.Supervisor.PID)
			case "PID reuse":
				native.alive[e.Supervisor.PID] = HostProcess{PID: e.Supervisor.PID, Start: "foreign", Group: e.Supervisor.Group}
			case "lost supervisor":
				delete(native.alive, e.Supervisor.PID)
			case "symlink":
				if err := os.Rename(path, path+"-preserved"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path+"-preserved", path); err != nil {
					t.Fatal(err)
				}
			case "replaced directory":
				if err := os.Rename(path, path+"-preserved"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			case "marker changed":
				if err := os.WriteFile(filepath.Join(path, hostOwnerFile), []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
			case "missing directory":
				if err := os.Rename(path, path+"-preserved"); err != nil {
					t.Fatal(err)
				}
			}
			driver := HostDriver{Store: store, Native: native}
			if err := driver.Stop(context.Background(), c, r, state); err == nil {
				t.Fatal("uncertain ownership released capacity")
			}
			if native.stopped != 0 {
				t.Fatal("signalled uncertain or unrelated process")
			}
			if err := driver.Cleanup(context.Background(), c, r, store.View()); err == nil {
				t.Fatal("deleted unconfirmed execution")
			}
			if used, _, _ := usage(store.View()); used.CPU < r.Resources.CPU {
				t.Fatal("released uncertain reservation")
			}
		})
	}
}
func TestHostVerifiedCleanupRepeatedAndEscapingLinks(t *testing.T) {
	c, store, native, p := hostFixture(t)
	p = buildHostFixture(t, c, store, native, p)
	r := prepareHostFixture(t, c, store, native, p)
	d := store.View().HostDirectories[r.ID]
	outside := filepath.Join(c.Storage.Data, "personal.txt")
	if err := os.WriteFile(outside, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(hostDirectoryPath(c, *d), "home/escape")); err != nil {
		t.Fatal(err)
	}
	driver := HostDriver{Store: store, Native: native}
	for i := 0; i < 2; i++ {
		if err := driver.Stop(context.Background(), c, r, store.View()); err != nil {
			t.Fatal(err)
		}
		if err := driver.Cleanup(context.Background(), c, r, store.View()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(hostDirectoryPath(c, *d)); !os.IsNotExist(err) {
		t.Fatal("execution directory retained", err)
	}
	if body, err := os.ReadFile(outside); err != nil || string(body) != "preserve" {
		t.Fatal("removed personal resource")
	}
	if err := driver.Validate(context.Background(), c, p, store.View()); err != nil {
		t.Fatal("cleanup removed shared distribution", err)
	}
}
func TestHostEnvironmentAllowlist(t *testing.T) {
	execution := filepath.Join(t.TempDir(), "execution")
	t.Setenv("PATH", "/fixture/tools:/usr/bin")
	t.Setenv("DEVELOPER_DIR", "/fixture/Xcode.app/Contents/Developer")
	t.Setenv("RUNMOOR_TEST_CREDENTIAL", "secret-management-value")
	t.Setenv("HOME", "/personal/home")
	t.Setenv("TMPDIR", "/personal/temp")
	t.Setenv("ARBITRARY_SECRET", "secret-management-value")
	env := hostEnvironment(execution)
	for _, want := range []string{"PATH=/fixture/tools:/usr/bin", "DEVELOPER_DIR=/fixture/Xcode.app/Contents/Developer", "HOME=" + filepath.Join(execution, "home"), "TMPDIR=" + filepath.Join(execution, "tmp"), "TMP=" + filepath.Join(execution, "tmp"), "TEMP=" + filepath.Join(execution, "tmp")} {
		if !slices.Contains(env, want) {
			t.Fatal("missing environment", want)
		}
	}
	for _, forbidden := range []string{"secret-management-value", "RUNMOOR_TEST_CREDENTIAL", "ARBITRARY_SECRET", "/personal"} {
		if strings.Contains(strings.Join(env, "\n"), forbidden) {
			t.Fatal("inherited unsafe environment")
		}
	}
}

func TestHostManagedUpdatePinsAndRetainsActiveDistribution(t *testing.T) {
	c, store, native, p := hostFixture(t)
	m := NewManager(store, "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer m.cancel()
	m.Drivers = func(kind Backend) (Driver, error) {
		if kind != Host {
			t.Fatal("host-only execution probed another backend")
		}
		return &HostDriver{Store: store, Native: native}, nil
	}
	if err := m.activate(c); err != nil {
		t.Fatal(err)
	}
	release, body := fixtureRelease(t, Tart, "arm64")
	m.RunnerBuilder = &ManagedImageBuilder{Store: store, Host: native, Client: archiveClient(body), Log: m.Log}
	setManagedReleases(t, m, []RunnerRelease{release})
	m.updateManaged(context.Background(), p.Name)
	first := store.View().Managed[p.Name]
	if first == nil || first.Current == nil || first.Current.RunnerVersion != "2.338.0" {
		t.Fatal("initial host distribution not activated", first)
	}
	old := first.CurrentArtifact
	running := prepareHostFixture(t, c, store, native, *first.Current)
	for _, version := range []string{"2.339.0", "2.340.0"} {
		next := release
		next.Tag = "v" + version
		next.Assets = slices.Clone(release.Assets)
		for i := range next.Assets {
			next.Assets[i].Name = strings.ReplaceAll(release.Assets[i].Name, "2.338.0", version)
			next.Assets[i].URL = strings.ReplaceAll(release.Assets[i].URL, "2.338.0", version)
		}
		// Assets belong to this independent fixture, not the earlier release slice.
		next.Assets = slices.Clone(next.Assets)
		setManagedReleases(t, m, []RunnerRelease{next, release})
		if err := m.requestRunnerUpdate(p.Name); err != nil {
			t.Fatal(err)
		}
		setManagedReleases(t, m, []RunnerRelease{next, release})
		m.updateManaged(context.Background(), p.Name)
	}
	state := store.View()
	if state.Artifacts[old] == nil || state.HostDirectories[old] == nil || state.Runners[running.ID].Image != old {
		t.Fatal("active generation was collected or rewritten")
	}
	// A pinned version still uses verified distributions rather than image pins.
	c.Pools[0].RunnerVersion = "2.338.0"
	if err := m.accept(c, false); err != nil {
		t.Fatal(err)
	}
	setManagedReleases(t, m, []RunnerRelease{release})
	m.updateManaged(context.Background(), p.Name)
	if q := store.View().Managed[p.Name]; q.Mode != RunnerPinned || q.Current.RunnerVersion != "2.338.0" {
		t.Fatal("pin not preserved", q)
	}
}
func TestHostArtifactInterruptedRemovalAndMarkerlessIntent(t *testing.T) {
	c, store, native, p := hostFixture(t)
	p = buildHostFixture(t, c, store, native, p)
	d := *store.View().HostDirectories[p.Image]
	root, err := openHostDirectory(c, d, d.Installation, false)
	if err != nil {
		t.Fatal(err)
	}
	root.Close()
	parent, err := os.OpenRoot(hostParent(c, d.Kind))
	if err != nil {
		t.Fatal(err)
	}
	if err = hostRenameNoReplace(parent, d.ID, ".remove-"+d.ID); err != nil {
		t.Fatal(err)
	}
	parent.Close()
	builder := &ManagedImageBuilder{Store: store}
	if err = builder.cleanupHost(context.Background(), c, *store.View().Artifacts[d.ID]); err != nil {
		t.Fatal("staged removal did not recover", err)
	}
	// If final deletion succeeded but the caller's state transition failed, its
	// committed removal proof allows a safe idempotent retry of confirmed absence.
	d.RemovalCommitted = true
	if err = store.Update(func(s *Snapshot) error { s.HostDirectories[d.ID] = &d; return nil }); err != nil {
		t.Fatal(err)
	}
	if err = builder.cleanupHost(context.Background(), c, *store.View().Artifacts[d.ID]); err != nil {
		t.Fatal("confirmed removal retry failed", err)
	}
	id := newID()
	intent := HostDirectory{ID: id, Token: newID(), Kind: HostWorkspace, Installation: store.View().Installation}
	if err = privateDir(hostParent(c, intent.Kind)); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(hostDirectoryPath(c, intent), 0700); err != nil {
		t.Fatal(err)
	}
	if err = store.Update(func(s *Snapshot) error { s.HostDirectories[id] = &intent; return nil }); err != nil {
		t.Fatal(err)
	}
	_, _, err = createHostDirectory(context.Background(), store, c, id, HostWorkspace)
	requireCode(t, err, ErrOwnership)
	requireCode(t, removeHostDirectory(context.Background(), store, c, intent), ErrOwnership)
}

type fixtureHostWorker struct {
	mu       sync.Mutex
	process  HostProcess
	done     chan int
	members  []HostProcess
	started  int
	stopped  int
	startErr error
	deadline func(HostBootstrap, time.Time) time.Time
}

func (f *fixtureHostWorker) Start(context.Context, *os.Root, HostBootstrap) (HostProcess, <-chan int, error) {
	f.started++
	return f.process, f.done, f.startErr
}
func (f *fixtureHostWorker) Members(HostProcess) ([]HostProcess, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.members), nil
}
func (f *fixtureHostWorker) Terminate(p HostProcess, force bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !sameHostProcess(p, f.process) {
		return hostOwnership()
	}
	f.stopped++
	f.members = nil
	select {
	case f.done <- 1:
	default:
	}
	return nil
}
func (f *fixtureHostWorker) Deadline(in HostBootstrap, previous time.Time) time.Time {
	if f.deadline != nil {
		return f.deadline(in, previous)
	}
	return previous
}

func TestHostJobDeadlineRequiresBusyAuthority(t *testing.T) {
	c, store, native, p := hostFixture(t)
	p = buildHostFixture(t, c, store, native, p)
	r := prepareHostFixture(t, c, store, native, p)
	s := store.View()
	in := HostBootstrap{Directory: *s.HostDirectories[r.ID]}
	for _, phase := range []RunnerPhase{Preparing, Idle, Cleaning, Completed, Quarantined} {
		s.Runners[r.ID].Phase = phase
		if deadline := hostJobDeadline(in, time.Time{}, s); !deadline.IsZero() {
			t.Fatalf("%s established a job deadline: %s", phase, deadline)
		}
	}
	s.Runners[r.ID].Phase = Busy
	assigned := time.Now().Add(6 * time.Hour).UTC()
	s.Runners[r.ID].Deadline = assigned
	if deadline := hostJobDeadline(in, time.Time{}, s); !deadline.Equal(assigned) {
		t.Fatal("assignment deadline was shortened", deadline)
	}
	// Busy-aware removal establishes the existing conservative bound when no
	// assignment start was observed, using the execution's original generation.
	s.Runners[r.ID].Phase = Idle
	s.Runners[r.ID].Generation = "original"
	s.Generations["original"] = c
	recordBusyRemoval(&s, r.ID, Idle, nil)
	want := r.CreatedAt.Add(c.JobTimeout())
	if deadline := hostJobDeadline(in, time.Time{}, s); !deadline.Equal(want) {
		t.Fatal("unknown start lost the conservative deadline", deadline)
	}
	s.Runners[r.ID].Phase = Cleaning
	if deadline := hostJobDeadline(in, want, s); !deadline.Equal(want) {
		t.Fatal("later lifecycle cleared an established deadline")
	}
	s.Installation = newID()
	if deadline := hostJobDeadline(in, time.Time{}, s); !deadline.IsZero() {
		t.Fatal("foreign installation established a deadline")
	}
}

func TestHostSupervisorIdleAndBusyDeadlines(t *testing.T) {
	for _, busy := range []bool{false, true} {
		t.Run(map[bool]string{false: "idle", true: "busy"}[busy], func(t *testing.T) {
			c, store, _, _ := hostFixture(t)
			d, root, err := createHostDirectory(context.Background(), store, c, newID(), HostWorkspace)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			process := HostProcess{PID: 77, Start: "worker-77", Group: 77}
			worker := &fixtureHostWorker{process: process, done: make(chan int, 1), members: []HostProcess{process}}
			startupDeadline := time.Now().Add(1100 * time.Millisecond)
			observed := make(chan struct{}, 1)
			worker.deadline = func(_ HostBootstrap, previous time.Time) time.Time {
				if !time.Now().Before(startupDeadline) {
					select {
					case observed <- struct{}{}:
					default:
					}
					if busy {
						return startupDeadline
					}
				}
				return previous
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			finished := make(chan int, 1)
			status := HostExecutionStatus{ID: d.ID, Token: d.Token, Supervisor: HostProcess{PID: 66, Start: "supervisor-66", Group: 66}}
			go func() {
				finished <- superviseHost(ctx, root, HostBootstrap{Directory: d, Deadline: startupDeadline}, status, worker)
			}()
			select {
			case <-observed:
			case <-ctx.Done():
				t.Fatal("deadline was not observed")
			}
			if !busy {
				worker.mu.Lock()
				worker.members = nil
				worker.mu.Unlock()
				worker.done <- 0
			}
			<-finished
			final, err := hostReadStatus(root, d)
			if err != nil {
				t.Fatal(err)
			}
			if busy && (final.Phase != HostFailed || worker.stopped == 0) {
				t.Fatal("busy timeout left a worker alive")
			}
			if !busy && (final.Phase != HostFinished || worker.stopped != 0) {
				t.Fatal("idle runner was terminated on its bootstrap deadline")
			}
		})
	}
}

func TestHostSupervisorDeadlinePollingBounded(t *testing.T) {
	c, store, _, _ := hostFixture(t)
	d, root, err := createHostDirectory(context.Background(), store, c, newID(), HostWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	process := HostProcess{PID: 77, Start: "worker-77", Group: 77}
	worker := &fixtureHostWorker{process: process, done: make(chan int, 1), members: []HostProcess{process}}
	reads := make(chan struct{}, 16)
	worker.deadline = func(_ HostBootstrap, previous time.Time) time.Time {
		reads <- struct{}{}
		return previous
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan int, 1)
	status := HostExecutionStatus{ID: d.ID, Token: d.Token, Supervisor: HostProcess{PID: 66, Start: "supervisor-66", Group: 66}}
	go func() {
		finished <- superviseHost(ctx, root, HostBootstrap{Directory: d, Deadline: time.Now().Add(time.Minute)}, status, worker)
	}()
	select {
	case <-reads:
	case <-time.After(5 * time.Second):
		cancel()
		<-finished
		t.Fatal("initial deadline was not read")
	}
	select {
	case <-reads:
		cancel()
		<-finished
		t.Fatal("idle supervisor reopened state at the process-observation cadence")
	case <-time.After(300 * time.Millisecond):
	}
	cancel()
	<-finished
}

func TestHostSupervisorCachedDeadlineTimer(t *testing.T) {
	c, store, _, _ := hostFixture(t)
	d, root, err := createHostDirectory(context.Background(), store, c, newID(), HostWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	process := HostProcess{PID: 77, Start: "worker-77", Group: 77}
	worker := &fixtureHostWorker{process: process, done: make(chan int, 1), members: []HostProcess{process}}
	var deadline time.Time
	worker.deadline = func(_ HostBootstrap, previous time.Time) time.Time {
		if deadline.IsZero() {
			deadline = time.Now().Add(50 * time.Millisecond)
		}
		return deadline
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	status := HostExecutionStatus{ID: d.ID, Token: d.Token, Supervisor: HostProcess{PID: 66, Start: "supervisor-66", Group: 66}}
	superviseHost(ctx, root, HostBootstrap{Directory: d, Deadline: time.Now().Add(time.Minute)}, status, worker)
	if ctx.Err() != nil || worker.stopped == 0 {
		t.Fatal("cached deadline waited for the next five-second state poll")
	}
}

func TestHostSupervisorMockedExitTimeoutAndCancellation(t *testing.T) {
	for _, scenario := range []string{"immediate exit", "start failure", "cancellation", "timeout", "normal exit"} {
		t.Run(scenario, func(t *testing.T) {
			c, store, _, _ := hostFixture(t)
			d, root, err := createHostDirectory(context.Background(), store, c, newID(), HostWorkspace)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			process := HostProcess{PID: 77, Start: "worker-77", Group: 77}
			worker := &fixtureHostWorker{process: process, done: make(chan int, 1), members: []HostProcess{process}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			deadline := time.Now().Add(time.Minute)
			switch scenario {
			case "immediate exit":
				worker.done <- 0
				worker.members = nil
			case "start failure":
				worker.startErr = errors.New("fixture")
			case "cancellation":
				cancel()
			case "timeout":
				deadline = time.Now().Add(20 * time.Millisecond)
			case "normal exit":
				go func() {
					time.Sleep(1100 * time.Millisecond)
					worker.mu.Lock()
					worker.members = nil
					worker.mu.Unlock()
					worker.done <- 0
				}()
			}
			status := HostExecutionStatus{ID: d.ID, Token: d.Token, Supervisor: HostProcess{PID: 66, Start: "supervisor-66", Group: 66}}
			superviseHost(ctx, root, HostBootstrap{Directory: d, Deadline: deadline}, status, worker)
			final, err := hostReadStatus(root, d)
			if err != nil {
				t.Fatal(err)
			}
			if final.Phase != HostFailed && final.Phase != HostFinished {
				t.Fatal("supervisor did not publish terminal outcome")
			}
			if scenario == "normal exit" && final.Phase != HostFinished {
				t.Fatal("normal exit reported preparation failure")
			}
			if scenario == "timeout" && worker.stopped == 0 {
				t.Fatal("timeout left worker alive")
			}
			if scenario == "cancellation" && worker.started != 0 {
				t.Fatal("started after cancellation")
			}
		})
	}
}

type fixtureHostRemote struct {
	fakeRemote
	next int
}

func (r *fixtureHostRemote) JIT(context.Context, PoolState, Runner) (int, string, error) {
	r.next++
	return r.next, "fixture-private-jit-" + newID(), nil
}
func TestHostManagerRegistrationCompletionTimeoutAndScopedStop(t *testing.T) {
	c, store, native, p := hostFixture(t)
	m := NewManager(store, "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer m.cancel()
	remote := &fixtureHostRemote{}
	m.RemoteFactory = func(Connection) (Remote, error) { return remote, nil }
	m.Drivers = func(kind Backend) (Driver, error) {
		if kind != Host {
			t.Fatal("probed non-host backend")
		}
		return &HostDriver{Store: store, Native: native}, nil
	}
	if err := m.activate(c); err != nil {
		t.Fatal(err)
	}
	release, body := fixtureRelease(t, Tart, "arm64")
	m.RunnerBuilder = &ManagedImageBuilder{Store: store, Host: native, Client: archiveClient(body), Log: m.Log}
	setManagedReleases(t, m, []RunnerRelease{release})
	m.updateManaged(context.Background(), p.Name)
	var pool string
	if err := store.Update(func(s *Snapshot) error {
		for id, q := range s.Pools {
			if q.Spec.Name == p.Name && q.Phase == Ready {
				pool = id
				q.ScaleSetID = 1
				q.Session = "host-session"
			}
		}
		return nil
	}); err != nil || pool == "" {
		t.Fatal(err)
	}
	var runners []Runner
	for i := 0; i < 3; i++ {
		id := seedRunner(t, m, pool, Preparing)
		if err := store.Update(func(s *Snapshot) error {
			r := s.Runners[id]
			r.Backend = Host
			r.Image = s.Pools[pool].Spec.Image
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		m.prepare(context.Background(), id)
		r := *store.View().Runners[id]
		if r.Phase != Idle || r.GitHubID != i+1 {
			t.Fatal("host JIT registration not distinct or ready", r.Phase, r.GitHubID)
		}
		runners = append(runners, r)
	}
	message := &scaleset.RunnerScaleSetMessage{MessageID: 1, Statistics: &scaleset.RunnerScaleSetStatistic{TotalAssignedJobs: 3}, JobStartedMessages: []*scaleset.JobStarted{{RunnerID: runners[0].GitHubID, RunnerName: runners[0].Name}}}
	if err := store.Update(func(s *Snapshot) error { return applyMessage(s, pool, "host-session", message) }); err != nil {
		t.Fatal(err)
	}
	if r := store.View().Runners[runners[0].ID]; r.Phase != Busy || r.Deadline.Sub(r.StartedAt) != 6*time.Hour {
		t.Fatal("job deadline not reset on assignment")
	}
	message = &scaleset.RunnerScaleSetMessage{MessageID: 2, Statistics: &scaleset.RunnerScaleSetStatistic{}, JobCompletedMessages: []*scaleset.JobCompleted{{RunnerID: runners[0].GitHubID, RunnerName: runners[0].Name}}}
	if err := store.Update(func(s *Snapshot) error { return applyMessage(s, pool, "host-session", message) }); err != nil {
		t.Fatal(err)
	}
	m.cleanup(context.Background(), runners[0].ID)
	if r := store.View().Runners[runners[0].ID]; r.Phase != Completed || !r.Terminated || !r.LocalCleaned {
		t.Fatal("manager completion cleanup failed", r.Phase, r.Problem)
	}
	if err := store.Update(func(s *Snapshot) error {
		r := s.Runners[runners[1].ID]
		r.Phase = Busy
		r.Deadline = time.Now().Add(-time.Second)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	m.inspect(context.Background(), runners[1].ID)
	m.cleanup(context.Background(), runners[1].ID)
	if r := store.View().Runners[runners[1].ID]; r.Phase != Completed {
		t.Fatal("timed-out host execution not cleaned", r.Phase, r.Problem)
	}
	if err := m.StopPool("unrelated", true); err == nil {
		t.Fatal("unknown pool unexpectedly stopped")
	}
	if r := store.View().Runners[runners[2].ID]; r.Phase != Idle {
		t.Fatal("scoped stop modified another pool")
	}
	if err := m.StopPool(p.Name, true); err != nil {
		t.Fatal(err)
	}
	m.cleanup(context.Background(), runners[2].ID)
	if r := store.View().Runners[runners[2].ID]; r.Phase != Completed {
		t.Fatal("force-stop not cleaned", r.Phase, r.Problem)
	}
}
