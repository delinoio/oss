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
	mu       sync.Mutex
	c        Config
	running  map[string]bool
	commands [][]string
	jit      string
}

func (f *tartFixture) Run(_ context.Context, name string, args, env []string, in io.Reader) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands, append([]string{}, args...))
	if name == "/usr/bin/sw_vers" {
		return []byte("14.7"), nil
	}
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "TART_NO_AUTO_PRUNE=1") || !strings.Contains(joined, "TART_HOME="+filepath.Join(f.c.Storage.Data, "tart")) {
		return nil, problem(ErrConfig, "Unsafe Tart environment.", "Fix the test boundary.")
	}
	for _, v := range env {
		if strings.HasPrefix(v, "RUNMOOR_TEST_CREDENTIAL=") {
			return nil, problem(ErrAuth, "Credential leaked to Tart.", "Remove it.")
		}
	}
	switch args[0] {
	case "--version":
		return []byte(TartVersion), nil
	case "create", "clone", "import":
		vm := args[len(args)-1]
		dir := vmPath(f.c, vm)
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
		return json.Marshal(vmInfo{Running: f.running[args[1]], State: "stopped", OS: "darwin", CPU: 1, Memory: 512})
	case "set":
	case "stop":
		f.running[args[1]] = false
	case "delete":
		delete(f.running, args[1])
		return nil, os.RemoveAll(vmPath(f.c, args[1]))
	case "exec":
		if strings.Contains(strings.Join(args, " "), "__guest-bootstrap") {
			b, _ := io.ReadAll(in)
			var v GuestInput
			if e := json.Unmarshal(b, &v); e != nil {
				return nil, e
			}
			f.jit = v.JIT
		} else if in != nil {
			io.Copy(io.Discard, in)
		} else {
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
	return 123, nil
}
func fakeTart(c Config) (*TartDriver, *tartFixture) {
	f := &tartFixture{c: c, running: map[string]bool{}}
	return &TartDriver{Exec: f, HostCheck: func(context.Context) error { return nil }}, f
}

type startingTartCommand struct {
	*tartFixture
	started           bool
	polls, readyAfter int
}

func (f *startingTartCommand) Start(string, []string, []string) (int, error) {
	f.started = true
	return 123, nil
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
	if e = driver.Prepare(ctx, c, p, r, snap, "fixture-jit", func(h Handle) error { r.Handle = h; return nil }); e != nil {
		t.Fatal(e)
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
	digest, e := imageDigest(c, im.VM)
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
