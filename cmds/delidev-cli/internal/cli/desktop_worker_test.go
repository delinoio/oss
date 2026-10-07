// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/server"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
)

func TestDesktopWorkerOriginalAdmissionReuseEOFAndStop(t *testing.T) {
	root := filepath.Join(t.TempDir(), "owner")
	serverContext, cancelServer := context.WithCancel(context.Background())
	serverReady, serverDone := make(chan server.Endpoint, 1), make(chan error, 1)
	go func() {
		serverDone <- server.Serve(serverContext, server.Config{DataDir: root, Listen: "127.0.0.1:0", DisableBackgroundMaintenanceForTesting: true, Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}, func(endpoint server.Endpoint) { serverReady <- endpoint })
	}()
	defer func() {
		cancelServer()
		if err := <-serverDone; err != nil {
			t.Error(err)
		}
	}()
	var endpoint server.Endpoint
	select {
	case endpoint = <-serverReady:
	case <-time.After(15 * time.Second):
		t.Fatal("server startup")
	}
	o := options{dataDir: root}
	if _, err := pairLocalDevice(context.Background(), o, filepath.Join(root, "desktop-client"), domain.ClientDevice); err != nil {
		t.Fatal(err)
	}
	clientCredential, err := worker.LoadCredential(filepath.Join(root, "desktop-client"))
	if err != nil {
		t.Fatal(err)
	}
	if err := desktopWorkerAuthority(context.Background(), o, endpoint.URL, domain.NewID()); err == nil {
		t.Fatal("replacement client admitted")
	}
	if err := desktopWorkerAuthority(context.Background(), o, "http://127.0.0.1:1", clientCredential.DeviceID); err == nil {
		t.Fatal("foreign listener admitted")
	}
	if _, err := os.Stat(filepath.Join(root, "worker")); !os.IsNotExist(err) {
		t.Fatal("rejected authority created Worker state", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	input, send := io.Pipe()
	output, receive := io.Pipe()
	firstReply, done := make(chan map[string]any, 1), make(chan error, 1)
	go func() {
		var value map[string]any
		_ = json.NewDecoder(output).Decode(&value)
		firstReply <- value
		_, _ = io.Copy(io.Discard, output)
		output.Close()
	}()
	go func() {
		_, err := desktopWorkerHost(ctx, o, []string{"--mode", "launch", "--client-id", string(clientCredential.DeviceID)}, IO{In: input, Out: receive, Err: io.Discard}, endpoint.URL)
		receive.Close()
		done <- err
	}()
	defer func() { cancel(); send.Close() }()
	select {
	case reply := <-firstReply:
		result, ok := reply["result"].(map[string]any)
		if !ok || result["started"] != true {
			t.Fatal("admission", reply)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("no original admission")
	}
	workerRoot := filepath.Join(root, "worker")
	var first worker.RuntimeStatus
	deadline := time.Now().Add(15 * time.Second)
	for {
		first, _ = worker.Status(workerRoot)
		if first.State == worker.StateRunning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("readiness", first)
		}
		time.Sleep(20 * time.Millisecond)
	}
	credential, err := worker.LoadCredential(workerRoot)
	if err != nil {
		t.Fatal(err)
	}
	value, err := desktopWorkerHost(ctx, o, []string{"--mode", "launch", "--client-id", string(clientCredential.DeviceID)}, IO{In: &emptyReader{}, Out: io.Discard, Err: io.Discard}, endpoint.URL)
	if err != nil || value.(map[string]any)["started"] != false {
		t.Fatal("reuse", value, err)
	}
	// Closing the desktop pipe is crash simulation, never an explicit Stop.
	send.Close()
	time.Sleep(250 * time.Millisecond)
	if status, err := worker.Status(workerRoot); err != nil || !status.ControllerActive || status.Lifecycle.Generation != first.Lifecycle.Generation {
		t.Fatal("EOF canceled Worker", status, err)
	}
	if _, err := stopLocalWorker(ctx, workerRoot, first.Lifecycle.Generation); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("controller did not join")
	}
	value, err = desktopWorkerHost(ctx, o, []string{"--mode", "ensure", "--client-id", string(clientCredential.DeviceID)}, IO{In: &emptyReader{}, Out: io.Discard, Err: io.Discard}, endpoint.URL)
	if err != nil || value.(map[string]any)["started"] != false {
		t.Fatal("Stop reopened", value, err)
	}
	retained, err := worker.LoadCredential(workerRoot)
	if err != nil || retained != credential {
		t.Fatal("registration changed", err)
	}
	// A new app process reopens the stopped intent. Losing its admission reply
	// cannot cancel the original controller or authorize a concurrent replay.
	newInput, newSend := io.Pipe()
	defer newSend.Close()
	lost := &lostWorkerAdmission{observed: make(chan struct{}, 1)}
	newDone := make(chan error, 1)
	go func() {
		_, err := desktopWorkerHost(ctx, o, []string{"--mode", "launch", "--client-id", string(clientCredential.DeviceID)}, IO{In: newInput, Out: lost, Err: io.Discard}, endpoint.URL)
		newDone <- err
	}()
	select {
	case <-lost.observed:
	case <-time.After(15 * time.Second):
		t.Fatal("fresh process did not admit Worker")
	}
	second, err := worker.Status(workerRoot)
	if err != nil || !second.ControllerActive || second.Lifecycle.Generation == first.Lifecycle.Generation {
		t.Fatal("lost admission canceled fresh generation", second, err)
	}
	value, err = desktopWorkerHost(ctx, o, []string{"--mode", "ensure", "--client-id", string(clientCredential.DeviceID)}, IO{In: &emptyReader{}, Out: io.Discard, Err: io.Discard}, endpoint.URL)
	if err != nil || value.(map[string]any)["started"] != false {
		t.Fatal("lost reply admitted competing Worker", value, err)
	}
	if err := worker.RequestStop(workerRoot, first.Lifecycle.Generation); err == nil {
		t.Fatal("stale Stop targeted replacement")
	}
	if _, err := io.WriteString(newSend, "{\"version\":1,\"action\":\"stop\"}\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-newDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("original Stop did not join fresh Worker")
	}
	if stopped, err := worker.Status(workerRoot); err != nil || stopped.ControllerActive || stopped.Lifecycle.Desired != worker.WorkerStopped {
		t.Fatal("original Stop was not durable", stopped, err)
	}
}

type lostWorkerAdmission struct{ observed chan struct{} }

func (w *lostWorkerAdmission) Write([]byte) (int, error) {
	select {
	case w.observed <- struct{}{}:
	default:
	}
	return 0, io.ErrClosedPipe
}

type emptyReader struct{}

func (*emptyReader) Read([]byte) (int, error) { return 0, io.EOF }
