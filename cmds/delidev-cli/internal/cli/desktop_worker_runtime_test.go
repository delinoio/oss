// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/desktopruntime"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
)

// Exercise public short controllers through a real resident-runtime transition.
// The retained credential, journal and grant stay untouched while the runtime
// restarts on a different loopback listener and the immutable pairing address
// is taken over by an unrelated listener.
func TestDesktopWorkerControllersUseProvenRuntime(t *testing.T) {
	root := filepath.Join(t.TempDir(), "owner")
	original := startResidentFixture(t, root, "127.0.0.1:0")
	original.launch(t)
	if reply := original.request(t, "device.pair-local"); reply.Error != nil {
		t.Fatal(reply.Error.Code)
	}
	clientRoot := filepath.Join(root, "desktop-client")
	clientCredential, err := worker.LoadCredential(clientRoot)
	if err != nil {
		t.Fatal(err)
	}
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
	originalEndpoint := credential.Endpoint
	original.quit(t)
	var staleRequests atomic.Int32
	if listener, err := net.Listen("tcp4", mustHostPort(t, originalEndpoint)); err == nil {
		stale := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			staleRequests.Add(1)
			w.WriteHeader(http.StatusUnauthorized)
		}))
		stale.Listener.Close()
		stale.Listener = listener
		stale.Start()
		defer stale.Close()
	} else {
		t.Log("immutable pairing address was not reusable; stale listener check is limited to the retired port")
	}
	f := startResidentFixture(t, root, "127.0.0.1:0")
	f.launch(t)
	defer f.quit(t)
	if f.target.Endpoint == originalEndpoint {
		t.Fatal("runtime restart reused the immutable pairing address")
	}
	if _, err := desktopWorkerExecutable(context.Background(), o, args); err != nil {
		t.Fatal("retained preparation", domain.SafeError(err).Code)
	}
	retainedBefore, err := worker.LoadCredential(workerRoot)
	if err != nil || retainedBefore != credential {
		t.Fatal("restart rewrote the original Worker credential")
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

func mustHostPort(t *testing.T, endpoint string) string {
	t.Helper()
	host := strings.TrimPrefix(endpoint, "http://")
	if _, _, err := net.SplitHostPort(host); err != nil {
		t.Fatal(err)
	}
	return host
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

func editDesktopWorkerJSON(t *testing.T, path string, edit func(map[string]any)) {
	t.Helper()
	raw, err := security.ReadPrivate(path, 32<<10)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	edit(value)
	writeDesktopWorkerFixture(t, path, value)
}

// Following the app runtime needs the retained original grant. Consistently
// edited credential and journal evidence, or missing, foreign and mismatched
// grant evidence, must stay RecoveryRequired: no marker, no new grant, no
// credential rewrite and no request to any listener the evidence names.
func TestDesktopWorkerRegistrationRequiresRetainedOriginalGrant(t *testing.T) {
	root := filepath.Join(t.TempDir(), "owner")
	f := startResidentFixture(t, root, "127.0.0.1:0")
	f.launch(t)
	defer f.quit(t)
	if reply := f.request(t, "device.pair-local"); reply.Error != nil {
		t.Fatal(reply.Error.Code)
	}
	clientCredential, err := worker.LoadCredential(filepath.Join(root, "desktop-client"))
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"--mode", "launch", "--client-id", string(clientCredential.DeviceID)}
	ensure := []string{"--mode", "ensure", "--client-id", string(clientCredential.DeviceID)}
	o := options{dataDir: root}
	if _, err := desktopWorkerExecutable(context.Background(), o, args); err != nil {
		t.Fatal("initial preparation", domain.SafeError(err).Code)
	}
	var staleRequests atomic.Int32
	stale := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		staleRequests.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer stale.Close()
	workerRoot := filepath.Join(root, "worker")
	credentialPath := filepath.Join(workerRoot, "device.json")
	journalPath := filepath.Join(workerRoot, "local-pairing.json")
	marker := filepath.Join(workerRoot, "desktop-runtime-follow.json")
	var attempt localPairingAttempt
	raw, err := security.ReadPrivate(journalPath, 4096)
	if err != nil || json.Unmarshal(raw, &attempt) != nil {
		t.Fatal("pairing journal")
	}
	grantPath := filepath.Join(root, "pairing-codes", string(attempt.RequestID)+".json")
	originalCredential, _ := security.ReadPrivate(credentialPath, 16<<10)
	originalJournal, _ := security.ReadPrivate(journalPath, 4096)
	originalGrant, _ := security.ReadPrivate(grantPath, 32<<10)
	grants := func() []string {
		entries, err := os.ReadDir(filepath.Join(root, "pairing-codes"))
		if err != nil {
			t.Fatal(err)
		}
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		return names
	}
	before := grants()
	restore := func() {
		for path, content := range map[string][]byte{credentialPath: originalCredential, journalPath: originalJournal, grantPath: originalGrant} {
			if err := security.WriteAtomic(path, content); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Remove(marker); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
	}
	vectors := map[string]func(){
		"consistently edited endpoints": func() {
			editDesktopWorkerJSON(t, credentialPath, func(v map[string]any) { v["endpoint"] = stale.URL })
			editDesktopWorkerJSON(t, journalPath, func(v map[string]any) { v["endpoint"] = stale.URL })
		},
		"missing grant": func() {
			if err := os.Remove(grantPath); err != nil {
				t.Fatal(err)
			}
		},
		"foreign grant server": func() {
			editDesktopWorkerJSON(t, grantPath, func(v map[string]any) { v["server_id"] = domain.NewID() })
		},
		"mismatched pairing identity": func() {
			editDesktopWorkerJSON(t, credentialPath, func(v map[string]any) { v["pairing_id"] = domain.NewID() })
		},
		"foreign request identity": func() {
			editDesktopWorkerJSON(t, journalPath, func(v map[string]any) { v["request_id"] = domain.NewID() })
		},
	}
	for name, mutate := range vectors {
		t.Run(name, func(t *testing.T) {
			restore()
			mutate()
			mutatedCredential, _ := security.ReadPrivate(credentialPath, 16<<10)
			for _, a := range [][]string{args, ensure} {
				_, err := desktopWorkerExecutable(context.Background(), o, a)
				if err == nil || domain.SafeError(err).Code != domain.RecoveryRequired {
					t.Fatalf("mode %s: unverified evidence did not require recovery: %v", a[1], err)
				}
			}
			if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatal("follow marker created from unverified evidence")
			}
			if staleRequests.Load() != 0 {
				t.Fatal("request reached the edited listener")
			}
			if current, _ := security.ReadPrivate(credentialPath, 16<<10); !bytes.Equal(current, mutatedCredential) {
				t.Fatal("credential was rewritten")
			}
			if names := grants(); strings.Join(names, ",") != strings.Join(before, ",") && name != "missing grant" {
				t.Fatalf("a new grant was issued: %v", names)
			}
		})
	}
	restore()
	if _, err := desktopWorkerExecutable(context.Background(), o, ensure); err != nil {
		t.Fatal("unchanged retained pairing no longer follows", domain.SafeError(err).Code)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("unchanged retained pairing did not follow")
	}
}
