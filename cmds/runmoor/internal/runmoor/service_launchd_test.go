package runmoor

import (
	"context"
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
	if args[0] == "bootout" {
		return nil, f.bootout
	}
	if strings.HasSuffix(args[1], "/"+serviceLabel) {
		return nil, f.service
	}
	return nil, f.domain
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
