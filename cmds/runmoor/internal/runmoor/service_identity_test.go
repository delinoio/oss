package runmoor

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestServiceDefinitionsRoundTripConfigurationPaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("service definitions are supported on macOS and Linux")
	}
	root := t.TempDir()
	binary := filepath.Join(root, `runmoor bin "quoted"%$\`)
	config := root + `/folder with spaces/quote " ampersand & percent % dollar $ slash \ /config.toml`
	for _, goos := range []string{"darwin", "linux"} {
		t.Run(goos, func(t *testing.T) {
			definition, err := serviceDefinition(goos, binary, config)
			if err != nil {
				t.Fatal(err)
			}
			got, err := serviceConfigFromDefinition(goos, []byte(definition))
			if err != nil {
				t.Fatal(err)
			}
			if want := filepath.Clean(config); got != want {
				t.Fatalf("configuration path = %q, want %q", got, want)
			}
		})
	}
}

func TestServiceDefinitionsNormalizeConfigurationPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("service definitions are supported on macOS and Linux")
	}
	root := t.TempDir()
	installed := root + `/unused/../runmoor config.toml`
	wanted := filepath.Join(root, "runmoor config.toml")
	for _, goos := range []string{"darwin", "linux"} {
		t.Run(goos, func(t *testing.T) {
			definition, err := serviceDefinition(goos, filepath.Join(root, "runmoor"), installed)
			if err != nil {
				t.Fatal(err)
			}
			got, err := serviceConfigFromDefinition(goos, []byte(definition))
			if err != nil {
				t.Fatal(err)
			}
			if got != filepath.Clean(wanted) {
				t.Fatalf("configuration path = %q, want %q", got, filepath.Clean(wanted))
			}
		})
	}
}

func TestRequireServiceConfigMatchAcceptsNormalizedAbsolutePaths(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("service definitions are supported on macOS and Linux")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	if runtime.GOOS == "linux" {
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	}
	unit := servicePath()
	if err := os.MkdirAll(filepath.Dir(unit), 0700); err != nil {
		t.Fatal(err)
	}
	installed := home + `/unused/../runmoor config.toml`
	wanted := filepath.Join(home, "runmoor config.toml")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	definition, err := serviceDefinition(runtime.GOOS, binary, installed)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unit, []byte(definition), 0600); err != nil {
		t.Fatal(err)
	}
	if err := requireServiceConfigMatch(runtime.GOOS, unit, wanted); err != nil {
		t.Fatalf("equivalent normalized paths did not match: %v", err)
	}
}

func TestServiceDefinitionParsersRejectAmbiguousArguments(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("service definitions are supported on macOS and Linux")
	}
	root := t.TempDir()
	config := filepath.Join(root, "runmoor config.toml")
	for _, goos := range []string{"darwin", "linux"} {
		t.Run(goos, func(t *testing.T) {
			definition, err := serviceDefinition(goos, filepath.Join(root, "runmoor"), config)
			if err != nil {
				t.Fatal(err)
			}
			if goos == "darwin" {
				definition = strings.Replace(definition, "</array>", "<string>--config</string></array>", 1)
			} else {
				definition = strings.Replace(definition, " run --config ", " run --config --config ", 1)
			}
			if _, err := serviceConfigFromDefinition(goos, []byte(definition)); err == nil {
				t.Fatal("ambiguous configuration arguments were accepted")
			}
			definition, err = serviceDefinition(goos, filepath.Join(root, "runmoor"), config)
			if err != nil {
				t.Fatal(err)
			}
			if goos == "darwin" {
				definition = strings.Replace(definition, "</dict></plist>", "<key>Label</key><string>"+serviceLabel+"</string></dict></plist>", 1)
			} else {
				otherConfig := filepath.Join(root, "other.toml")
				before := "ExecStop=" + systemdQuote(filepath.Join(root, "runmoor")) + " stop --config " + systemdQuote(config)
				after := "ExecStop=" + systemdQuote(filepath.Join(root, "runmoor")) + " stop --config " + systemdQuote(otherConfig)
				definition = strings.Replace(definition, before, after, 1)
			}
			if _, err := serviceConfigFromDefinition(goos, []byte(definition)); err == nil {
				t.Fatal("duplicate or conflicting definition entries were accepted")
			}
		})
	}
}

type serviceCommandRecorder struct {
	calls []string
	onRun func(name string, args []string)
}

func (r *serviceCommandRecorder) Run(_ context.Context, name string, args, _ []string, _ io.Reader) ([]byte, error) {
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	if r.onRun != nil {
		r.onRun(name, args)
	}
	return nil, nil
}

func (*serviceCommandRecorder) Start(string, []string, []string) (int, error) { return 0, nil }

func TestServiceActionsRejectMismatchedConfigurationBeforeSideEffects(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("service definitions are supported on macOS and Linux")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	if runtime.GOOS == "linux" {
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	}
	unit := servicePath()
	if err := os.MkdirAll(filepath.Dir(unit), 0700); err != nil {
		t.Fatal(err)
	}
	installedPath := filepath.Join(home, "installed-A.toml")
	requestedPath := filepath.Join(home, "requested-B.toml")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	definition, err := serviceDefinition(runtime.GOOS, binary, installedPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unit, []byte(definition), 0600); err != nil {
		t.Fatal(err)
	}

	for _, action := range []string{"start", "stop", "uninstall"} {
		t.Run(action, func(t *testing.T) {
			config := fixtureConfig(t)
			commands := &serviceCommandRecorder{}
			err := Service(context.Background(), action, requestedPath, config, commands)
			requireCode(t, err, ErrConfig)
			if strings.Contains(err.Error(), installedPath) || strings.Contains(err.Error(), requestedPath) {
				t.Fatal("configuration paths leaked in the mismatch diagnostic")
			}
			if len(commands.calls) != 0 {
				t.Fatalf("OS service commands were invoked: %v", commands.calls)
			}
			if _, err := os.Stat(config.Storage.State); !os.IsNotExist(err) {
				t.Fatalf("requested configuration state was touched: %v", err)
			}
			got, err := os.ReadFile(unit)
			if err != nil || string(got) != definition {
				t.Fatalf("installed definition changed: read error %v", err)
			}
		})
	}
}

func TestServiceActionsRejectSystemdDropInsBeforeSideEffects(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("systemd service definitions are supported on Linux")
	}
	home := t.TempDir()
	configHome := filepath.Join(home, "config")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_CONFIG_DIRS", filepath.Join(home, "xdg-config"))
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(home, "runtime"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_DATA_DIRS", filepath.Join(home, "xdg-data"))
	unit := servicePath()
	if err := os.MkdirAll(filepath.Dir(unit), 0700); err != nil {
		t.Fatal(err)
	}
	installedPath := filepath.Join(home, "installed.toml")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	definition, err := serviceDefinition(runtime.GOOS, binary, installedPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unit, []byte(definition), 0600); err != nil {
		t.Fatal(err)
	}
	dropIn := filepath.Join(configHome, "systemd", "user", "runmoor.service.d")
	if err := os.MkdirAll(dropIn, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dropIn, "override.conf"), []byte("[Service]\nExecStart=/tmp/other-runmoor\n"), 0600); err != nil {
		t.Fatal(err)
	}

	for _, action := range []string{"start", "stop", "uninstall"} {
		t.Run(action, func(t *testing.T) {
			config := fixtureConfig(t)
			commands := &serviceCommandRecorder{}
			err := Service(context.Background(), action, installedPath, config, commands)
			requireCode(t, err, ErrConfig)
			if len(commands.calls) != 0 {
				t.Fatalf("OS service commands were invoked: %v", commands.calls)
			}
			if _, err := os.Stat(config.Storage.State); !os.IsNotExist(err) {
				t.Fatalf("requested configuration state was touched: %v", err)
			}
			got, err := os.ReadFile(unit)
			if err != nil || string(got) != definition {
				t.Fatalf("installed definition changed: read error %v", err)
			}
		})
	}
}

func TestServiceActionsDoNotContactDifferentRunningManager(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("service definitions are supported on macOS and Linux")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	if runtime.GOOS == "linux" {
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	}
	unit := servicePath()
	if err := os.MkdirAll(filepath.Dir(unit), 0700); err != nil {
		t.Fatal(err)
	}
	installedPath := filepath.Join(home, "installed-A.toml")
	requestedPath := filepath.Join(home, "running-B.toml")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	definition, err := serviceDefinition(runtime.GOOS, binary, installedPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unit, []byte(definition), 0600); err != nil {
		t.Fatal(err)
	}
	manager, config, _, _, _ := testManager(t)
	server, err := manager.ServeControl()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	for _, action := range []string{"stop", "uninstall", "start"} {
		t.Run(action, func(t *testing.T) {
			commands := &serviceCommandRecorder{}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			requireCode(t, Service(ctx, action, requestedPath, config, commands), ErrConfig)
			if manager.Store.View().Stopping {
				t.Fatal("mismatched service action reached the running manager")
			}
			if len(commands.calls) != 0 {
				t.Fatalf("OS service commands were invoked: %v", commands.calls)
			}
		})
	}
}

func TestMatchingServiceUninstallWaitsForJobsAndImages(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("service definitions are supported on macOS and Linux")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	if runtime.GOOS == "linux" {
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	}
	unit := servicePath()
	if err := os.MkdirAll(filepath.Dir(unit), 0700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(home, "installed-A.toml")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	definition, err := serviceDefinition(runtime.GOOS, binary, configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unit, []byte(definition), 0600); err != nil {
		t.Fatal(err)
	}
	manager, config, _, _, pool := testManager(t)
	runner := seedRunner(t, manager, pool, Busy)
	if err := manager.Store.Update(func(snapshot *Snapshot) error {
		snapshot.Images["open-image"] = &Image{ID: "open-image", Phase: ImageOpen}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	server, err := manager.ServeControl()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	commands := &serviceCommandRecorder{}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	err = Service(ctx, "uninstall", configPath, config, commands)
	cancel()
	requireCode(t, err, ErrControl)
	if len(commands.calls) != 0 {
		t.Fatalf("service unloaded before work drained: %v", commands.calls)
	}
	if _, err := os.Stat(unit); err != nil {
		t.Fatalf("service definition was not preserved during drain: %v", err)
	}
	if err := manager.Store.Update(func(snapshot *Snapshot) error {
		snapshot.Runners[runner].Phase = Completed
		snapshot.Runners[runner].Terminated = true
		snapshot.Runners[runner].LocalCleaned = true
		delete(snapshot.Images, "open-image")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := Service(context.Background(), "uninstall", configPath, config, commands); err != nil {
		t.Fatalf("uninstall after work drained failed: %v", err)
	}
	if _, err := os.Stat(unit); !os.IsNotExist(err) {
		t.Fatal("drained service definition was not removed")
	}
	if len(commands.calls) == 0 {
		t.Fatal("matching service was not unloaded after work drained")
	}
}

func TestServiceUninstallRevalidatesDefinitionAfterDrain(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("service definitions are supported on macOS and Linux")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	if runtime.GOOS == "linux" {
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
		t.Setenv("XDG_CONFIG_DIRS", filepath.Join(home, "xdg-config"))
		t.Setenv("XDG_RUNTIME_DIR", filepath.Join(home, "runtime"))
		t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
		t.Setenv("XDG_DATA_DIRS", filepath.Join(home, "xdg-data"))
	}
	unit := servicePath()
	if err := os.MkdirAll(filepath.Dir(unit), 0700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(home, "installed.toml")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	definition, err := serviceDefinition(runtime.GOOS, binary, configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unit, []byte(definition), 0600); err != nil {
		t.Fatal(err)
	}
	manager, config, _, _, pool := testManager(t)
	runner := seedRunner(t, manager, pool, Busy)
	server, err := manager.ServeControl()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	commands := &serviceCommandRecorder{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- Service(ctx, "uninstall", configPath, config, commands) }()

	deadline := time.Now().Add(time.Second)
	for !manager.Store.View().Stopping && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !manager.Store.View().Stopping {
		t.Fatal("uninstall did not request the manager to drain")
	}
	replacement, err := serviceDefinition(runtime.GOOS, filepath.Join(home, "newer-runmoor"), configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unit, []byte(replacement), 0600); err != nil {
		t.Fatal(err)
	}
	if err := manager.Store.Update(func(snapshot *Snapshot) error {
		snapshot.Runners[runner].Phase = Completed
		snapshot.Runners[runner].Terminated = true
		snapshot.Runners[runner].LocalCleaned = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		requireCode(t, err, ErrConfig)
	case <-ctx.Done():
		t.Fatal("uninstall did not finish after the manager drain")
	}
	if len(commands.calls) != 0 {
		t.Fatalf("service manager was contacted after the definition changed: %v", commands.calls)
	}
	got, err := os.ReadFile(unit)
	if err != nil || string(got) != replacement {
		t.Fatalf("new service definition was not preserved: read error %v", err)
	}
}

func TestServiceUninstallPreservesDefinitionReplacedDuringUnload(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("service definitions are supported on macOS and Linux")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	if runtime.GOOS == "linux" {
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
		t.Setenv("XDG_CONFIG_DIRS", filepath.Join(home, "xdg-config"))
		t.Setenv("XDG_RUNTIME_DIR", filepath.Join(home, "runtime"))
		t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
		t.Setenv("XDG_DATA_DIRS", filepath.Join(home, "xdg-data"))
	}
	unit := servicePath()
	if err := os.MkdirAll(filepath.Dir(unit), 0700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(home, "installed.toml")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	definition, err := serviceDefinition(runtime.GOOS, binary, configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unit, []byte(definition), 0600); err != nil {
		t.Fatal(err)
	}
	replacement, err := serviceDefinition(runtime.GOOS, filepath.Join(home, "newer-runmoor"), configPath)
	if err != nil {
		t.Fatal(err)
	}
	manager, config, _, _, _ := testManager(t)
	server, err := manager.ServeControl()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	commands := &serviceCommandRecorder{onRun: func(_ string, _ []string) {
		if err := os.WriteFile(unit, []byte(replacement), 0600); err != nil {
			t.Errorf("replace service definition during unload: %v", err)
		}
	}}
	err = Service(context.Background(), "uninstall", configPath, config, commands)
	requireCode(t, err, ErrConfig)
	if len(commands.calls) == 0 {
		t.Fatal("service unload was not reached")
	}
	got, err := os.ReadFile(unit)
	if err != nil || string(got) != replacement {
		t.Fatalf("new service definition was not preserved: read error %v", err)
	}
}

func TestServiceDefinitionMustBePrivateAndUnambiguous(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("service definitions are supported on macOS and Linux")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	if runtime.GOOS == "linux" {
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	}
	unit := servicePath()
	if err := os.MkdirAll(filepath.Dir(unit), 0700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(home, "config.toml")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	definition, err := serviceDefinition(runtime.GOOS, binary, configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := requireServiceConfigMatch(runtime.GOOS, unit, configPath); err == nil {
		t.Fatal("missing service definition was accepted")
	} else {
		requireCode(t, err, ErrConfig)
	}
	if err := os.WriteFile(unit, []byte("not a service definition"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := requireServiceConfigMatch(runtime.GOOS, unit, configPath); err == nil {
		t.Fatal("malformed service definition was accepted")
	} else {
		requireCode(t, err, ErrConfig)
	}
	config := fixtureConfig(t)
	commands := &serviceCommandRecorder{}
	requireCode(t, Service(context.Background(), "uninstall", configPath, config, commands), ErrConfig)
	if len(commands.calls) != 0 {
		t.Fatalf("malformed definition invoked OS service commands: %v", commands.calls)
	}
	if _, err := os.Stat(config.Storage.State); !os.IsNotExist(err) {
		t.Fatalf("malformed definition touched configuration state: %v", err)
	}
	if got, err := os.ReadFile(unit); err != nil || string(got) != "not a service definition" {
		t.Fatalf("malformed definition was changed: %v", err)
	}
	if err := os.WriteFile(unit, []byte(definition), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, "service-link")
	if err := os.Symlink(unit, link); err != nil {
		t.Fatal(err)
	}
	if err := requireServiceConfigMatch(runtime.GOOS, link, configPath); err == nil {
		t.Fatal("symlinked service definition was accepted")
	} else {
		requireCode(t, err, ErrConfig)
	}
	if err := os.Chown(unit, os.Geteuid()+1, -1); err == nil {
		if err := requireServiceConfigMatch(runtime.GOOS, unit, configPath); err == nil {
			t.Fatal("foreign-owned service definition was accepted")
		} else {
			requireCode(t, err, ErrConfig)
		}
	}
}
