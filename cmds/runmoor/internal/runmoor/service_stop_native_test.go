//go:build darwin || linux

package runmoor

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The bounded sleeper proves kernel argv and start identity only. It never
// installs a service, contacts an account, or executes a runner job.
func init() {
	if os.Getenv("RUNMOOR_SERVICE_STOP_PROCESS_FIXTURE") != "1" {
		return
	}
	ready := os.NewFile(3, "service-stop-fixture")
	ready.Write([]byte("ready"))
	ready.Close()
	time.Sleep(30 * time.Second)
	os.Exit(0)
}

type serviceStopNativeFixture struct {
	pid      int
	commands []string
}

func (f *serviceStopNativeFixture) Run(_ context.Context, name string, args, _ []string, _ io.Reader) ([]byte, error) {
	f.commands = append(f.commands, name+" "+strings.Join(args, " "))
	if name == "launchctl" && len(args) == 1 && args[0] == "list" {
		return []byte("PID\tStatus\tLabel\n" + strconv.Itoa(f.pid) + "\t0\t" + serviceLabel + "\n"), nil
	}
	if name == "systemctl" && len(args) > 1 && args[1] == "show" {
		return []byte(strconv.Itoa(f.pid)), nil
	}
	return nil, nil
}
func (*serviceStopNativeFixture) Start(string, []string, []string) (int, error) { return 0, nil }

func TestServiceStopDoesNotAdoptCandidateStorage(t *testing.T) {
	for _, scenario := range []string{"unreachable candidate", "foreground candidate peer"} {
		t.Run(scenario, func(t *testing.T) {
			original, c, _, _, pool := testManager(t)
			runner := seedRunner(t, original, pool, Busy)
			before := original.Store.View().Runners[runner]
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", home)
			configPath := filepath.Join(home, "config.toml")
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ready, write, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			sleeper := exec.Command(binary, "run", "--config", configPath)
			sleeper.Env = append(minimalEnv(), "RUNMOOR_SERVICE_STOP_PROCESS_FIXTURE=1")
			sleeper.ExtraFiles = []*os.File{write}
			if err := sleeper.Start(); err != nil {
				t.Fatal(err)
			}
			write.Close()
			t.Cleanup(func() { sleeper.Process.Kill(); sleeper.Wait(); ready.Close() })
			ready.SetReadDeadline(time.Now().Add(5 * time.Second))
			if _, err := io.ReadFull(ready, make([]byte, 5)); err != nil {
				t.Fatal(err)
			}
			unit := servicePath()
			if err := os.MkdirAll(filepath.Dir(unit), 0700); err != nil {
				t.Fatal(err)
			}
			definition, err := serviceDefinition(runtime.GOOS, binary, configPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(unit, []byte(definition), 0600); err != nil {
				t.Fatal(err)
			}
			candidate := c
			candidate.Storage.State = filepath.Join(home, "candidate-state")
			candidate.Storage.Data = filepath.Join(home, "candidate-data")
			var foreground *Manager
			if scenario == "foreground candidate peer" {
				foreground, candidate, _, _, _ = testManager(t)
				server, err := foreground.ServeControl()
				if err != nil {
					t.Fatal(err)
				}
				defer server.Close()
			}
			for _, action := range []string{"stop", "uninstall"} {
				fixture := &serviceStopNativeFixture{pid: sleeper.Process.Pid}
				requireCode(t, Service(context.Background(), action, configPath, candidate, fixture), ErrControl)
				for _, command := range fixture.commands {
					if strings.Contains(command, "bootout") || strings.Contains(command, "disable") || strings.Contains(command, "daemon-reload") {
						t.Fatalf("native removal admitted: %v", fixture.commands)
					}
				}
				if data, err := os.ReadFile(unit); err != nil || string(data) != definition {
					t.Fatal("definition changed")
				}
			}
			if original.Store.View().Stopping || !reflect.DeepEqual(original.Store.View().Runners[runner], before) {
				t.Fatal("original execution changed")
			}
			if foreground != nil && foreground.Store.View().Stopping {
				t.Fatal("foreground peer stopped")
			}
			if scenario == "unreachable candidate" {
				if _, err := os.Stat(candidate.Storage.State); !os.IsNotExist(err) {
					t.Fatal("fresh candidate store created")
				}
			}
		})
	}
}
