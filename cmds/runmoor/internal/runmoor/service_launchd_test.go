package runmoor

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

type serviceExit int

func (e serviceExit) Error() string { return "private launchd diagnostic" }
func (e serviceExit) ExitCode() int { return int(e) }

type launchdUnloadFixture struct {
	CommandExecutor
	bootout, service, domain error
	commands                 []string
}

func (f *launchdUnloadFixture) Run(_ context.Context, name string, args, _ []string, _ io.Reader) ([]byte, error) {
	f.commands = append(f.commands, name+" "+strings.Join(args, " "))
	if len(args) == 2 && args[0] == "list" && args[1] == serviceLabel {
		return nil, serviceExit(113)
	}
	if args[0] == "bootout" {
		return nil, f.bootout
	}
	if strings.HasSuffix(args[1], "/"+serviceLabel) {
		return nil, f.service
	}
	return nil, f.domain
}

type launchdIdentityFixture struct {
	pid      int
	inactive bool
	commands []string
}

func (f *launchdIdentityFixture) Run(_ context.Context, name string, args, _ []string, _ io.Reader) ([]byte, error) {
	f.commands = append(f.commands, name+" "+strings.Join(args, " "))
	if name == "launchctl" && len(args) == 2 && args[0] == "list" && args[1] == serviceLabel {
		if f.inactive {
			return []byte("-\t0\t" + serviceLabel + "\n"), nil
		}
		return []byte(strconv.Itoa(f.pid) + "\t0\t" + serviceLabel + "\n"), nil
	}
	return nil, errors.New("unexpected launchd identity command")
}

func (*launchdIdentityFixture) Start(string, []string, []string) (int, error) {
	return 0, errors.New("unexpected service spawn")
}

func TestParseLaunchdProcessArguments(t *testing.T) {
	data := make([]byte, 4)
	binary.NativeEndian.PutUint32(data, 4)
	data = append(data, []byte("/fixture/runmoor\x00\x00/fixture/runmoor\x00run\x00--config\x00/fixture/config.toml\x00")...)
	want := []string{"/fixture/runmoor", "run", "--config", "/fixture/config.toml"}
	got, err := parseLaunchdProcessArguments(data)
	if err != nil || !sameServiceArguments(got, want) {
		t.Fatalf("arguments = %q, error = %v", got, err)
	}
}

func TestLaunchdActionsRejectAnActiveManagerWithDifferentArguments(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("launchd service")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	unit := servicePath()
	if err := os.MkdirAll(filepath.Dir(unit), 0700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(home, "config.toml")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	definition, err := serviceDefinition("darwin", binary, configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unit, []byte(definition), 0600); err != nil {
		t.Fatal(err)
	}
	c := fixtureConfig(t)
	for _, action := range []string{"stop", "uninstall"} {
		t.Run(action, func(t *testing.T) {
			fixture := &launchdIdentityFixture{pid: os.Getpid()}
			err := Service(context.Background(), action, configPath, c, fixture)
			requireCode(t, err, ErrConfig)
			if len(fixture.commands) != 1 || fixture.commands[0] != "launchctl list "+serviceLabel {
				t.Fatalf("mismatched launchd service reached a later command: %v", fixture.commands)
			}
			if _, err := os.Stat(unit); err != nil {
				t.Fatalf("mismatched launchd service definition was removed: %v", err)
			}
		})
	}
}

func TestLaunchdActionsRejectAnInactiveLoadedJob(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("launchd service")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	unit := servicePath()
	if err := os.MkdirAll(filepath.Dir(unit), 0700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(home, "config.toml")
	definition, err := serviceDefinition("darwin", "/fixture/runmoor", configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unit, []byte(definition), 0600); err != nil {
		t.Fatal(err)
	}
	c := fixtureConfig(t)
	for _, action := range []string{"stop", "uninstall"} {
		t.Run(action, func(t *testing.T) {
			fixture := &launchdIdentityFixture{inactive: true}
			err := Service(context.Background(), action, configPath, c, fixture)
			requireCode(t, err, ErrDependency)
			if len(fixture.commands) != 1 || fixture.commands[0] != "launchctl list "+serviceLabel {
				t.Fatalf("inactive loaded launchd service reached a later command: %v", fixture.commands)
			}
			if _, err := os.Stat(unit); err != nil {
				t.Fatalf("inactive loaded launchd service definition was removed: %v", err)
			}
		})
	}
}

func TestLaunchdProcessCommandLineUsesKernelArguments(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("launchd process inspection is available on macOS")
	}
	args, err := launchdProcessCommandLine(os.Getpid())
	if err != nil {
		t.Fatal("could not inspect current process arguments")
	}
	if len(args) == 0 || args[0] != os.Args[0] {
		t.Fatalf("process argv[0] = %q, want %q", args, os.Args[0])
	}
}

func TestLaunchdUnloadRequiresConfirmedAbsence(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		bootout, service, domain error
		wantError                bool
		calls                    int
	}{
		{"unloaded", nil, nil, nil, false, 1},
		{"already absent", serviceExit(5), serviceExit(113), nil, false, 3},
		{"still loaded", serviceExit(5), nil, nil, true, 2},
		{"permission denied", serviceExit(1), serviceExit(1), nil, true, 2},
		{"unknown probe failure", serviceExit(5), errors.New("private transport failure"), nil, true, 2},
		{"domain unavailable", serviceExit(5), serviceExit(113), serviceExit(125), true, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &launchdUnloadFixture{bootout: tc.bootout, service: tc.service, domain: tc.domain}
			err := unloadLaunchd(context.Background(), "gui/1001", "/fixture/runmoor.plist", f)
			if tc.wantError {
				requireCode(t, err, ErrDependency)
				if strings.Contains(err.Error(), "private") {
					t.Fatal("raw launchd diagnostic leaked")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if len(f.commands) != tc.calls || (tc.calls > 1 && f.commands[1] != "launchctl print gui/1001/"+serviceLabel) || (tc.calls == 3 && f.commands[2] != "launchctl print gui/1001") {
				t.Fatal("absence was not confirmed in the exact domain", f.commands)
			}
		})
	}
}

func TestLaunchdUninstallPreservesDefinitionUntilUnloaded(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("launchd service path")
	}
	c, store := fixtureStore(t)
	store.Close()
	t.Setenv("HOME", filepath.Dir(c.Storage.State))
	unit := servicePath()
	if err := os.MkdirAll(filepath.Dir(unit), 0700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(filepath.Dir(c.Storage.State), "installed.toml")
	definition, err := serviceDefinition("darwin", "/fixture/runmoor", configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unit, []byte(definition), 0600); err != nil {
		t.Fatal(err)
	}
	f := &launchdUnloadFixture{bootout: serviceExit(5)}
	for _, action := range []string{"stop", "uninstall"} {
		requireCode(t, Service(context.Background(), action, configPath, c, f), ErrDependency)
		if data, err := os.ReadFile(unit); err != nil || string(data) != definition {
			t.Fatal("failed unload removed the service definition")
		}
	}
	f.service = serviceExit(113)
	if err := Service(context.Background(), "uninstall", configPath, c, f); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(unit); !os.IsNotExist(err) {
		t.Fatal("confirmed absence did not allow uninstall")
	}
}

type launchdStartFixture struct {
	results  []error
	commands []string
}

func (f *launchdStartFixture) Run(_ context.Context, name string, args, _ []string, _ io.Reader) ([]byte, error) {
	f.commands = append(f.commands, name+" "+strings.Join(args, " "))
	index := len(f.commands) - 1
	if index < len(f.results) {
		return nil, f.results[index]
	}
	return nil, nil
}

func (*launchdStartFixture) Start(string, []string, []string) (int, error) { return 0, nil }

func TestLaunchdStartReloadsValidatedDefinitionBeforeStarting(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("launchd service")
	}
	for _, tc := range []struct {
		name         string
		results      []error
		loaded       bool
		bootoutFails bool
		wantErr      bool
	}{
		{
			name:    "loaded job is booted out and reloaded from disk",
			results: []error{nil, nil, nil},
			loaded:  true,
		},
		{
			name:    "unloaded job bootstraps validated definition",
			results: []error{serviceExit(113), nil},
		},
		{
			name:         "failed bootout does not bootstrap",
			results:      []error{nil, serviceExit(5)},
			loaded:       true,
			bootoutFails: true,
			wantErr:      true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			unit := servicePath()
			if err := os.MkdirAll(filepath.Dir(unit), 0700); err != nil {
				t.Fatal(err)
			}
			configPath := filepath.Join(home, "config.toml")
			definition, err := serviceDefinition("darwin", filepath.Join(home, "runmoor"), configPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(unit, []byte(definition), 0600); err != nil {
				t.Fatal(err)
			}

			commands := &launchdStartFixture{results: tc.results}
			err = Service(context.Background(), "start", configPath, Config{}, commands)
			if tc.wantErr {
				requireCode(t, err, ErrDependency)
			} else if err != nil {
				t.Fatal(err)
			}
			got := append([]string(nil), commands.commands...)
			domain := "gui/" + strconv.Itoa(os.Getuid())
			want := []string{"launchctl print " + domain + "/" + serviceLabel}
			if tc.loaded {
				want = append(want, "launchctl bootout "+domain+" "+unit)
			}
			if !tc.bootoutFails {
				want = append(want, "launchctl bootstrap "+domain+" "+unit)
			}
			if strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Fatalf("launchctl commands = %v, want %v", got, want)
			}
		})
	}
}
