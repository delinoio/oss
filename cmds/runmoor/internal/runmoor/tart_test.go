package runmoor

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type tartFixture struct {
	mu                sync.Mutex
	c                 Config
	running           map[string]bool
	commands          [][]string
	jit               string
	version           string
	guestOS           tartGuestOS
	rejectGuest       bool
	afterGet          func(string)
	beforeDelete      func(string)
	beforePinned      func([]string, *os.File)
	beforeStartPinned func([]string, *os.File)
	pinnedSetIdentity string
	pinnedSetInfo     os.FileInfo
}

// Keep fake Tart processes outside the host PID range so macOS CI cannot
// mistake a real system process for one of these detached test processes.
const fixtureTartPID = 1 << 30

func (f *tartFixture) Run(_ context.Context, name string, args, env []string, in io.Reader) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands, append([]string{}, args...))
	if name == "/usr/bin/sw_vers" {
		return []byte("14.7"), nil
	}
	home := ""
	noAutoPrune := false
	for _, value := range env {
		if strings.HasPrefix(value, "TART_HOME=") {
			home = strings.TrimPrefix(value, "TART_HOME=")
		}
		if value == "TART_NO_AUTO_PRUNE=1" {
			noAutoPrune = true
		}
	}
	creationRoot := filepath.Join(f.c.Storage.Data, "tart-creation")
	validHome := home == tartHome(f.c) || filepath.Dir(home) == creationRoot && validID(filepath.Base(home))
	if !noAutoPrune || !validHome {
		return nil, problem(ErrConfig, "Unsafe Tart environment.", "Fix the test boundary.")
	}
	for _, v := range env {
		if strings.HasPrefix(v, "RUNMOOR_TEST_CREDENTIAL=") {
			return nil, problem(ErrAuth, "Credential leaked to Tart.", "Remove it.")
		}
	}
	switch args[0] {
	case "--version":
		return []byte(f.version), nil
	case "create", "clone", "import":
		vm := args[len(args)-1]
		dir := tartVMPathAtHome(home, vm)
		if e := os.MkdirAll(dir, 0700); e != nil {
			return nil, e
		}
		for _, name := range []string{"config.json", "nvram.bin", "disk.img"} {
			if e := os.WriteFile(filepath.Join(dir, name), []byte("immutable-fixture"), 0600); e != nil {
				return nil, e
			}
		}
		f.running[vm] = false
	case "get":
		b, err := json.Marshal(vmInfo{Running: f.running[args[1]], State: "stopped", OS: f.guestOS, CPU: 1, Memory: 512})
		if f.afterGet != nil {
			hook := f.afterGet
			f.afterGet = nil
			hook(args[1])
		}
		return b, err
	case "set":
	case "stop":
		f.running[args[1]] = false
	case "delete":
		if f.beforeDelete != nil {
			hook := f.beforeDelete
			f.beforeDelete = nil
			hook(args[1])
		}
		delete(f.running, args[1])
		return nil, os.RemoveAll(tartVMPathAtHome(home, args[1]))
	case "exec":
		if strings.Contains(strings.Join(args, " "), "__guest-bootstrap") {
			b, _ := io.ReadAll(in)
			var v GuestInput
			if e := json.Unmarshal(b, &v); e != nil {
				return nil, e
			}
			f.jit = v.JIT
		} else if strings.Contains(strings.Join(args, " "), "__guest-status") {
			return json.Marshal(GuestStatus{ID: args[len(args)-1], Ready: true})
		} else if in != nil {
			io.Copy(io.Discard, in)
		} else {
			if f.rejectGuest {
				return []byte("RUNMOOR_INVALID\n"), nil
			}
			return []byte("RUNMOOR_READY\n"), nil
		}
	default:
		return nil, problem(ErrConfig, "Unexpected Tart fixture operation.", "Extend the fixture deliberately.")
	}
	return nil, nil
}
func (f *tartFixture) Start(_ string, args, env []string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands, append([]string{}, args...))
	f.running[args[len(args)-1]] = true
	return fixtureTartPID, nil
}
func (f *tartFixture) canonicalPinnedArgs(args, env []string, dir *os.File) ([]string, error) {
	b, err := readTartVMOwnerMarker(dir, 4096)
	if err != nil {
		return nil, err
	}
	var owner vmOwner
	if err = json.Unmarshal(b, &owner); err != nil {
		return nil, err
	}
	home := filepath.Join(f.c.Storage.Data, "tart")
	for _, value := range env {
		if strings.HasPrefix(value, "TART_HOME=") {
			home = strings.TrimPrefix(value, "TART_HOME=")
		}
	}
	result := append([]string(nil), args...)
	for i, arg := range result {
		if target, linkErr := os.Readlink(filepath.Join(home, "vms", arg)); linkErr == nil && target == "/dev/fd/3" {
			result[i] = owner.VM
		}
	}
	return result, nil
}
func (f *tartFixture) RunPinned(ctx context.Context, name string, args, env []string, in io.Reader, dir *os.File) ([]byte, error) {
	if f.beforePinned != nil {
		hook := f.beforePinned
		f.beforePinned = nil
		hook(args, dir)
	}
	canonical, err := f.canonicalPinnedArgs(args, env, dir)
	if err != nil {
		return nil, err
	}
	if len(canonical) > 0 && canonical[0] == "set" {
		f.pinnedSetIdentity = canonical[1]
		f.pinnedSetInfo, err = dir.Stat()
		if err != nil {
			return nil, err
		}
	}
	return f.Run(ctx, name, canonical, env, in)
}
func (f *tartFixture) StartPinned(name string, args, env []string, dir *os.File) (int, error) {
	if f.beforeStartPinned != nil {
		hook := f.beforeStartPinned
		f.beforeStartPinned = nil
		hook(args, dir)
	}
	canonical, err := f.canonicalPinnedArgs(args, env, dir)
	if err != nil {
		return 0, err
	}
	return f.Start(name, canonical, env)
}
func fakeTart(c Config) (*TartDriver, *tartFixture) {
	f := &tartFixture{c: c, running: map[string]bool{}, version: "2.37.0", guestOS: tartGuestOSDarwin}
	return &TartDriver{
		Exec: f, HostCheck: func(context.Context) error { return nil },
		processIdentity: func(int) (string, error) { return "fixture-process-start", nil },
	}, f
}

type tartVMInfoResponse struct {
	CommandExecutor
	output []byte
	err    error
}

func (r tartVMInfoResponse) Run(ctx context.Context, name string, args, env []string, in io.Reader) ([]byte, error) {
	if len(args) > 0 && args[0] == "get" {
		return r.output, r.err
	}
	return r.CommandExecutor.Run(ctx, name, args, env, in)
}

func TestTartVersionCompatibility(t *testing.T) {
	for _, tc := range []struct {
		version string
		allowed bool
	}{
		{"2.0.0", true},
		{"2.37.0", true},
		{"2.999.999", true},
		{"2.38.0+build.7", true},
		{" 2.38.0\n", true},
		{"1.99.0", false},
		{"3.0.0", false},
		{"2.38.0-beta.1", false},
		{"2", false},
		{"2.38", false},
		{"2.038.0", false},
		{"2.38.0 junk", false},
		{"v2.38.0", false},
		{"2.38.0+", false},
		{"", false},
	} {
		t.Run(tc.version, func(t *testing.T) {
			c, _ := fixtureStore(t)
			driver, fixture := fakeTart(c)
			fixture.version = tc.version
			err := driver.check(context.Background(), c)
			if tc.allowed {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			requireCode(t, err, ErrDependency)
		})
	}
}

type startingTartCommand struct {
	*tartFixture
	started           bool
	polls, readyAfter int
}

func (f *startingTartCommand) Start(string, []string, []string) (int, error) {
	f.started = true
	return fixtureTartPID, nil
}
func (f *startingTartCommand) StartPinned(name string, args, env []string, dir *os.File) (int, error) {
	f.started = true
	if _, err := f.tartFixture.canonicalPinnedArgs(args, env, dir); err != nil {
		return 0, err
	}
	return fixtureTartPID, nil
}
func (f *startingTartCommand) Run(ctx context.Context, name string, args, env []string, in io.Reader) ([]byte, error) {
	if f.started && args[0] == "get" {
		f.polls++
		if f.readyAfter > 0 && f.polls >= f.readyAfter {
			f.mu.Lock()
			f.running[args[1]] = true
			f.mu.Unlock()
		}
	}
	return f.tartFixture.Run(ctx, name, args, env, in)
}
func (f *startingTartCommand) RunPinned(ctx context.Context, name string, args, env []string, in io.Reader, dir *os.File) ([]byte, error) {
	if f.beforePinned != nil {
		hook := f.beforePinned
		f.beforePinned = nil
		hook(args, dir)
	}
	canonical, err := f.tartFixture.canonicalPinnedArgs(args, env, dir)
	if err != nil {
		return nil, err
	}
	return f.Run(ctx, name, canonical, env, in)
}

func TestImageOpenWaitsForConfirmedVMStartup(t *testing.T) {
	for _, readyAfter := range []int{0, 2} {
		t.Run(map[int]string{0: "detached process exited without booting", 2: "delayed boot"}[readyAfter], func(t *testing.T) {
			c, s := fixtureStore(t)
			driver, fixture := fakeTart(c)
			command := &startingTartCommand{tartFixture: fixture, readyAfter: readyAfter}
			driver.Exec = command
			images := &ImageManager{Store: s, Tart: driver}
			im, err := images.Operate(context.Background(), c, ImageRequest{Action: "create", Name: "setup", IPSW: "/fixture.ipsw", Resources: Resources{1, 512}})
			if err != nil {
				t.Fatal(err)
			}
			timeout := time.Second
			if readyAfter == 0 {
				timeout = 50 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			_, err = images.Operate(ctx, c, ImageRequest{Action: "open", ID: im.ID})
			if readyAfter > 0 {
				if err != nil || command.polls < readyAfter {
					t.Fatal("image open acknowledged an unconfirmed VM", err)
				}
				if got := s.View().ImageTartPIDs[im.ID]; got != fixtureTartPID {
					t.Fatalf("persisted setup Tart PID = %d, want %d", got, fixtureTartPID)
				}
				if got := s.View().ImageTartStarts[im.ID]; got != "fixture-process-start" {
					t.Fatalf("persisted setup Tart process start = %q, want fixture identity", got)
				}
				status, marshalErr := json.Marshal(statusOf(s.View(), false))
				if marshalErr != nil || strings.Contains(string(status), "image_tart_pids") || strings.Contains(string(status), "fixture-process-start") {
					t.Fatal("private image Tart process state leaked through status JSON", marshalErr)
				}
				return
			}
			requireCode(t, err, ErrPreparation)
			if s.View().Images[im.ID].Problem == nil || s.View().Images[im.ID].Phase != ImageOpen {
				t.Fatal("failed startup lost its diagnostic or uncertain reservation")
			}
			if err = images.Reconcile(context.Background(), c); err != nil {
				t.Fatal(err)
			}
			if s.View().Images[im.ID].Phase != ImagePreparing || s.View().Images[im.ID].Problem == nil {
				t.Fatal("confirmed stop discarded the startup diagnostic")
			}
		})
	}
}

func TestImageSealAndConfigurationShareRunnerPathValidation(t *testing.T) {
	for _, path := range []string{"/Users/runner/tools/../actions-runner", "/Users/runner/a..b", "relative/runner", "/runner\x00", "/runner\n", "/runner\r", "/Users/runner/actions-runner", "/Users/runner/actions runner", ""} {
		t.Run(path, func(t *testing.T) {
			c, s := fixtureStore(t)
			driver, fixture := fakeTart(c)
			images := &ImageManager{Store: s, Tart: driver}
			im, err := images.Operate(context.Background(), c, ImageRequest{Action: "create", Name: "setup", IPSW: "/fixture.ipsw", Resources: Resources{1, 512}})
			if err != nil {
				t.Fatal(err)
			}
			c.Pools[0].RunnerPath = path
			_, configErr := NormalizeConfig(c)
			before, calls := fingerprint(s.View()), len(fixture.commands)
			im, err = images.Operate(context.Background(), c, ImageRequest{Action: "seal", ID: im.ID, RunnerPath: path, RunnerVersion: "2.337.0"})
			if configErr != nil {
				requireCode(t, err, ErrConfig)
				if fingerprint(s.View()) != before {
					t.Fatal("invalid seal path mutated image state or reserved capacity")
				}
				for _, args := range fixture.commands[calls:] {
					if args[0] != "--version" {
						t.Fatal("invalid seal path reached VM preparation", args)
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := path
			if want == "" {
				want = "/Users/runner/actions-runner"
			}
			if im.RunnerPath != want || im.Phase != ImageSealed {
				t.Fatal("seal changed the accepted literal runner path")
			}
			c.Pools[0].RunnerPath = im.RunnerPath
			normalized, err := NormalizeConfig(c)
			if err != nil || normalized.Pools[0].RunnerPath != im.RunnerPath {
				t.Fatal("sealed metadata cannot be configured exactly", err)
			}
			p := normalized.Pools[0]
			p.Backend, p.Image = Tart, im.ID
			if err = driver.Validate(context.Background(), c, p, s.View()); err != nil {
				t.Fatal("pool rejected its compatible sealed path", err)
			}
		})
	}
}

func TestImageSealPreservesDependencyFailuresBeforeReservation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		output []byte
		err    error
	}{
		{name: "unavailable metadata", err: context.DeadlineExceeded},
		{name: "malformed metadata", output: []byte("{")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, s := fixtureStore(t)
			driver, fixture := fakeTart(c)
			images := &ImageManager{Store: s, Tart: driver}
			im, err := images.Operate(context.Background(), c, ImageRequest{Action: "create", Name: "setup", IPSW: "/fixture.ipsw", Resources: Resources{1, 512}})
			if err != nil {
				t.Fatal(err)
			}
			driver.Exec = tartVMInfoResponse{CommandExecutor: fixture, output: tc.output, err: tc.err}
			_, err = images.Operate(context.Background(), c, ImageRequest{Action: "seal", ID: im.ID, RunnerVersion: "2.337.0"})
			requireCode(t, err, ErrDependency)
			stored := s.View().Images[im.ID]
			if stored.Phase != ImagePreparing || stored.Problem == nil || stored.Problem.Code != ErrDependency {
				t.Fatalf("metadata dependency failure changed reservation state or classification: %#v", stored)
			}
			if fixture.running[im.VM] {
				t.Fatal("metadata dependency failure booted the VM")
			}
		})
	}
}

func TestImageSealRejectsUnsupportedGuestOSBeforeReservationOrBoot(t *testing.T) {
	for _, tc := range []struct {
		name    string
		guestOS tartGuestOS
	}{
		{name: "linux", guestOS: "linux"},
		{name: "missing", guestOS: ""},
		{name: "unknown", guestOS: "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, s := fixtureStore(t)
			driver, fixture := fakeTart(c)
			images := &ImageManager{Store: s, Tart: driver}
			im, err := images.Operate(context.Background(), c, ImageRequest{Action: "create", Name: "setup", IPSW: "/fixture.ipsw", Resources: Resources{1, 512}})
			if err != nil {
				t.Fatal(err)
			}
			fixture.guestOS = tc.guestOS
			firstCommand := len(fixture.commands)
			_, err = images.Operate(context.Background(), c, ImageRequest{Action: "seal", ID: im.ID, RunnerVersion: "2.337.0"})
			requireCode(t, err, ErrImage)
			if p, ok := err.(*Problem); !ok || !strings.Contains(p.Recovery, "macOS") {
				t.Fatalf("unsupported guest error lacks macOS recovery guidance: %v", err)
			}
			stored := s.View().Images[im.ID]
			if stored.Phase != ImagePreparing || stored.Problem == nil || stored.Problem.Code != ErrImage {
				t.Fatalf("unsupported guest changed reservation state or lost its diagnostic: %#v", stored)
			}
			for _, args := range fixture.commands[firstCommand:] {
				if args[0] == "run" || args[0] == "exec" || args[0] == "stop" {
					t.Fatalf("unsupported guest reached VM preparation: %v", args)
				}
			}
			if fixture.running[im.VM] {
				t.Fatal("unsupported guest was booted")
			}
		})
	}
}

func TestUnsupportedSealedGuestCannotValidateOrCloneAndCanBeRemoved(t *testing.T) {
	c, s := fixtureStore(t)
	driver, fixture := fakeTart(c)
	images := &ImageManager{Store: s, Tart: driver}
	ctx := context.Background()
	im, err := images.Operate(ctx, c, ImageRequest{Action: "create", Name: "base", IPSW: "/fixture.ipsw", Resources: Resources{1, 512}})
	if err != nil {
		t.Fatal(err)
	}
	im, err = images.Operate(ctx, c, ImageRequest{Action: "seal", ID: im.ID, RunnerVersion: "2.337.0"})
	if err != nil {
		t.Fatal(err)
	}
	fixture.guestOS = "linux"
	p := c.Pools[0]
	p.Backend, p.Image, p.RunnerPath, p.RunnerVersion = Tart, im.ID, im.RunnerPath, im.RunnerVersion
	firstCommand := len(fixture.commands)
	requireCode(t, driver.Validate(ctx, c, p, s.View()), ErrImage)
	published := false
	err = driver.Prepare(ctx, c, p, Runner{ID: newID(), Backend: Tart, Image: im.ID}, s.View(), "unused-jit", func(Handle) error {
		published = true
		return nil
	})
	requireCode(t, err, ErrImage)
	if published {
		t.Fatal("unsupported sealed image was published for job preparation")
	}
	_, err = images.Operate(ctx, c, ImageRequest{Action: "create", Name: "replacement", From: im.ID, Resources: Resources{1, 512}})
	requireCode(t, err, ErrImage)
	for _, args := range fixture.commands[firstCommand:] {
		if args[0] == "clone" || args[0] == "exec" || args[0] == "set" || args[0] == "run" {
			t.Fatalf("unsupported sealed image reached clone or helper transfer: %v", args)
		}
	}
	if retained := s.View().Images[im.ID]; retained == nil || retained.Phase != ImageSealed {
		t.Fatal("validation changed or discarded the unsupported sealed record")
	}
	if _, err = images.Operate(ctx, c, ImageRequest{Action: "remove", ID: im.ID}); err != nil {
		t.Fatalf("unsupported sealed image could not be explicitly removed: %v", err)
	}
	if s.View().Images[im.ID] != nil {
		t.Fatal("explicit image removal retained the unsupported revision")
	}
}

func TestTartImageLifecycleAndCredentialBoundary(t *testing.T) {
	c, s := fixtureStore(t)
	t.Setenv("RUNMOOR_TEST_CREDENTIAL", "management-secret-must-stay-host")
	driver, fixture := fakeTart(c)
	images := &ImageManager{Store: s, Tart: driver}
	ctx := context.Background()
	im, e := images.Operate(ctx, c, ImageRequest{Action: "create", Name: "xcode", IPSW: "/operator/local.ipsw", Resources: Resources{1, 512}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = images.Operate(ctx, c, ImageRequest{Action: "open", ID: im.ID}); e != nil {
		t.Fatal(e)
	}
	im, e = images.Operate(ctx, c, ImageRequest{Action: "seal", ID: im.ID, RunnerVersion: "2.337.0"})
	if e != nil {
		t.Fatal(e)
	}
	if im.Phase != ImageSealed || im.Digest == "" {
		t.Fatal("image not sealed")
	}
	baseMarker, e := os.ReadFile(vmOwnerMarkerPath(c, im.VM))
	if e != nil {
		t.Fatal("sealed base owner marker is missing", e)
	}
	_, e = images.Operate(ctx, c, ImageRequest{Action: "open", ID: im.ID})
	requireCode(t, e, ErrImage)
	p := c.Pools[0]
	p.Backend = Tart
	p.Image = im.ID
	p.RunnerPath = im.RunnerPath
	p.RunnerVersion = im.RunnerVersion
	r := Runner{ID: newID(), Backend: Tart, Image: im.ID}
	r.Name = "runmoor-" + r.ID
	snap := s.View()
	if e = driver.Prepare(ctx, c, p, r, snap, "fixture-jit", func(h Handle) error {
		r.Handle = h
		if h.PID > 0 {
			if h.tartProcessStart != "fixture-process-start" {
				t.Fatalf("published Tart process start = %q, want fixture identity", h.tartProcessStart)
			}
			snap.RunnerTartStarts = map[string]string{r.ID: h.tartProcessStart}
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	obs, e := driver.Inspect(ctx, c, r, snap)
	if e != nil || !obs.Exists || !obs.Running {
		t.Fatal("owned Tart clone did not survive inspection after manager restart", obs, e)
	}
	cloneMarker, e := os.ReadFile(vmOwnerMarkerPath(c, r.Handle.VM))
	if e != nil {
		t.Fatal("job clone owner marker is missing", e)
	}
	var clonedOwner vmOwner
	if json.Unmarshal(cloneMarker, &clonedOwner) != nil || clonedOwner.Installation != snap.Installation || clonedOwner.Entity != r.ID || clonedOwner.VM != r.Handle.VM {
		t.Fatal("job clone did not receive its fresh ownership identity", clonedOwner)
	}
	baseMarkerAfter, e := os.ReadFile(vmOwnerMarkerPath(c, im.VM))
	if e != nil || string(baseMarkerAfter) != string(baseMarker) {
		t.Fatal("cloning changed the sealed base owner marker", e)
	}
	if fixture.jit != "fixture-jit" {
		t.Fatal("JIT not delivered by stdin")
	}
	for _, cmd := range fixture.commands {
		if strings.Contains(strings.Join(cmd, " "), "fixture-jit") || strings.Contains(strings.Join(cmd, " "), "management-secret") {
			t.Fatal("secret interpolated into Tart argv")
		}
	}
	if e = driver.Stop(ctx, c, r, snap); e != nil {
		t.Fatal(e)
	}
	if e = driver.Cleanup(ctx, c, r, snap); e != nil {
		t.Fatal(e)
	}
	digest, e := imageDigestOwned(ctx, c, im.VM, snap.Installation, im.ID)
	if e != nil || digest != im.Digest {
		t.Fatal("base mutated during job lifecycle")
	}
	if _, e = os.Stat(vmPath(c, r.Handle.VM)); !os.IsNotExist(e) {
		t.Fatal("clone leaked")
	}
	if _, e = images.Operate(ctx, c, ImageRequest{Action: "remove", ID: im.ID}); e != nil {
		t.Fatal(e)
	}
}
func TestTartRefusesUnownedVMAndImageDeletionInUse(t *testing.T) {
	c, s := fixtureStore(t)
	driver, _ := fakeTart(c)
	r := Runner{ID: newID(), Handle: Handle{VM: "operator-vm"}}
	if e := os.MkdirAll(vmPath(c, r.Handle.VM), 0700); e != nil {
		t.Fatal(e)
	}
	requireCode(t, driver.Cleanup(context.Background(), c, r, s.View()), ErrOwnership)
	if _, e := os.Stat(vmPath(c, r.Handle.VM)); e != nil {
		t.Fatal("unrelated VM removed")
	}
	imID := newID()
	vm := "rm-image-" + imID
	if e := claimVM(c, vm, s.View().Installation, imID); e != nil {
		t.Fatal(e)
	}
	if e := os.MkdirAll(vmPath(c, vm), 0700); e != nil {
		t.Fatal(e)
	}
	if e := publishVMOwnerMarker(c, vm, s.View().Installation, imID); e != nil {
		t.Fatal(e)
	}
	s.Update(func(v *Snapshot) error {
		v.Images[imID] = &Image{ID: imID, VM: vm, Phase: ImageSealed}
		v.Runners["busy"] = &Runner{Image: imID, Phase: Busy}
		return nil
	})
	_, e := (&ImageManager{Store: s, Tart: driver}).Operate(context.Background(), c, ImageRequest{Action: "remove", ID: imID})
	requireCode(t, e, ErrImageInUse)
}

func TestTartRejectsChangedSealedBaseBeforeCloning(t *testing.T) {
	for _, damaged := range []string{"config.json", "nvram.bin", "disk.img", "missing-file", "missing-digest"} {
		t.Run(damaged, func(t *testing.T) {
			c, s := fixtureStore(t)
			driver, fixture := fakeTart(c)
			images := &ImageManager{Store: s, Tart: driver}
			ctx := context.Background()
			im, err := images.Operate(ctx, c, ImageRequest{Action: "create", Name: "base", IPSW: "/operator/local.ipsw", Resources: Resources{1, 512}})
			if err != nil {
				t.Fatal(err)
			}
			im, err = images.Operate(ctx, c, ImageRequest{Action: "seal", ID: im.ID, RunnerVersion: "2.337.0"})
			if err != nil {
				t.Fatal(err)
			}
			switch damaged {
			case "missing-file":
				err = os.Remove(filepath.Join(vmPath(c, im.VM), "disk.img"))
			case "missing-digest":
				err = s.Update(func(v *Snapshot) error { v.Images[im.ID].Digest = ""; return nil })
			default:
				err = os.WriteFile(filepath.Join(vmPath(c, im.VM), damaged), []byte("changed-after-sealing"), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			p := c.Pools[0]
			p.Backend, p.Image, p.RunnerPath, p.RunnerVersion = Tart, im.ID, im.RunnerPath, im.RunnerVersion
			r := Runner{ID: newID(), Backend: Tart, Image: im.ID}
			published := false
			err = driver.Prepare(ctx, c, p, r, s.View(), "unused-jit", func(Handle) error { published = true; return nil })
			requireCode(t, err, ErrImage)
			if published {
				t.Fatal("altered base reached job provisioning")
			}
			_, err = images.Operate(ctx, c, ImageRequest{Action: "create", Name: "replacement", From: im.ID, Resources: Resources{1, 512}})
			requireCode(t, err, ErrImage)
			for _, args := range fixture.commands {
				if args[0] == "clone" {
					t.Fatal("altered sealed base was cloned")
				}
			}
		})
	}
}

func TestSetupReservationsShareHostBudget(t *testing.T) {
	c, s := fixtureStore(t)
	c.Host.MaxRunners = 2
	driver, _ := fakeTart(c)
	m := &ImageManager{Store: s, Tart: driver}
	ids := []string{newID(), newID(), newID()}
	s.Update(func(v *Snapshot) error {
		for _, id := range ids {
			v.Images[id] = &Image{ID: id, Phase: ImagePreparing, Resources: Resources{1, 128}}
		}
		return nil
	})
	for _, id := range ids[:2] {
		if e := m.reserve(c, id); e != nil {
			t.Fatal(e)
		}
	}
	requireCode(t, m.reserve(c, ids[2]), ErrCapacity)
}
