package process

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func init() {
	if len(os.Args) != 3 || os.Args[1] != "__delidev_process_fixture" {
		return
	}
	switch os.Args[2] {
	case "streams":
		raw, err := io.ReadAll(os.Stdin)
		if err != nil {
			os.Exit(9)
		}
		_, _ = os.Stdout.Write(raw)
		_, _ = os.Stderr.Write([]byte("separate diagnostic"))
		os.Exit(7)
	case "output":
		for {
			if _, err := os.Stdout.Write(make([]byte, 32768)); err != nil {
				os.Exit(0)
			}
		}
	case "sleep":
		for {
			time.Sleep(time.Second)
		}
	}
	os.Exit(2)
}
func config(t *testing.T, mode string) Config {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Config{Directory: filepath.Join(t.TempDir(), "processes"), OwnerID: domain.NewID(), Executable: executable, Args: []string{"__delidev_process_fixture", mode}, Env: []string{"PATH=/usr/bin:/bin", "DELIDEV_TEST_SECRET=not-for-journals"}, Cwd: t.TempDir()}
}
func TestStartBarrierAndSeparateInteractiveStreams(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var stdout, stderr bytes.Buffer
	c := config(t, "streams")
	c.Stdout = &stdout
	c.Stderr = &stderr
	h, err := Start(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if h.Identity().OwnerID != c.OwnerID || h.Identity().Birth == "" {
		t.Fatal("missing execution/start identity")
	}
	select {
	case <-h.Done():
		t.Fatal("process ran before resume")
	default:
	}
	if err := h.Resume(); err != nil {
		t.Fatal(err)
	}
	if err := h.Resume(); err == nil {
		t.Fatal("start barrier released twice")
	}
	raw := []byte("partial: 한글 / 😀\n")
	for _, value := range raw {
		if _, err := h.Write([]byte{value}); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.CloseInput(); err != nil {
		t.Fatal(err)
	}
	err = h.Wait()
	exit, ok := err.(interface{ ExitCode() int })
	if !ok || exit.ExitCode() != 7 {
		t.Fatalf("wrong exit: %v", err)
	}
	if !bytes.Equal(stdout.Bytes(), raw) || stderr.String() != "separate diagnostic" {
		t.Fatalf("streams mixed: %q %q", stdout.String(), stderr.String())
	}
	if err := h.Stop(); err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir(c.Directory, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, secret := range [][]byte{[]byte("not-for-journals"), []byte("partial:"), []byte("separate diagnostic")} {
			if bytes.Contains(raw, secret) {
				t.Fatal("ownership journal retained process content")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestCancellationAndForeignOwnerRecovery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := config(t, "sleep")
	h, err := Start(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if err := h.Resume(); err != nil {
		t.Fatal(err)
	}
	foreign := h.Identity()
	foreign.OwnerID = domain.NewID()
	if err := ReconcileProcess(foreign); err == nil {
		t.Fatal("foreign execution ownership accepted")
	}
	if !ProcessAlive(h.Identity()) {
		t.Fatal("foreign recovery killed process")
	}
	cancel()
	select {
	case <-h.Done():
	case <-time.After(15 * time.Second):
		t.Fatal("canceled child did not stop")
	}
	if err := h.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := ReconcileProcess(h.Identity()); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(h.Identity().ScopeDir, "ownership.json"))
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}
	if record["complete"] != true {
		t.Fatal("termination lacked durable completion proof")
	}
}

type rejectedOutput struct{}

func (rejectedOutput) Write([]byte) (int, error) { return 0, io.ErrShortBuffer }
func TestOutputFailureRequiresReconciliation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c := config(t, "output")
	c.Stdout = rejectedOutput{}
	h, err := Start(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if err := h.Resume(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-h.Done():
	case <-ctx.Done():
		t.Fatal("output failure left the process unobserved")
	}
	if h.Wait() == nil {
		t.Fatal("dropped native output reported success")
	}
	if err := ReconcileProcess(h.Identity()); err != nil {
		t.Fatal(err)
	}
	if err := ReconcileOwner(c.Directory, c.OwnerID); err != nil {
		t.Fatal(err)
	}
}
func TestStopBeforeResumeHasNoNativeExecution(t *testing.T) {
	c := config(t, "sleep")
	h, err := Start(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if err := h.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := h.Resume(); err == nil {
		t.Fatal("stopped process crossed the start barrier")
	}
	if err := ReconcileOwner(c.Directory, c.OwnerID); err != nil {
		t.Fatal(err)
	}
}
