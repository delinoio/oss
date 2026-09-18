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
