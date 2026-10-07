// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/desktopruntime"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/server"
)

type residentFixture struct {
	cmd    *exec.Cmd
	input  io.WriteCloser
	frames chan desktopReply
	done   chan error
	root   string
	target desktopruntime.Target
}

func startResidentFixture(t *testing.T, root, listen string) *residentFixture {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	f := &residentFixture{cmd: exec.Command(binary, "--data-dir", root, "server", "desktop-host", "--control-version", "2", "--listen", listen), frames: make(chan desktopReply, 64), done: make(chan error, 1), root: root}
	f.input, err = f.cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := f.cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	f.cmd.Stderr = io.Discard
	if err := f.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		defer close(f.frames)
		reader := bufio.NewReader(output)
		for {
			line, err := readDesktopFrame(reader)
			if err != nil {
				return
			}
			var frame desktopReply
			if json.Unmarshal(line, &frame) != nil {
				return
			}
			f.frames <- frame
		}
	}()
	go func() { f.done <- f.cmd.Wait() }()
	t.Cleanup(func() { f.input.Close(); f.cmd.Process.Kill() })
	hello := f.next(t)
	if hello.Error != nil {
		t.Fatalf("host failed: %s", hello.Error.Code)
	}
	raw, _ := json.Marshal(hello.Result)
	var value struct {
		Endpoint   string
		Generation domain.ID
		Key        string
	}
	json.Unmarshal(raw, &value)
	if hello.Version != 2 || hello.ID != "" || value.Generation.Validate() != nil || value.Endpoint == "" {
		t.Fatal("invalid hello")
	}
	f.target = desktopruntime.Target{Version: 2, Root: root, Endpoint: value.Endpoint, Generation: value.Generation, Key: value.Key}
	return f
}
func (f *residentFixture) next(t *testing.T) desktopReply {
	t.Helper()
	select {
	case r, ok := <-f.frames:
		if !ok {
			t.Fatal("host output closed")
		}
		return r
	case <-time.After(40 * time.Second):
		t.Fatal("host reply timeout")
	}
	return desktopReply{}
}
func (f *residentFixture) request(t *testing.T, op desktopOperation, args ...string) desktopReply {
	t.Helper()
	id := domain.NewID()
	if err := json.NewEncoder(f.input).Encode(desktopRequest{Version: 2, ID: id, Operation: op, Arguments: args}); err != nil {
		t.Fatal(err)
	}
	r := f.next(t)
	if r.ID != id {
		t.Fatal("uncorrelated reply")
	}
	return r
}
func (f *residentFixture) launch(t *testing.T) {
	t.Helper()
	r := f.request(t, desktopLaunch)
	if r.Error != nil {
		t.Fatalf("launch: %s", r.Error.Code)
	}
	target, err := desktopruntime.Load(f.root, domain.ID(r.Result.(map[string]any)["server"].(map[string]any)["status"].(map[string]any)["server_id"].(string)))
	if err != nil {
		t.Fatal(err)
	}
	f.target = target
}
func (f *residentFixture) quit(t *testing.T) {
	t.Helper()
	json.NewEncoder(f.input).Encode(desktopRequest{Version: 2, ID: domain.NewID(), Operation: desktopShutdown})
	select {
	case err := <-f.done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(40 * time.Second):
		t.Fatal("host did not join")
	}
}

func TestDesktopResidentCommandsRandomPortAndEOF(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private")
	f := startResidentFixture(t, root, "127.0.0.1:0")
	f.launch(t)
	if f.target.Endpoint == "http://127.0.0.1:46310" {
		t.Fatal("fixed desktop listener")
	}
	if _, err := server.LoadEndpoint(root); err == nil {
		t.Fatal("desktop leaked into ordinary discovery")
	}
	for i := 0; i < 5; i++ {
		r := f.request(t, desktopEnsure)
		if r.Error != nil {
			t.Fatalf("ensure: %s", r.Error.Code)
		}
		r = f.request(t, "server.desktop-status")
		if r.Error != nil {
			t.Fatalf("status: %s", r.Error.Code)
		}
	}
	r := f.request(t, "device.pair-local")
	if r.Error != nil {
		t.Fatalf("pair: %s", r.Error.Code)
	}
	r = f.request(t, "device.inspect")
	if r.Error != nil {
		t.Fatalf("inspect: %s", r.Error.Code)
	}
	f.input.Close()
	select {
	case err := <-f.done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(40 * time.Second):
		t.Fatal("EOF left host alive")
	}
	if _, err := desktopruntime.Load(root, f.target.ServerID); err == nil {
		t.Fatal("retired endpoint remains published")
	}
	intent, err := server.ReadLifecycle(root)
	if err != nil || intent.State != server.DesiredStopped {
		t.Fatal("EOF lost restart suppression")
	}
}
func TestDesktopRestartPreservesOriginalPairing(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private")
	f := startResidentFixture(t, root, "127.0.0.1:0")
	f.launch(t)
	if r := f.request(t, "device.pair-local"); r.Error != nil {
		t.Fatal(r.Error.Code)
	}
	before, err := os.ReadFile(filepath.Join(root, "desktop-client", "device.json"))
	if err != nil {
		t.Fatal(err)
	}
	f.quit(t)
	g := startResidentFixture(t, root, "127.0.0.1:0")
	g.launch(t)
	if r := g.request(t, "device.pair-local"); r.Error != nil {
		t.Fatal(r.Error.Code)
	}
	after, err := os.ReadFile(filepath.Join(root, "desktop-client", "device.json"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("port change rewrote credential")
	}
	if r := g.request(t, "device.inspect-local", "--expected-endpoint", g.target.Endpoint); r.Error != nil {
		t.Fatal(r.Error.Code)
	}
	g.quit(t)
}
func TestDesktopRejectsGenericCommandsAndOverrides(t *testing.T) {
	f := startResidentFixture(t, filepath.Join(t.TempDir(), "private"), "127.0.0.1:0")
	for _, r := range []struct {
		op   desktopOperation
		args []string
	}{{"server.run", nil}, {"worker.start", []string{"--worker-dir", "/foreign"}}, {"device.inspect", []string{"--server", "http://127.0.0.1:1"}}} {
		if f.request(t, r.op, r.args...).Error == nil {
			t.Fatal("unclosed operation admitted")
		}
	}
	f.quit(t)
}
func TestDesktopFrameBoundsAndBufferedRequests(t *testing.T) {
	r := bufio.NewReader(bytes.NewBufferString("one\ntwo\n"))
	for _, want := range []string{"one\n", "two\n"} {
		got, err := readDesktopFrame(r)
		if err != nil || string(got) != want {
			t.Fatal("buffered frame lost")
		}
	}
	if _, err := readDesktopFrame(bufio.NewReader(bytes.NewReader(bytes.Repeat([]byte{'x'}, desktopFrameLimit+1)))); err == nil {
		t.Fatal("oversized frame accepted")
	}
}

// Verify actual graceful product Stop keeps the same CLI available. It never
// converts Stop into another detached server or reopens the retained intent.
func TestDesktopStopKeepsHostAndEnsureCannotReopen(t *testing.T) {
	f := startResidentFixture(t, filepath.Join(t.TempDir(), "private"), "127.0.0.1:0")
	f.launch(t)
	c, err := connectClient(options{dataDir: f.root, desktop: &f.target}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.transport.CloseIdleConnections()
	var out bytes.Buffer
	if code := Run(desktopruntime.WithTarget(context.Background(), &f.target), []string{"--data-dir", f.root, "server", "stop"}, IO{Out: &out, In: bytes.NewReader(nil), Err: io.Discard}); code != 0 {
		t.Fatalf("stop failed: %d", code)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		r := f.request(t, desktopEnsure)
		if r.Error == nil && r.Result.(map[string]any)["state"] == "stopped" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("ensure reopened Stop")
		}
		time.Sleep(25 * time.Millisecond)
	}
	if r := f.request(t, "browser-storage.prepare"); r.Error != nil {
		t.Fatal("stopped server killed host")
	}
	f.quit(t)
}
