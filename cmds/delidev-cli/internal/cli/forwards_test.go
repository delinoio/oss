package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/server"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
)

func TestCLISessionForward(t *testing.T) {
	root := filepath.Join(t.TempDir(), "server")
	ctx, cancel := context.WithCancel(context.Background())
	ready, done := make(chan struct{}), make(chan error, 1)
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	go func() {
		done <- server.Serve(ctx, server.Config{DisableBackgroundMaintenanceForTesting: true, DataDir: root, Listen: "127.0.0.1:0", Logger: logger}, func(server.Endpoint) { close(ready) })
	}()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		t.Fatal("server startup timeout")
	}
	run := func(args []string, input any) map[string]any {
		t.Helper()
		raw := ""
		if input != nil {
			value, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			raw = string(value)
		}
		code, result := cliRun(t, root, args, raw)
		if code != 0 {
			t.Fatalf("%v: %d %v", args, code, result)
		}
		return result["result"].(map[string]any)
	}
	grant := run([]string{"device", "create-pairing", "--type", "worker", "--name", "forward fixture"}, nil)
	codeDocument, err := os.ReadFile(grant["code_file"].(string))
	if err != nil {
		t.Fatal(err)
	}
	workerRoot := filepath.Join(t.TempDir(), "worker")
	code, paired := cliRun(t, root, []string{"worker", "pair", "--worker-dir", workerRoot, "--code-stdin"}, string(codeDocument))
	if code != 0 {
		t.Fatal(paired)
	}
	machine := paired["result"].(map[string]any)["machine_id"].(string)
	workerCtx, stopWorker := context.WithCancel(ctx)
	workerReady, workerDone := make(chan struct{}), make(chan error, 1)
	go func() {
		workerDone <- worker.Run(workerCtx, worker.Config{Root: workerRoot, Logger: logger, Ready: func(domain.ID) { close(workerReady) }})
	}()
	defer func() {
		stopWorker()
		if err := <-workerDone; err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-workerReady:
	case <-time.After(10 * time.Second):
		t.Fatal("Worker startup timeout")
	}
	provider := run([]string{"provider", "create"}, domain.Provider{Name: "Fixture", Endpoint: "http://127.0.0.1:1/v1", Protocol: domain.OpenAIChat, Authentication: domain.KeylessAuth})["resource"].(map[string]any)
	model := run([]string{"model", "create"}, domain.Model{Name: "Fixture model", NativeID: "fixture", ProviderID: domain.ID(provider["id"].(string)), Harnesses: []domain.Harness{domain.Codex}, MetadataSource: domain.UserDeclared})["resource"].(map[string]any)
	agent := run([]string{"agent", "create"}, domain.Agent{Name: "Fixture", Harness: domain.Codex, ModelID: domain.ID(model["id"].(string)), Options: domain.AgentOptions{Permission: domain.PermissionDefault}})["resource"].(map[string]any)
	session := run([]string{"session", "create"}, domain.CreateSession{Name: "Forward fixture", AgentID: domain.ID(agent["id"].(string)), MachineID: domain.ID(machine), Workspace: domain.GeneralChat, Prompt: "Unused fixture prompt"})["session"].(map[string]any)
	verifyCLIForward(t, ctx, root, machine, session)
}

// Exercise CLI framing and the production Worker's joined forwarding lane.
// No installed harness, provider account or external network is involved.
func verifyCLIForward(t *testing.T, ctx context.Context, root, machine string, session map[string]any) {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	fixtureDone := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			fixtureDone <- err
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
		value := make([]byte, 12)
		_, err = io.ReadFull(conn, value)
		if err == nil {
			_, err = conn.Write(value)
		}
		fixtureDone <- err
	}()
	sessionID := session["id"].(string)
	id := string(domain.NewID())
	var args []string
	var out *io.PipeReader
	var decoder *json.Decoder
	var done chan int
	var stop context.CancelFunc
	var first map[string]any
	// Background workspace publication may advance the accepted session revision
	// after session creation and before this foreground command starts. Refresh
	// only this expected conflict so the fixture still exercises the production
	// revision guard without making the test timing-sensitive.
	for attempt := 0; attempt < 4; attempt++ {
		code, current := cliRun(t, root, []string{"session", "get", "--id", sessionID}, "")
		if code != 0 {
			t.Fatal(current)
		}
		latest := current["result"].(map[string]any)
		args = []string{"session", "forward", "start", "--session-id", sessionID, "--machine-id", machine, "--revision", strconv.FormatUint(uint64(latest["revision"].(float64)), 10), "--worker-port", strconv.Itoa(listener.Addr().(*net.TCPAddr).Port), "--request-id", id}
		var writer *io.PipeWriter
		out, writer = io.Pipe()
		command, cancel := context.WithCancel(ctx)
		stop = cancel
		done = make(chan int, 1)
		go func() {
			done <- Run(command, append([]string{"--data-dir", root}, args...), IO{In: bytes.NewReader(nil), Out: writer, Err: io.Discard})
			_ = writer.Close()
		}()
		decoder = json.NewDecoder(out)
		readiness := make(chan map[string]any, 1)
		go func() { var v map[string]any; _ = decoder.Decode(&v); readiness <- v }()
		select {
		case first = <-readiness:
		case <-time.After(15 * time.Second):
			t.Fatal("CLI forward readiness timeout")
		}
		problem, isProblem := first["error"].(map[string]any)
		if !isProblem || problem["code"] != "conflict" || problem["message"] != "The session revision changed." {
			break
		}
		stop()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Fatal("CLI stale forward attempt did not stop")
		}
		_ = out.Close()
	}
	if stop == nil {
		t.Fatal("CLI forward did not start")
	}
	defer out.Close()
	defer stop()
	if first == nil || first["error"] != nil {
		t.Fatal("CLI start failed", first)
	}
	result := first["result"].(map[string]any)
	endpoint, ok := result["local_endpoint"].(string)
	if !ok || result["ready"] != true || result["forward_id"] != id {
		t.Fatal("CLI lost exact endpoint", first)
	}
	conn, err := net.DialTimeout("tcp4", endpoint, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	payload := []byte("opaque\x00bytes")
	if _, err := conn.Write(payload); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, got); err != nil || !bytes.Equal(got, payload) {
		t.Fatal("CLI forwarding changed exact bytes", err)
	}
	if err := <-fixtureDone; err != nil {
		t.Fatal(err)
	}
	code, observed := cliRun(t, root, []string{"session", "forward", "status", "--session-id", sessionID, "--id", id}, "")
	if code != 0 {
		t.Fatal(observed)
	}
	record := observed["result"].(map[string]any)["forward"].(map[string]any)
	if body := record["data"].(map[string]any); body["state"] != "active" || body["local_endpoint"] != endpoint {
		t.Fatal("CLI status lost active endpoint", record)
	}
	code, replay := cliRun(t, root, args, "")
	if code != 0 || replay["result"].(map[string]any)["replayed"] != true {
		t.Fatal("CLI start replayed native listener", replay)
	}
	code, stopped := cliRun(t, root, []string{"session", "forward", "stop", "--session-id", sessionID, "--id", id, "--revision", strconv.FormatUint(uint64(record["revision"].(float64)), 10)}, "")
	if code != 0 {
		t.Fatal(stopped)
	}
	var final map[string]any
	if err := decoder.Decode(&final); err != nil {
		t.Fatal("CLI final envelope", err)
	}
	select {
	case code := <-done:
		if code != 0 {
			t.Fatal("CLI lifetime failed", code, final)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("CLI lifetime did not join")
	}
	if conn, err := net.DialTimeout("tcp4", endpoint, time.Second); err == nil {
		conn.Close()
		t.Fatal("CLI Stop left native listener")
	}
	code, replay = cliRun(t, root, args, "")
	if code != 0 || replay["result"].(map[string]any)["replayed"] != true {
		t.Fatal("CLI stopped replay reopened listener", replay)
	}
}
