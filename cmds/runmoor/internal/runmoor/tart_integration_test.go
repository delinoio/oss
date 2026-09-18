package runmoor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
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
	// os.Executable() is the Go test harness here, not the shipped CLI.
	// Build the real helper so this opt-in test exercises the production entrypoint.
	helper := filepath.Join(t.TempDir(), "runmoor")
	build := exec.CommandContext(ctx, "go", "build", "-o", helper, "../..")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build guest helper: %v: %s", err, output)
	}
	driver := &TartDriver{Exec: OSCommand{}, GuestExecutable: helper}
	defer func() {
		for _, image := range s.View().Images {
			cleanup, stop := context.WithTimeout(context.Background(), time.Minute)
			r := Runner{ID: image.ID, Handle: Handle{VM: image.VM}}
			_ = driver.Stop(cleanup, c, r, s.View())
			if err := driver.Cleanup(cleanup, c, r, s.View()); err != nil {
				t.Error(err)
			}
			stop()
		}
	}()
	m := &ImageManager{Store: s, Tart: driver}
	im, e := m.Operate(ctx, c, ImageRequest{Action: "create", Name: "integration", From: source, SourceHome: os.Getenv("RUNMOOR_TEST_TART_SOURCE_HOME"), Resources: Resources{2, 4096}})
	if e != nil {
		t.Fatal(e)
	}
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
	prepareErr := driver.Prepare(ctx, c, p, r, s.View(), "invalid-fixture-jit", func(h Handle) error { r.Handle = h; return nil })
	if prepareErr != nil {
		// Invalid fixture JIT may exit within the startup observation window.
		// That is a preparation failure, not evidence of a healthy idle runner.
		requireCode(t, prepareErr, ErrPreparation)
	}
	other := &TartDriver{Exec: OSCommand{}}
	observed, e := other.Inspect(ctx, c, r, s.View())
	if e != nil {
		t.Fatal("manager-restart inspection failed", e)
	}
	if prepareErr != nil && observed.ExitCode == nil {
		t.Fatal("preparation failed without confirming the expected invalid-JIT guest exit", prepareErr)
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
