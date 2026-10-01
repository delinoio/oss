package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/server"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/userservice"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
)

// This opt-in fixture creates only unique current-user registrations derived
// from temporary scopes. It never reads a saved user scope or provider account.
func TestNativeUserServiceLifecycle(t *testing.T) {
	if os.Getenv("DELIDEV_NATIVE_USER_SERVICE_TEST") != "1" {
		t.Skip("explicit temporary native service fixture not selected")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" && runtime.GOOS != "windows" {
		t.Skip("unsupported native service platform")
	}
	directory, e := os.MkdirTemp("", "delidev-user-service-fixture-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("Retained isolated native fixture: %s", directory)
		} else if e := os.RemoveAll(directory); e != nil {
			t.Error(e)
		}
	})
	directory, e = filepath.EvalSymlinks(directory)
	if e != nil {
		t.Fatal(e)
	}
	binary := filepath.Join(directory, "delidev")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, "../../")
	if output, e := build.CombinedOutput(); e != nil {
		t.Fatalf("fixture build: %s %v", output, e)
	}
	root := filepath.Join(directory, "server")
	invoke := func(args ...string) (json.RawMessage, error) {
		var response struct {
			Result json.RawMessage `json:"result"`
			Error  *domain.Error   `json:"error"`
		}
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, binary, append([]string{"--data-dir", root}, args...)...)
		output, e := command.Output()
		if len(output) == 0 {
			return nil, e
		}
		if decode := json.Unmarshal(output, &response); decode != nil {
			return nil, decode
		}
		if response.Error != nil {
			return response.Result, response.Error
		}
		return response.Result, e
	}
	controller, stop := context.WithCancel(context.Background())
	defer stop()
	foreground := exec.CommandContext(controller, binary, "--data-dir", root, "server", "run", "--listen", "127.0.0.1:0")
	if e = foreground.Start(); e != nil {
		t.Fatal(e)
	}
	initialDone := make(chan error, 1)
	go func() { initialDone <- foreground.Wait() }()
	until := time.Now().Add(15 * time.Second)
	for {
		if _, e = server.LoadEndpoint(root); e == nil {
			break
		}
		if time.Now().After(until) {
			t.Fatal("temporary server did not initialize")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, e = invoke("server", "stop"); e != nil {
		t.Fatal(e)
	}
	if e = <-initialDone; e != nil {
		t.Fatal(e)
	}
	action := func(kind, verb string, revision uint64, extra ...string) userservice.Result {
		t.Helper()
		args := []string{kind, "service", verb, "--revision", strconv.FormatUint(revision, 10)}
		args = append(args, extra...)
		raw, e := invoke(args...)
		if e != nil {
			t.Fatalf("native %s %s: %v", kind, verb, e)
		}
		var result userservice.Result
		if json.Unmarshal(raw, &result) != nil {
			t.Fatal("invalid service result")
		}
		return result
	}
	status := func(kind string) userservice.Status {
		t.Helper()
		raw, e := invoke(kind, "service", "status")
		if e != nil {
			t.Fatal(e)
		}
		var result userservice.Status
		if json.Unmarshal(raw, &result) != nil {
			t.Fatal("invalid status")
		}
		return result
	}
	cleanup := func(kind string) {
		if kind == "worker" {
			if _, e := worker.LoadCredential(filepath.Join(root, "worker")); os.IsNotExist(e) {
				return
			}
		}
		s := status(kind)
		if s.State == userservice.Absent {
			return
		}
		if s.State != userservice.Stopped {
			s = action(kind, "stop", s.Revision).Status
		}
		action(kind, "remove", s.Revision)
	}
	// Cleanup uses only the original verified registration; a failure remains
	// visible instead of force-deleting native state or unrelated processes.
	t.Cleanup(func() { cleanup("worker"); cleanup("server") })
	installed := action("server", "install", 0, "--listen", "127.0.0.1:0")
	if installed.Status.State != userservice.Stopped || installed.Status.LoginEnabled {
		t.Fatal("native install started server")
	}
	registrationPath := filepath.Join(root, "user-service-server.json")
	assertLaunch := func(want string) {
		t.Helper()
		registration, err := os.ReadFile(registrationPath)
		if err != nil {
			t.Fatal(err)
		}
		intent, err := server.ReadLifecycle(root)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := invoke("server", "desktop-launch", "--listen", "127.0.0.1:0")
		if err != nil || !strings.Contains(string(raw), want) {
			t.Fatal("desktop launch did not respect native service ownership", err, string(raw))
		}
		after, err := os.ReadFile(registrationPath)
		if err != nil || !bytes.Equal(registration, after) {
			t.Fatal("desktop launch changed the original service registration", err)
		}
		afterIntent, err := server.ReadLifecycle(root)
		if err != nil || intent != afterIntent {
			t.Fatal("desktop launch changed native-owned lifecycle intent", err)
		}
	}
	assertLaunch(`"state":"service-managed"`)
	started := action("server", "start", 1)
	if started.Status.State != userservice.Running {
		t.Fatal(started)
	}
	until = time.Now().Add(15 * time.Second)
	for {
		if _, e = invoke("server", "status"); e == nil {
			break
		}
		if time.Now().After(until) {
			t.Fatal("service server did not become ready")
		}
		time.Sleep(50 * time.Millisecond)
	}
	assertLaunch(`"reused":true`)
	// The exact server owner/identity/data remains unchanged across service launch.
	before, e := security.LoadIdentity(root)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = invoke("worker", "pair-local"); e != nil {
		t.Fatal(e)
	}
	workerRoot := filepath.Join(root, "worker")
	credential, e := worker.LoadCredential(workerRoot)
	if e != nil {
		t.Fatal(e)
	}
	action("worker", "install", 0)
	action("worker", "start", 1)
	until = time.Now().Add(15 * time.Second)
	for {
		s, e := worker.Status(workerRoot)
		if e == nil && s.State == worker.StateRunning {
			break
		}
		if time.Now().After(until) {
			t.Fatal("service Worker did not attach")
		}
		time.Sleep(50 * time.Millisecond)
	}
	action("worker", "stop", 2)
	action("worker", "remove", 3)
	retained, e := worker.LoadCredential(workerRoot)
	if e != nil || retained.DeviceID != credential.DeviceID || retained.Token != credential.Token {
		t.Fatal("service removal changed Worker registration")
	}
	// Foreground startup must not create a competing server while service owns it.
	if _, e = invoke("server", "run", "--listen", "127.0.0.1:0"); domain.SafeError(e).Code != domain.Conflict {
		t.Fatal("foreground stole service scope", e)
	}
	// Authenticated self-Stop returns acceptance before its own server exits.
	if _, e = invoke("service-control", "stop", "--kind", "server", "--revision", "2"); e != nil {
		t.Fatal("self Stop acknowledgement lost", e)
	}
	until = time.Now().Add(20 * time.Second)
	for {
		s := status("server")
		if s.State == userservice.Stopped && s.CleanupConfirmed && !s.LoginEnabled {
			break
		}
		if time.Now().After(until) {
			t.Fatal("self Stop cleanup unconfirmed")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if raw, e := invoke("server", "ensure", "--listen", "127.0.0.1:0"); e != nil || !strings.Contains(string(raw), `"state":"stopped"`) {
		t.Fatal("automatic controller restarted a stopped native service", e)
	}
	assertLaunch(`"state":"service-managed"`)
	if _, e = invoke("service-run", "--kind", "server", "--id", string(installed.Status.ID)); e == nil {
		t.Fatal("delayed login restarted stopped server")
	}
	action("server", "start", 3)
	action("server", "stop", 4)
	action("server", "remove", 5)
	if raw, e := invoke("server", "ensure", "--listen", "127.0.0.1:0"); e != nil || !strings.Contains(string(raw), `"state":"stopped"`) {
		t.Fatal("removing a stopped service erased automatic restart suppression", e)
	}
	after, e := security.LoadIdentity(root)
	if e != nil || before != after {
		t.Fatal("service removal changed owner data or authentication")
	}
	if _, e = os.Stat(filepath.Join(root, "state.sqlite")); e != nil {
		t.Fatal("service removal erased database", e)
	}
}
