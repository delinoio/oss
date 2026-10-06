package runmoor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"
)

type reloadFixture struct {
	t            *testing.T
	r            *serviceReloader
	m            *Manager
	c            Config
	path         string
	pid, peer    int
	loaded       bool
	version      string
	args         map[int][]string
	commands     []string
	envs         [][]string
	actions      []string
	preflightErr error
	onCommand    func(string) error
	controlErr   error
	waits        int
	log          bytes.Buffer
}

func newReloadFixture(t *testing.T, platform string) *reloadFixture {
	t.Helper()
	m, c, remote, driver, _ := testManager(t)
	home := filepath.Dir(c.Storage.State)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(home, "runtime"))
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path="+filepath.Join(home, "bus"))
	t.Setenv("RUNMOOR_RELOAD_SECRET", "private-fixture-value")
	unit := filepath.Join(home, "units", systemdServiceName)
	if platform == "darwin" {
		unit = filepath.Join(home, "units", serviceLabel+".plist")
	}
	if err := os.MkdirAll(filepath.Dir(unit), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "config.toml")
	body, err := toml.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	m.ConfigPath = path
	oldBinary := filepath.Join(home, "old", "runmoor")
	definition, err := serviceDefinition(platform, oldBinary, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unit, []byte(definition), 0600); err != nil {
		t.Fatal(err)
	}
	oldArgs, err := reloadArguments(platform, []byte(definition))
	if err != nil {
		t.Fatal(err)
	}
	f := &reloadFixture{t: t, m: m, c: c, path: path, pid: 101, peer: 101, loaded: true, version: "0.2.7", args: map[int][]string{101: oldArgs}}
	f.r = &serviceReloader{Platform: platform, Unit: unit, Binary: filepath.Join(home, "new", "runmoor"), Version: Version, Exec: f, Log: slog.New(slog.NewTextHandler(&f.log, nil)),
		Control: f.control, ProcessArgs: func(pid int) ([]string, error) {
			args, ok := f.args[pid]
			if !ok {
				return nil, os.ErrNotExist
			}
			return args, nil
		},
		ProcessStart: func(pid int) (string, error) { return "fixture:" + strconv.Itoa(pid), nil },
		Preflight: func(ctx context.Context, c Config, s Snapshot) error {
			if f.preflightErr != nil {
				return f.preflightErr
			}
			_, err := validateReloadCandidate(ctx, c, s, nil, func(Backend) (Driver, error) { return driver, nil }, func(Connection) (Remote, error) { return remote, nil })
			return err
		},
		Wait: func(ctx context.Context) bool { f.waits++; return f.waits < 3 && ctx.Err() == nil },
	}
	return f
}

func (f *reloadFixture) Start(string, []string, []string) (int, error) {
	return 0, errors.New("unexpected spawn")
}
func (f *reloadFixture) Run(_ context.Context, name string, args, env []string, _ io.Reader) ([]byte, error) {
	command := name + " " + strings.Join(args, " ")
	f.commands = append(f.commands, command)
	f.envs = append(f.envs, append([]string{}, env...))
	if f.onCommand != nil {
		if err := f.onCommand(command); err != nil {
			return nil, err
		}
	}
	if name == f.r.Binary {
		return []byte("runmoor " + f.r.Version + " (" + Revision + ")\n"), nil
	}
	if name == "systemctl" && len(args) > 1 && args[1] == "show" {
		return []byte(strconv.Itoa(f.pid)), nil
	}
	if name == "launchctl" && args[0] == "list" {
		output := "PID\tStatus\tLabel\n"
		if f.loaded {
			pid := "-"
			if f.pid > 0 {
				pid = strconv.Itoa(f.pid)
			}
			output += pid + "\t0\t" + serviceLabel + "\n"
		}
		return []byte(output), nil
	}
	if name == "systemctl" && args[1] == "kill" {
		f.replaceManager()
		return nil, nil
	}
	if name == "launchctl" && args[0] == "kickstart" {
		j, err := readReloadJournal(f.r.Unit)
		if err != nil {
			return nil, err
		}
		f.pid = 202
		f.peer = 0
		f.args[202] = []string{j.Binary, "__service-reload-handoff", reloadJournalPath(f.r.Unit), j.Token}
	}
	if name == "launchctl" && args[0] == "bootout" {
		f.pid = 0
		f.peer = 0
		f.loaded = false
	}
	if name == "launchctl" && args[0] == "bootstrap" {
		f.replaceManager()
		f.loaded = true
	}
	return nil, nil
}

func (f *reloadFixture) replaceManager() {
	body, err := os.ReadFile(f.r.Unit)
	if err != nil {
		f.t.Fatal(err)
	}
	args, err := reloadArguments(f.r.Platform, body)
	if err != nil {
		f.t.Fatal(err)
	}
	f.pid = 303
	f.peer = 303
	f.version = f.r.Version
	f.args[f.pid] = args
}

func (f *reloadFixture) control(ctx context.Context, c Config, req ControlRequest, expected int) (ControlResponse, int, error) {
	f.actions = append(f.actions, req.Action)
	if f.controlErr != nil {
		return ControlResponse{}, 0, f.controlErr
	}
	if f.peer == 0 || expected != 0 && expected != f.peer {
		return ControlResponse{}, f.peer, reloadFailure()
	}
	if req.Action == "reload" {
		err := f.m.Reload(ctx)
		return ControlResponse{SchemaVersion: 1}, f.peer, err
	}
	status := statusOf(f.m.Store.View(), true)
	status.Version = f.version
	return ControlResponse{SchemaVersion: 1, Status: status}, f.peer, nil
}

func (f *reloadFixture) reload() error { return f.r.Reload(context.Background(), f.path, f.c) }
func (f *reloadFixture) mutated() bool {
	for _, command := range f.commands {
		if strings.Contains(command, " daemon-reload") || strings.Contains(command, " kill ") || strings.Contains(command, " debug ") || strings.Contains(command, " kickstart ") || strings.Contains(command, " bootout ") || strings.Contains(command, " bootstrap ") {
			return true
		}
	}
	return false
}

func TestServiceReloadReplacesOnlyManagerAndPreservesJobs(t *testing.T) {
	for _, platform := range []string{"linux", "darwin"} {
		t.Run(platform, func(t *testing.T) {
			f := newReloadFixture(t, platform)
			pool := sortedPools(f.m.Store.View())[0].ID
			id := seedRunner(t, f.m, pool, Busy)
			if err := f.m.Store.Update(func(s *Snapshot) error {
				s.Paused = true
				s.Pools[pool].Phase = Paused
				s.Runners[id].Resources = Resources{7, 1234}
				s.Runners[id].Deadline = nowUTC().Add(time.Hour)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			before := f.m.Store.View()
			saved, _ := json.Marshal(before.Runners[id])
			if err := f.reload(); err != nil {
				t.Fatal(err)
			}
			after := f.m.Store.View()
			current, _ := json.Marshal(after.Runners[id])
			if !bytes.Equal(saved, current) || !after.Paused || after.Pools[pool].Phase != Paused || after.Installation != before.Installation {
				t.Fatal("replacement changed job or lifecycle authority")
			}
			if j, err := readReloadJournal(f.r.Unit); err != nil || j != nil {
				t.Fatalf("journal not cleared: %v", err)
			}
			definition, err := os.ReadFile(f.r.Unit)
			if err != nil {
				t.Fatal(err)
			}
			args, err := reloadArguments(platform, definition)
			if err != nil || args[0] != f.r.Binary {
				t.Fatal("service kept old executable")
			}
			if platform == "linux" && !strings.Contains(strings.Join(f.commands, "\n"), "systemctl --user kill --kill-who=main --signal=SIGKILL runmoor.service") {
				t.Fatal("kill not limited to the manager")
			}
			for i, command := range f.commands {
				env := commandEnvironmentMap(t, f.envs[i])
				if _, ok := env["RUNMOOR_RELOAD_SECRET"]; ok {
					t.Fatal("secret inherited")
				}
				if strings.HasPrefix(command, "systemctl ") {
					if env["XDG_RUNTIME_DIR"] == "" || env["DBUS_SESSION_BUS_ADDRESS"] == "" {
						t.Fatal("session lookup lost")
					}
				} else if _, ok := env["XDG_RUNTIME_DIR"]; ok {
					t.Fatal("session context passed outside systemctl")
				}
				if strings.Contains(command, " restart ") || strings.Contains(command, " stop ") {
					t.Fatal("upgrade drained jobs")
				}
			}
			if strings.Contains(f.log.String(), f.path) || strings.Contains(f.log.String(), "private-fixture-value") {
				t.Fatal("private information logged")
			}
		})
	}
}

func TestServiceReloadLeavesCurrentNewerAndForegroundManagers(t *testing.T) {
	for _, scenario := range []string{"same", "newer", "foreground", "no service", "inactive service"} {
		t.Run(scenario, func(t *testing.T) {
			f := newReloadFixture(t, "linux")
			switch scenario {
			case "same":
				f.version = Version
			case "newer":
				f.version = "99.0.0"
			case "foreground":
				f.peer = 404
			case "no service":
				if err := os.Remove(f.r.Unit); err != nil {
					t.Fatal(err)
				}
			case "inactive service":
				f.pid = 0
				f.peer = 404
			}
			if err := f.reload(); err != nil {
				t.Fatal(err)
			}
			if f.mutated() {
				t.Fatal("reload changed an unrelated or current service")
			}
			if f.actions[len(f.actions)-1] != "reload" {
				t.Fatal("settings were not reloaded")
			}
		})
	}
}

func TestServiceReloadRejectsUnsafeCandidatesBeforeReplacement(t *testing.T) {
	for _, scenario := range []string{"invalid version", "invalid CLI version", "preflight", "stopping", "wrong arguments", "reused PID", "invalid unit", "wrong config", "unreachable", "concurrent service"} {
		t.Run(scenario, func(t *testing.T) {
			f := newReloadFixture(t, "linux")
			switch scenario {
			case "invalid version":
				f.version = "0.2"
			case "invalid CLI version":
				f.r.Version = "0.3.0-beta"
			case "preflight":
				f.preflightErr = problem(ErrAuth, "Fixture credential invalid.", "Replace credential.")
			case "stopping":
				f.m.Store.Update(func(s *Snapshot) error { s.Stopping = true; return nil })
			case "wrong arguments":
				f.args[f.pid] = []string{"/unrelated", "run"}
			case "reused PID":
				calls := 0
				f.r.ProcessStart = func(int) (string, error) { calls++; return strconv.Itoa(calls), nil }
			case "invalid unit":
				os.WriteFile(f.r.Unit, []byte("invalid definition"), 0600)
			case "wrong config":
				text, _ := serviceDefinition("linux", f.args[f.pid][0], f.path+".other")
				os.WriteFile(f.r.Unit, []byte(text), 0600)
			case "unreachable":
				f.controlErr = reloadFailure()
			case "concurrent service":
				lock, err := lockServiceOperation(f.r.Unit)
				if err != nil {
					t.Fatal(err)
				}
				defer unlockState(lock)
			}
			if err := f.reload(); err == nil {
				t.Fatal("unsafe reload succeeded")
			}
			if f.mutated() {
				t.Fatal("unsafe reload reached native mutation")
			}
		})
	}
}

func TestServiceReloadInterruptedNativeOutcomesAreRecoverable(t *testing.T) {
	for _, platform := range []string{"linux", "darwin"} {
		t.Run(platform, func(t *testing.T) {
			f := newReloadFixture(t, platform)
			failed := false
			f.onCommand = func(command string) error {
				if !failed && (strings.Contains(command, " --signal=SIGKILL ") || strings.Contains(command, " bootstrap ")) {
					failed = true
					f.replaceManager()
					f.loaded = true
					return errors.New("private native failure")
				}
				return nil
			}
			if err := f.reload(); err == nil {
				t.Fatal("unknown command outcome claimed success")
			}
			j, err := readReloadJournal(f.r.Unit)
			if err != nil || j == nil {
				t.Fatal("recovery intent lost")
			}
			before := len(f.commands)
			f.onCommand = nil
			if err := f.reload(); err != nil {
				t.Fatal(err)
			}
			for _, command := range f.commands[before:] {
				if strings.Contains(command, " --signal=SIGKILL ") || strings.Contains(command, " kickstart ") || strings.Contains(command, " bootstrap ") {
					t.Fatal("recovery blindly repeated replacement")
				}
			}
		})
	}
}

func TestServiceReloadRejectsDefinitionReplacementAndConcurrentStop(t *testing.T) {
	for _, scenario := range []string{"file replacement", "stop", "wrong replacement manager"} {
		t.Run(scenario, func(t *testing.T) {
			f := newReloadFixture(t, "linux")
			f.onCommand = func(command string) error {
				if strings.Contains(command, " daemon-reload") {
					switch scenario {
					case "file replacement":
						body, _ := os.ReadFile(f.r.Unit)
						other := f.r.Unit + ".replacement"
						if err := os.WriteFile(other, body, 0600); err != nil {
							t.Fatal(err)
						}
						if err := os.Rename(other, f.r.Unit); err != nil {
							t.Fatal(err)
						}
					case "stop":
						f.m.Stop(false)
					case "wrong replacement manager":
						f.pid = 999
						f.args[999] = []string{"/foreign"}
					}
				}
				return nil
			}
			if err := f.reload(); err == nil {
				t.Fatal("concurrent change was ignored")
			}
			for _, command := range f.commands {
				if strings.Contains(command, " --signal=SIGKILL ") {
					t.Fatal("changed service was killed")
				}
			}
		})
	}
}

func TestServiceReloadWaitTimeoutAndLaunchdHelperRecovery(t *testing.T) {
	for _, platform := range []string{"linux", "darwin"} {
		t.Run(platform, func(t *testing.T) {
			f := newReloadFixture(t, platform)
			f.onCommand = func(command string) error {
				if strings.Contains(command, " --signal=SIGKILL ") {
					f.peer = 0
					f.pid = 0
					return errors.New("interrupted kill")
				}
				if strings.Contains(command, " bootout ") {
					return errors.New("interrupted unload")
				}
				return nil
			}
			if err := f.reload(); err == nil {
				t.Fatal("incomplete replacement succeeded")
			}
			f.onCommand = nil
			if platform == "linux" {
				if err := f.reload(); err == nil || f.waits == 0 {
					t.Fatal("missing service not observed with a bounded wait")
				}
				f.replaceManager()
			}
			if err := f.reload(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestServiceReloadStartupUsesCommittedConfigAndPreservesStop(t *testing.T) {
	f := newReloadFixture(t, "linux")
	f.onCommand = func(command string) error {
		if strings.Contains(command, " --signal=SIGKILL ") {
			return errors.New("pause before replacement")
		}
		return nil
	}
	if err := f.reload(); err == nil {
		t.Fatal("expected pending restart")
	}
	f.replaceManager()
	f.pid = os.Getpid()
	f.args[f.pid] = f.args[303]
	f.c.Pools[0].Labels = append(f.c.Pools[0].Labels, "unaccepted-candidate")
	committed, preserve, err := f.r.startup(context.Background(), f.path, f.c)
	if err != nil || !preserve || fingerprint(committed) != fingerprint(f.m.Store.View().Requested) {
		t.Fatal("startup accepted the candidate")
	}
	if err := f.m.Stop(false); err != nil {
		t.Fatal(err)
	}
	f.m.PreserveStop = preserve
	if err := f.m.activate(committed); err != nil {
		t.Fatal(err)
	}
	if !f.m.Store.View().Stopping {
		t.Fatal("restart undid Stop")
	}
	f.pid = 404
	_, preserve, err = f.r.startup(context.Background(), f.path, f.c)
	if err != nil || preserve {
		t.Fatal("foreground run consumed service recovery")
	}
}

func TestControlPeerBindsResponseToActualProcess(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("Unix control")
	}
	m, c, _, _, _ := testManager(t)
	server, err := m.ServeControl()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	response, pid, err := sendControlPeer(context.Background(), c, ControlRequest{Action: "status"}, os.Getpid(), true)
	if err != nil || pid != os.Getpid() || response.Status == nil {
		t.Fatalf("peer identity: %d, %v", pid, err)
	}
	_, _, err = sendControlPeer(context.Background(), c, ControlRequest{Action: "stop"}, os.Getpid()+1, true)
	if err == nil || m.Store.View().Stopping {
		t.Fatal("request reached a different peer")
	}
	if _, err := controlPeerPID(&net.TCPConn{}); err == nil {
		t.Fatal("non-Unix peer accepted")
	}
}

func TestServiceReloadReadyConfigurationSurvivesManagerLoopStartup(t *testing.T) {
	f := newReloadFixture(t, "linux")
	if err := f.m.initializeRun(f.c); err != nil {
		t.Fatal(err)
	}
	server, err := f.m.ServeControl()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	candidate := f.c
	candidate.Pools = append([]Pool{}, f.c.Pools...)
	candidate.Pools[0].Labels = append([]string{}, f.c.Pools[0].Labels...)
	candidate.Pools[0].Labels = append(candidate.Pools[0].Labels, "reload-at-readiness")
	body, err := toml.Marshal(candidate)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.path, body, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := SendControl(context.Background(), f.c, ControlRequest{Action: "reload"}); err != nil {
		t.Fatal(err)
	}
	accepted := f.m.Store.View()
	if _, err := SendControl(context.Background(), f.c, ControlRequest{Action: "stop"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := f.m.runActivated(ctx); err != nil {
		t.Fatal(err)
	}
	current := f.m.Store.View()
	if current.Generation != accepted.Generation || fingerprint(current.Requested) != fingerprint(accepted.Requested) || !current.Stopping {
		t.Fatal("manager loop startup undid a control decision made after readiness")
	}
}

func TestServiceReloadCandidateChangeAfterRestartRetainsCommittedConfig(t *testing.T) {
	f := newReloadFixture(t, "linux")
	before := f.m.Store.View()
	f.onCommand = func(command string) error {
		if strings.Contains(command, " --signal=SIGKILL ") {
			return os.WriteFile(f.path, []byte("invalid candidate TOML"), 0600)
		}
		return nil
	}
	if err := f.reload(); err == nil {
		t.Fatal("replacement accepted an invalid changed candidate")
	}
	after := f.m.Store.View()
	if f.version != Version || after.Generation != before.Generation || fingerprint(after.Requested) != fingerprint(before.Requested) {
		t.Fatal("rejected candidate replaced committed configuration or downgraded manager")
	}
	j, err := readReloadJournal(f.r.Unit)
	if err != nil || j == nil || j.Stage != reloadRunning {
		t.Fatal("replacement outcome was not retained")
	}
	body, err := toml.Marshal(f.c)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.path, body, 0600); err != nil {
		t.Fatal(err)
	}
	f.commands = nil
	f.onCommand = nil
	if err := f.reload(); err != nil {
		t.Fatal(err)
	}
	if f.mutated() {
		t.Fatal("retry replaced the already running target manager")
	}
}

func TestServiceReloadRejectsUnsafeRecoveryRecord(t *testing.T) {
	for _, scenario := range []string{"symlink", "public permissions", "trailing JSON", "unknown field"} {
		t.Run(scenario, func(t *testing.T) {
			f := newReloadFixture(t, "linux")
			f.onCommand = func(command string) error {
				if strings.Contains(command, " --signal=SIGKILL ") {
					return errors.New("retain pending restart")
				}
				return nil
			}
			if err := f.reload(); err == nil {
				t.Fatal("expected pending replacement")
			}
			journal := reloadJournalPath(f.r.Unit)
			body, err := os.ReadFile(journal)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "symlink":
				if err := os.Rename(journal, journal+".retained"); err != nil {
					t.Fatal(err)
				}
				err = os.Symlink(journal+".retained", journal)
			case "public permissions":
				err = os.Chmod(journal, 0644)
			case "trailing JSON":
				err = os.WriteFile(journal, append(body, []byte("{}")...), 0600)
			case "unknown field":
				err = os.WriteFile(journal, bytes.Replace(body, []byte("\"schema\":1"), []byte("\"schema\":1,\"unknown\":true"), 1), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			retained, err := os.ReadFile(journal)
			if err != nil {
				t.Fatal(err)
			}
			f.commands = nil
			if err := f.reload(); err == nil || f.mutated() {
				t.Fatal("unsafe recovery record authorized a native operation")
			}
			current, err := os.ReadFile(journal)
			if err != nil || !bytes.Equal(current, retained) {
				t.Fatal("unsafe record was not preserved")
			}
		})
	}
}

func TestServiceStopUsesReloadSerializationLock(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("supported native service")
	}
	f := newReloadFixture(t, runtime.GOOS)
	unit := servicePath()
	if err := os.MkdirAll(filepath.Dir(unit), 0700); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(f.r.Unit)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unit, body, 0600); err != nil {
		t.Fatal(err)
	}
	lock, err := lockServiceOperation(unit)
	if err != nil {
		t.Fatal(err)
	}
	defer unlockState(lock)
	err = Service(context.Background(), "stop", f.path, f.c, f)
	if err == nil || classify(err, ErrControl, "fixture", "fixture").Code != ErrRetry || len(f.commands) != 0 || f.m.Store.View().Stopping {
		t.Fatal("service stop bypassed reload serialization")
	}
}
