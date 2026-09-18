package runmoor

import (
	"context"
	"os"
	"runtime"
	"testing"
	"time"
)

// This opt-in test uses an operator-provided clean image. It exercises actual
// Tart/Guest Agent execution with intentionally invalid JIT, never GitHub job
// dispatch. Nothing modifies or deletes the source image or its Tart home.
func TestTartIntegration(t *testing.T) {
	if os.Getenv("RUNMOOR_TART_TEST") != "1" {
		t.Skip("explicit opt-in required for real Tart execution")
	}
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Fatal("Tart requires macOS arm64")
	}
	source := os.Getenv("RUNMOOR_TEST_TART_SOURCE")
	version := os.Getenv("RUNMOOR_TEST_TART_RUNNER_VERSION")
	if source == "" || version == "" {
		t.Fatal("provide RUNMOOR_TEST_TART_SOURCE and RUNMOOR_TEST_TART_RUNNER_VERSION for a clean prepared source")
	}
	c, s := fixtureStore(t)
	c.Host.MemoryMiB = 8192
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	driver := &TartDriver{Exec: OSCommand{}}
	m := &ImageManager{Store: s, Tart: driver}
	im, e := m.Operate(ctx, c, ImageRequest{Action: "create", Name: "integration", From: source, SourceHome: os.Getenv("RUNMOOR_TEST_TART_SOURCE_HOME"), Resources: Resources{2, 4096}})
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), time.Minute)
		defer stop()
		r := Runner{ID: im.ID, Handle: Handle{VM: im.VM}}
		_ = driver.Stop(cleanup, c, r, s.View())
		_ = driver.Cleanup(cleanup, c, r, s.View())
	}()
	im, e = m.Operate(ctx, c, ImageRequest{Action: "seal", ID: im.ID, RunnerVersion: version, RunnerPath: os.Getenv("RUNMOOR_TEST_TART_RUNNER_PATH")})
	if e != nil {
		t.Fatal(e)
	}
	p := c.Pools[0]
	p.Backend = Tart
	p.Image = im.ID
	p.RunnerVersion = version
	p.RunnerPath = im.RunnerPath
	p.Resources = Resources{2, 4096}
	r := Runner{ID: newID(), Backend: Tart, Image: im.ID, Phase: Preparing}
	r.Name = "runmoor-" + r.ID
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), time.Minute)
		defer stop()
		_ = driver.Stop(cleanup, c, r, s.View())
		if e := driver.Cleanup(cleanup, c, r, s.View()); e != nil {
			t.Error(e)
		}
	}()
	if e = driver.Prepare(ctx, c, p, r, s.View(), "invalid-fixture-jit", func(h Handle) error { r.Handle = h; return nil }); e != nil {
		t.Fatal(e)
	}
	other := &TartDriver{Exec: OSCommand{}}
	if _, e = other.Inspect(ctx, c, r, s.View()); e != nil {
		t.Fatal("manager-restart inspection failed", e)
	}
	if e = other.Stop(ctx, c, r, s.View()); e != nil {
		t.Fatal(e)
	}
	if e = other.Cleanup(ctx, c, r, s.View()); e != nil {
		t.Fatal(e)
	}
	digest, e := imageDigest(c, im.VM)
	if e != nil || digest != im.Digest {
		t.Fatal("sealed base changed during clone execution")
	}
}
