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
			err := driver.guestReadyUnowned(ctx, Config{}, "setup", path, "2.337.0")
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

// Keep VM ownership checks and execute the real preparation shell. Record
// installation separately so rejected guests cannot reach directory replacement.
type preparedGuestValidationCommand struct {
	*tartFixture
	guest         *guestValidationCommand
	installations int
}

func (f *preparedGuestValidationCommand) RunPinned(ctx context.Context, name string, args, env []string, in io.Reader, dir *os.File) ([]byte, error) {
	if len(args) > 1 && args[0] == "exec" {
		if args[1] != "-i" {
			return f.guest.Run(ctx, name, args, env, in)
		}
		f.installations++
	}
	return f.tartFixture.RunPinned(ctx, name, args, env, in, dir)
}

func TestManagedTartInstallationValidatesWorkspaceBeforeMutation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix guest validation and owned VM storage")
	}
	for _, scenario := range []string{"absent workspace", "absent runner directory", "empty workspace", "dirty workspace", "failed empty listing", "failed partial listing", "root", "guest agent version", "registration", "transport recovery", "transport timeout"} {
		t.Run(scenario, func(t *testing.T) {
			c, store := fixtureStore(t)
			id := newID()
			vm := "rm-" + id
			installation := store.View().Installation
			if err := claimVM(c, vm, installation, id); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(vmPath(c, vm), 0700); err != nil {
				t.Fatal(err)
			}
			if err := publishVMOwnerMarker(c, vm, installation, id); err != nil {
				t.Fatal(err)
			}
			// Preparation rejects symlinked runner ancestors. Resolve the host's
			// temporary-directory aliases before using it as a guest fixture.
			dir, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(dir, "bin")
			path := filepath.Join(dir, "runner")
			for _, p := range []string{bin, path} {
				if err := os.Mkdir(p, 0700); err != nil {
					t.Fatal(err)
				}
			}
			write := func(name, script string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+script+"\n"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			write("id", "echo 1001")
			write("tart-guest-agent", "echo "+GuestAgentVersion)
			guest := &guestValidationCommand{env: []string{"PATH=" + bin + ":/usr/bin:/bin"}}
			switch scenario {
			case "absent runner directory":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "empty workspace", "dirty workspace", "failed empty listing", "failed partial listing":
				work := filepath.Join(path, "_work")
				if err := os.Mkdir(work, 0700); err != nil {
					t.Fatal(err)
				}
				if scenario != "empty workspace" {
					if err := os.WriteFile(filepath.Join(work, "private-job-output"), nil, 0600); err != nil {
						t.Fatal(err)
					}
				}
				if scenario == "failed empty listing" {
					write("ls", "echo private-workspace-diagnostic >&2; exit 1")
				} else if scenario == "failed partial listing" {
					write("ls", "echo private-job-output; echo private-workspace-diagnostic >&2; exit 1")
				}
			case "root":
				write("id", "echo 0")
			case "guest agent version":
				write("tart-guest-agent", "echo 0.0.0")
			case "registration":
				if err := os.WriteFile(filepath.Join(path, ".credentials"), []byte("private registration"), 0600); err != nil {
					t.Fatal(err)
				}
			case "transport recovery":
				guest.transient = 1
			case "transport timeout":
				guest.transient = 100
			}
			archive := filepath.Join(dir, "archive")
			if err := os.WriteFile(archive, []byte("fixture archive input"), 0600); err != nil {
				t.Fatal(err)
			}
			driver, fixture := fakeTart(c)
			command := &preparedGuestValidationCommand{tartFixture: fixture, guest: guest}
			driver.Exec = command
			builder := &ManagedImageBuilder{Images: &ImageManager{Store: store, Tart: driver}}
			timeout := 15 * time.Second
			if scenario == "transport timeout" {
				timeout = 50 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			err = builder.installTartArchive(ctx, c, vm, path, archive, installation, id)
			wantCalls, wantInstallations := 1, 0
			switch scenario {
			case "absent workspace", "absent runner directory", "empty workspace", "transport recovery":
				if err != nil {
					t.Fatal(err)
				}
				wantInstallations = 1
				if scenario == "transport recovery" {
					wantCalls = 2
				}
				if string(guest.output) != "RUNMOOR_READY\n" {
					t.Fatal("prepared guest did not return a bounded ready result")
				}
			case "transport timeout":
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("transport deadline result = %v", err)
				}
			default:
				requireCode(t, err, ErrImage)
				if string(guest.output) != "RUNMOOR_INVALID\n" {
					t.Fatal("rejected guest did not return a bounded invalid result")
				}
			}
			if guest.calls != wantCalls || command.installations != wantInstallations {
				t.Fatalf("validation calls = %d, installations = %d; want %d, %d", guest.calls, command.installations, wantCalls, wantInstallations)
			}
			if err != nil && strings.Contains(err.Error(), "private") {
				t.Fatal("guest diagnostics escaped redaction")
			}
		})
	}
}
