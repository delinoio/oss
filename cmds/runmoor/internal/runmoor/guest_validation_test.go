package runmoor

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Run the real guest validation shell, replacing only the Tart RPC transport.
// This catches nonzero shell exits being mistaken for a guest still booting.
type guestValidationCommand struct {
	CommandExecutor
	env              []string
	calls, transient int
	output           []byte
}

func (f *guestValidationCommand) Run(ctx context.Context, _ string, args, _ []string, _ io.Reader) ([]byte, error) {
	f.calls++
	if f.calls <= f.transient {
		return nil, errors.New("private transport diagnostic")
	}
	cmd := exec.CommandContext(ctx, args[2], args[3:]...)
	cmd.Env = f.env
	b, err := cmd.Output()
	f.output = b
	return b, err
}

func TestGuestValidationRejectsInvalidImagesWithoutReadinessRetry(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix guest validation")
	}
	for _, scenario := range []string{"ready", "root", "guest agent version", "missing agent", "runner version", "missing runner", "missing directory", ".runner", ".credentials", ".credentials_rsaparams", "dirty workspace", "unreadable workspace", "transport recovery", "transport timeout"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "bin")
			if err := os.Mkdir(bin, 0700); err != nil {
				t.Fatal(err)
			}
			write := func(name, script string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+script+"\n"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			write("id", "echo 1001")
			write("tart-guest-agent", "echo "+GuestAgentVersion)
			write("Runner.Listener", "echo 2.337.0")
			path := dir
			f := &guestValidationCommand{env: []string{"PATH=" + bin + ":/usr/bin:/bin"}}
			switch scenario {
			case "root":
				write("id", "echo 0")
			case "guest agent version":
				write("tart-guest-agent", "echo 0.0.0")
			case "missing agent":
				write("tart-guest-agent", "exit 127")
			case "runner version":
				write("Runner.Listener", "echo 0.0.0")
			case "missing runner":
				if err := os.Remove(filepath.Join(bin, "Runner.Listener")); err != nil {
					t.Fatal(err)
				}
			case "missing directory":
				path = filepath.Join(dir, "missing")
			case ".runner", ".credentials", ".credentials_rsaparams":
				if err := os.WriteFile(filepath.Join(dir, scenario), []byte("private registration"), 0600); err != nil {
					t.Fatal(err)
				}
			case "dirty workspace", "unreadable workspace":
				if err := os.Mkdir(filepath.Join(dir, "_work"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "_work", "private-job-output"), nil, 0600); err != nil {
					t.Fatal(err)
				}
				if scenario == "unreadable workspace" {
					write("ls", "exit 1")
				}
			case "transport recovery":
				f.transient = 1
			case "transport timeout":
				f.transient = 100
			}
			// The validation shell launches several real processes, so correctness
			// cases need headroom on loaded hosts. Only the timeout case below
			// deliberately tests a short transport deadline.
			timeout := 15 * time.Second
			if scenario == "transport timeout" {
				timeout = 50 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			driver := &TartDriver{Exec: f}
			err := driver.guestReady(ctx, Config{}, "setup", path, "2.337.0")
			switch scenario {
			case "ready", "transport recovery":
				if err != nil {
					t.Fatal(err)
				}
			case "transport timeout":
				requireCode(t, err, ErrPreparation)
			default:
				requireCode(t, err, ErrImage)
				if f.calls != 1 || string(f.output) != "RUNMOOR_INVALID\n" {
					t.Fatal("deterministic validation failure did not return one bounded result")
				}
			}
			if err != nil && strings.Contains(err.Error(), "private") {
				t.Fatal("guest diagnostics escaped redaction")
			}
		})
	}
}
