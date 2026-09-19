package runmoor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type cancelDigestSource struct {
	*strings.Reader // Also implements WriterTo, which must not bypass Read.
	interrupt       func()
	read            int
}

func (r *cancelDigestSource) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.read += n
	r.interrupt()
	return n, err
}

func TestImageHashStopsReadingOnCancellation(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "deadline"}[deadline], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			interrupt := cancel
			want := context.Canceled
			if deadline {
				ctx, cancel = context.WithTimeout(context.Background(), 10*time.Millisecond)
				defer cancel()
				interrupt = func() { <-ctx.Done() }
				want = context.DeadlineExceeded
			}
			src := &cancelDigestSource{Reader: strings.NewReader(strings.Repeat("disk", 1<<20)), interrupt: interrupt}
			_, err := io.CopyBuffer(sha256.New(), imageDigestReader{ctx: ctx, src: src}, make([]byte, 64<<10))
			if !errors.Is(err, want) {
				t.Fatalf("hash did not report cancellation: %v", err)
			}
			if src.read > 64<<10 || src.Len() == 0 {
				t.Fatalf("hash continued beyond a bounded read after cancellation: %d", src.read)
			}
		})
	}
}

func TestImageDigestPreservesFormatAndRejectsCancelledResults(t *testing.T) {
	c := fixtureConfig(t)
	vm := "digest-fixture"
	if err := os.MkdirAll(vmPath(c, vm), 0700); err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	for _, name := range []string{"config.json", "nvram.bin", "disk.img"} {
		contents := strings.Repeat(name, 10000)
		if err := os.WriteFile(filepath.Join(vmPath(c, vm), name), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(h, contents)
	}
	digest, err := imageDigest(context.Background(), c, vm)
	if err != nil || digest != hex.EncodeToString(h.Sum(nil)) {
		t.Fatal("sealed image digest compatibility changed", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if digest, err = imageDigest(ctx, c, vm); digest != "" || !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled hashing published a digest", digest, err)
	}
	ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if digest, err = imageDigest(ctx, c, vm); digest != "" || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("expired hashing published a digest", digest, err)
	}
}

type cancelBeforeDigestCommand struct {
	CommandExecutor
	cancel context.CancelFunc
}

func (c cancelBeforeDigestCommand) Run(ctx context.Context, name string, args, env []string, in io.Reader) ([]byte, error) {
	out, err := c.CommandExecutor.Run(ctx, name, args, env, in)
	if len(args) > 0 && args[0] == "get" {
		c.cancel()
	}
	return out, err
}

func TestTartPreparationCancelsBeforeDigestAndClone(t *testing.T) {
	c, s := fixtureStore(t)
	driver, fixture := fakeTart(c)
	images := &ImageManager{Store: s, Tart: driver}
	im, err := images.Operate(context.Background(), c, ImageRequest{Action: "create", Name: "base", IPSW: "/operator/local.ipsw", Resources: Resources{1, 512}})
	if err != nil {
		t.Fatal(err)
	}
	im, err = images.Operate(context.Background(), c, ImageRequest{Action: "seal", ID: im.ID, RunnerVersion: "2.337.0"})
	if err != nil {
		t.Fatal(err)
	}
	p := c.Pools[0]
	p.Backend, p.Image, p.RunnerPath, p.RunnerVersion = Tart, im.ID, im.RunnerPath, im.RunnerVersion
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	driver.Exec = cancelBeforeDigestCommand{CommandExecutor: fixture, cancel: cancel}
	err = driver.Prepare(ctx, c, p, Runner{ID: newID(), Backend: Tart, Image: im.ID}, s.View(), "unused-jit", func(Handle) error {
		t.Error("cancelled verification reached job provisioning")
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal("preparation did not propagate digest cancellation", err)
	}
	for _, args := range fixture.commands {
		if args[0] == "clone" {
			t.Fatal("cancelled verification cloned the sealed base")
		}
	}
	if got := s.View().Images[im.ID]; got.Phase != ImageSealed || got.Digest != im.Digest {
		t.Fatal("cancelled verification changed the sealed revision")
	}
}
