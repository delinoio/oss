// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/desktopruntime"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
)

// Exercise public short controllers with a resident host and an immutable
// pairing address that now belongs to an unrelated listener.
func TestDesktopWorkerControllersUseProvenRuntime(t *testing.T) {
	root := filepath.Join(t.TempDir(), "owner")
	f := startResidentFixture(t, root, "127.0.0.1:0")
	f.launch(t)
	defer f.quit(t)
	if reply := f.request(t, "device.pair-local"); reply.Error != nil {
		t.Fatal(reply.Error.Code)
	}
	var staleRequests atomic.Int32
	stale := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		staleRequests.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer stale.Close()
	clientRoot := filepath.Join(root, "desktop-client")
	clientCredential, err := worker.LoadCredential(clientRoot)
	if err != nil {
		t.Fatal(err)
	}
	clientCredential.Endpoint = stale.URL
	writeDesktopWorkerFixture(t, filepath.Join(clientRoot, "device.json"), clientCredential)
	originalClient, err := security.ReadPrivate(filepath.Join(clientRoot, "device.json"), 16<<10)
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"--mode", "launch", "--client-id", string(clientCredential.DeviceID)}
	o := options{dataDir: root}
	if _, err := desktopWorkerExecutable(context.Background(), o, args); err != nil {
		t.Fatal("initial preparation", domain.SafeError(err).Code)
	}
	workerRoot := filepath.Join(root, "worker")
	credential, err := worker.LoadCredential(workerRoot)
	if err != nil {
		t.Fatal(err)
	}
	credential.Endpoint = stale.URL
	writeDesktopWorkerFixture(t, filepath.Join(workerRoot, "device.json"), credential)
	var attempt localPairingAttempt
	raw, err := security.ReadPrivate(filepath.Join(workerRoot, "local-pairing.json"), 4096)
	if err != nil || json.Unmarshal(raw, &attempt) != nil {
		t.Fatal("pairing journal")
	}
	attempt.Endpoint = stale.URL
	writeDesktopWorkerFixture(t, filepath.Join(workerRoot, "local-pairing.json"), attempt)
	if _, err := desktopWorkerExecutable(context.Background(), o, args); err != nil {
		t.Fatal("retained preparation", domain.SafeError(err).Code)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, append([]string{"--data-dir", root, "worker", "desktop-host"}, args...)...)
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() { input.Close(); cmd.Process.Kill() })
	replies := make(chan envelope, 1)
	go func() {
		var reply envelope
		_ = json.NewDecoder(output).Decode(&reply)
		replies <- reply
		_, _ = io.Copy(io.Discard, output)
	}()
	select {
	case reply := <-replies:
		if reply.Error != nil {
			t.Fatal("host admission", reply.Error.Code)
		}
		if reply.Result == nil {
			t.Fatal("empty admission")
		}
	case <-time.After(40 * time.Second):
		t.Fatal("host admission timeout")
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		status, err := worker.Status(workerRoot)
		if err == nil && status.State == worker.StateRunning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Worker did not attach to resident server")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := io.WriteString(input, "{\"version\":1,\"action\":\"stop\"}\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("host did not join Stop")
	}
	before, err := worker.Status(workerRoot)
	if err != nil {
		t.Fatal(err)
	}
	journal := filepath.Join(workerRoot, "local-pairing.json")
	if err := os.Rename(journal, journal+".retained"); err != nil {
		t.Fatal(err)
	}
	if _, err := desktopWorkerExecutable(context.Background(), o, []string{"--mode", "ensure", "--client-id", string(clientCredential.DeviceID)}); err == nil {
		t.Fatal("missing local proof admitted")
	}
	if err := os.Rename(journal+".retained", journal); err != nil {
		t.Fatal(err)
	}
	// A valid-looking locator with the wrong key must fail the challenge.
	bad := f.target
	bad.Key, err = security.RandomToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := desktopruntime.Publish(bad); err != nil {
		t.Fatal(err)
	}
	if _, err := desktopWorkerExecutable(context.Background(), o, args); err == nil {
		t.Fatal("failed runtime proof admitted")
	}
	if err := desktopruntime.Retire(bad); err != nil {
		t.Fatal(err)
	}
	if _, err := desktopWorkerExecutable(context.Background(), o, args); err == nil {
		t.Fatal("missing locator admitted")
	}
	if err := desktopruntime.Publish(f.target); err != nil {
		t.Fatal(err)
	}
	after, err := worker.Status(workerRoot)
	retained, credentialErr := worker.LoadCredential(workerRoot)
	clientBytes, readErr := security.ReadPrivate(filepath.Join(clientRoot, "device.json"), 16<<10)
	if err != nil || credentialErr != nil || readErr != nil || retained != credential || !bytes.Equal(originalClient, clientBytes) || before.Lifecycle != after.Lifecycle {
		t.Fatal("admission changed original credentials or lifecycle")
	}
	if staleRequests.Load() != 0 {
		t.Fatal("credentials sent to immutable stale address")
	}
}

func writeDesktopWorkerFixture(t *testing.T, path string, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := security.WriteAtomic(path, raw); err != nil {
		t.Fatal(err)
	}
}
