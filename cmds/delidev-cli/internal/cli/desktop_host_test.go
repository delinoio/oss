// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/server"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/userservice"
)

type lostDesktopOutput struct{}

func (lostDesktopOutput) Write([]byte) (int, error) { return 0, errors.New("closed desktop output") }

type desktopHostFixture struct {
	cmd   *exec.Cmd
	input io.WriteCloser
	done  chan struct{}
	err   error
	first chan map[string]any
}

func launchDesktopFixture(t *testing.T, root, mode string) *desktopHostFixture {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	f := &desktopHostFixture{cmd: exec.Command(binary, "--data-dir", root, "server", "desktop-host", "--mode", mode, "--listen", "127.0.0.1:0"), done: make(chan struct{}), first: make(chan map[string]any, 1)}
	f.input, err = f.cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	reader, writer := io.Pipe()
	f.cmd.Stdout, f.cmd.Stderr = writer, io.Discard
	if err := f.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		var value map[string]any
		_ = json.NewDecoder(reader).Decode(&value)
		f.first <- value
		_, _ = io.Copy(io.Discard, reader)
		reader.Close()
	}()
	go func() { f.err = f.cmd.Wait(); writer.Close(); close(f.done) }()
	t.Cleanup(func() {
		select {
		case <-f.done:
			return
		default:
		}
		_, _ = io.WriteString(f.input, "{\"version\":1,\"action\":\"stop\"}\n")
		select {
		case <-f.done:
		case <-time.After(5 * time.Second):
			_ = f.cmd.Process.Kill()
			<-f.done
		}
		f.input.Close()
	})
	return f
}

func (f *desktopHostFixture) result(t *testing.T) map[string]any {
	t.Helper()
	select {
	case value := <-f.first:
		result, ok := value["result"].(map[string]any)
		if !ok {
			t.Fatal("desktop host did not return a result")
		}
		return result
	case <-time.After(40 * time.Second):
		t.Fatal("desktop startup did not settle")
	}
	return nil
}

func (f *desktopHostFixture) wait(t *testing.T) {
	t.Helper()
	select {
	case <-f.done:
		if f.err != nil {
			t.Fatal("desktop host failed to exit successfully")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("desktop server exit did not join")
	}
}

func TestDesktopHostOwnsOriginalProcessAndPreservesReuse(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private")
	first := launchDesktopFixture(t, root, "launch")
	result := first.result(t)
	if result["started"] != true {
		t.Fatal("desktop did not start its original process")
	}
	generation := domain.ID(result["generation"].(string))
	if generation.Validate() != nil {
		t.Fatal("invalid original generation")
	}
	identity, err := security.LoadIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	second := launchDesktopFixture(t, root, "launch")
	if second.result(t)["reused"] != true {
		t.Fatal("concurrent desktop did not reuse original server")
	}
	second.wait(t)
	if current, err := server.ReadLifecycle(root); err != nil || current.State != server.DesiredRunning || current.Generation != generation {
		t.Fatal("reuse changed original intent")
	}
	_, err = io.WriteString(first.input, "{\"version\":1,\"action\":\"stop\"}\n")
	if err != nil {
		t.Fatal(err)
	}
	first.wait(t)
	if current, err := server.ReadLifecycle(root); err != nil || current.State != server.DesiredStopped {
		t.Fatal("desktop quit lost Stop suppression")
	}
	lock, err := security.TryLock(filepath.Join(root, "server.lock"))
	if err != nil {
		t.Fatal("desktop process exited without releasing store ownership")
	}
	lock.Close()
	if _, err := os.Stat(filepath.Join(root, "server.json")); !os.IsNotExist(err) {
		t.Fatal("joined server retained endpoint")
	}
	retry := launchDesktopFixture(t, root, "retry")
	if retry.result(t)["state"] != "stopped" {
		t.Fatal("retry reopened stopped intent")
	}
	retry.wait(t)
	fresh := launchDesktopFixture(t, root, "launch")
	if fresh.result(t)["started"] != true {
		t.Fatal("fresh desktop did not reopen original scope")
	}
	retained, err := security.LoadIdentity(root)
	if err != nil || retained.ServerID != identity.ServerID || retained.Token != identity.Token {
		t.Fatal("fresh launch replaced owner identity")
	}
}

func TestDesktopHostEOFDoesNotStopServer(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private")
	f := launchDesktopFixture(t, root, "launch")
	f.result(t)
	f.input.Close()
	select {
	case <-f.done:
		t.Fatal("EOF stopped the original server")
	case <-time.After(250 * time.Millisecond):
	}
	probe := launchDesktopFixture(t, root, "ensure")
	if probe.result(t)["reused"] != true {
		t.Fatal("server was unavailable after control EOF")
	}
	probe.wait(t)
	if code := Run(context.Background(), []string{"--data-dir", root, "server", "stop"}, IO{In: strings.NewReader(""), Out: io.Discard, Err: io.Discard}); code != 0 {
		t.Fatal("original owner Stop failed")
	}
	f.wait(t)
}

func TestDesktopHostLostReadyDeliveryPreservesAdmittedServer(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := desktopHost(ctx, options{dataDir: root}, []string{"--mode", "launch", "--listen", "127.0.0.1:0"}, IO{In: strings.NewReader(""), Out: lostDesktopOutput{}, Err: io.Discard})
		done <- err
	}()
	defer cancel()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(root, "server.json")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("admitted server never published endpoint")
		}
		time.Sleep(25 * time.Millisecond)
	}
	select {
	case <-done:
		t.Fatal("lost readiness pipe stopped the admitted server")
	case <-time.After(250 * time.Millisecond):
	}
	for _, action := range []string{"status", "stop"} {
		if code := Run(context.Background(), []string{"--data-dir", root, "server", action}, IO{In: strings.NewReader(""), Out: io.Discard, Err: io.Discard}); code != 0 {
			t.Fatal("admitted server was unavailable after lost ready delivery")
		}
	}
	select {
	case err := <-done:
		if domain.SafeError(err).Code != domain.Unavailable {
			t.Fatal("lost delivery outcome was erased")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("server did not join after explicit Stop")
	}
}

func TestDesktopHostBrokenStandardPipesPreserveAdmittedServer(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private")
	if err := security.PrivateDir(root); err != nil {
		t.Fatal(err)
	}
	admission, err := userservice.AdmitLaunch(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer admission.Close()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "--data-dir", root, "server", "desktop-host", "--mode", "launch", "--listen", "127.0.0.1:0")
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	diagnostic, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	t.Cleanup(func() {
		select {
		case <-done:
		default:
			_ = cmd.Process.Kill()
			<-done
		}
	})
	// Release startup only after all desktop pipe ends are gone. This tests
	// real fd 1/2 SIGPIPE behavior, which an injected failing writer cannot.
	input.Close()
	output.Close()
	diagnostic.Close()
	admission.Close()
	deadline := time.Now().Add(40 * time.Second)
	for {
		select {
		case <-done:
			t.Fatal("lost desktop pipes terminated the admitted server")
		default:
		}
		if _, err := os.Stat(filepath.Join(root, "server.json")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("admitted server never published endpoint")
		}
		time.Sleep(25 * time.Millisecond)
	}
	for _, action := range []string{"status", "stop"} {
		if code := Run(context.Background(), []string{"--data-dir", root, "server", action}, IO{In: strings.NewReader(""), Out: io.Discard, Err: io.Discard}); code != 0 {
			t.Fatal("admitted server was unavailable after losing desktop pipes")
		}
	}
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("server did not join after explicit Stop")
	}
}

func TestDesktopHostStopDuringAdmissionCannotStartServer(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private")
	if err := security.PrivateDir(root); err != nil {
		t.Fatal(err)
	}
	admission, err := userservice.AdmitLaunch(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer admission.Close()
	f := launchDesktopFixture(t, root, "launch")
	_, _ = io.WriteString(f.input, "{\"version\":1,\"action\":\"stop\"}\n")
	select {
	case <-f.done:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not cancel held admission")
	}
	if intent, err := server.ReadLifecycle(root); err != nil || intent.Version != 0 {
		t.Fatal("canceled desktop wrote running intent")
	}
	if _, err := os.Stat(filepath.Join(root, "server.json")); !os.IsNotExist(err) {
		t.Fatal("canceled desktop initialized server")
	}
}

func TestDesktopHostEnsureCannotInitializeAndInvalidControlCannotStop(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private")
	f := launchDesktopFixture(t, root, "ensure")
	if f.result(t)["state"] != "stopped" {
		t.Fatal("ensure initialized an unconfigured scope")
	}
	f.wait(t)
	for _, input := range []string{"", "{\"version\":1,\"action\":\"stop\"}", "{\"version\":2,\"action\":\"stop\"}\n", "{\"version\":1,\"action\":\"start\"}\n", "{\"version\":1,\"action\":\"stop\",\"path\":\"foreign\"}\n", strings.Repeat("x", 258) + "\n"} {
		stop := make(chan struct{})
		readDesktopStop(strings.NewReader(input), stop)
		select {
		case <-stop:
			t.Fatal("malformed control granted Stop")
		default:
		}
	}
}
