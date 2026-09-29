package cli

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/server"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
)

func desktopFixture(t *testing.T) (string, worker.Credential) {
	t.Helper()
	return desktopFixtureEndpoint(t, nil)
}
func desktopFixtureEndpoint(t *testing.T, selectEndpoint func(string, server.Endpoint)) (string, worker.Credential) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "server")
	ctx, cancel := context.WithCancel(context.Background())
	ready, done := make(chan struct{}), make(chan error, 1)
	go func() {
		done <- server.Serve(ctx, server.Config{DataDir: root, Listen: "127.0.0.1:0", Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}, func(server.Endpoint) { close(ready) })
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		t.Fatal("fixture startup")
	}
	if selectEndpoint != nil {
		endpoint, err := server.LoadEndpoint(root)
		if err != nil {
			t.Fatal(err)
		}
		selectEndpoint(root, endpoint)
	}
	if code, result := cliRun(t, root, []string{"device", "pair-local", "--device-dir", filepath.Join(root, "desktop-client")}, ""); code != 0 {
		t.Fatal(result)
	}
	credential, err := worker.LoadCredential(filepath.Join(root, "desktop-client"))
	if err != nil {
		t.Fatal(err)
	}
	return root, credential
}
func revokeDesktop(t *testing.T, root string, credential worker.Credential) {
	t.Helper()
	if code, result := cliRun(t, root, []string{"device", "revoke", "--id", string(credential.DeviceID), "--revision", "1"}, ""); code != 0 {
		t.Fatal(result)
	}
}
func recoveryArgs(original worker.Credential, request domain.ID) []string {
	return []string{"--request-id", string(request), "device", "recover-local", "--id", string(original.DeviceID), "--revision", "2"}
}

func TestDesktopRecoveryRequiresOriginalCredentialCommitment(t *testing.T) {
	for _, mutation := range []string{"token", "pairing", "missing-commitment", "damaged-commitment"} {
		t.Run(mutation, func(t *testing.T) {
			root, original := desktopFixture(t)
			revokeDesktop(t, root, original)
			changed := original
			switch mutation {
			case "token":
				changed.Token, _ = worker.RandomToken()
			case "pairing":
				changed.PairingID = domain.NewID()
			case "missing-commitment":
				if err := os.Remove(filepath.Join(root, "desktop-registration.json")); err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
			case "damaged-commitment":
				if err := security.WriteAtomic(filepath.Join(root, "desktop-registration.json"), []byte("{}")); err != nil {
					t.Fatal(err)
				}
			}
			clientRoot := filepath.Join(root, "desktop-client")
			if err := writeRecoveryJSON(filepath.Join(clientRoot, "device.json"), changed); err != nil {
				t.Fatal(err)
			}
			before, err := desktopHashes(clientRoot)
			if err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{{"device", "inspect-local"}, recoveryArgs(original, domain.NewID())} {
				if code, result := cliRun(t, root, args, ""); code == 0 || result["error"].(map[string]any)["code"] != "recovery_required" {
					t.Fatal("unverified credential accepted", result)
				}
			}
			if _, err := os.Lstat(filepath.Join(root, "desktop-recovery.json")); !os.IsNotExist(err) {
				t.Fatal("unverified credential created recovery intent")
			}
			after, err := desktopHashes(clientRoot)
			if err != nil || after != before {
				t.Fatal("rejected recovery changed original files", err)
			}
			if code, result := cliRun(t, root, []string{"device", "list"}, ""); code != 0 || len(result["result"].(map[string]any)["resources"].([]any)) != 1 {
				t.Fatal("unverified credential created a replacement", result)
			}
		})
	}
}

func TestDesktopRecoveryRequiresOriginalLocalPairing(t *testing.T) {
	for _, mutation := range []string{"missing", "damaged", "foreign-server", "foreign-endpoint", "foreign-request", "foreign-grant"} {
		t.Run(mutation, func(t *testing.T) {
			root, original := desktopFixture(t)
			revokeDesktop(t, root, original)
			clientRoot := filepath.Join(root, "desktop-client")
			path := filepath.Join(clientRoot, "local-pairing.json")
			raw, err := security.ReadPrivate(path, 4096)
			if err != nil {
				t.Fatal(err)
			}
			var attempt map[string]any
			if err := json.Unmarshal(raw, &attempt); err != nil {
				t.Fatal(err)
			}
			switch mutation {
			case "missing":
				err = os.Remove(path)
			case "damaged":
				err = security.WriteAtomic(path, []byte("{}"))
			case "foreign-server":
				attempt["server_id"] = domain.NewID()
				err = writeRecoveryJSON(path, attempt)
			case "foreign-endpoint":
				attempt["endpoint"] = "http://127.0.0.1:1"
				err = writeRecoveryJSON(path, attempt)
			case "foreign-request":
				attempt["request_id"] = domain.NewID()
				err = writeRecoveryJSON(path, attempt)
			case "foreign-grant":
				grantPath := filepath.Join(root, "pairing-codes", attempt["request_id"].(string)+".json")
				var grant worker.PairingCode
				raw, err = security.ReadPrivate(grantPath, 32<<10)
				if err == nil {
					err = domain.Decode(raw, &grant)
				}
				if err == nil {
					grant.PairingID = domain.NewID()
					err = writeRecoveryJSON(grantPath, grant)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			before, err := desktopHashes(clientRoot)
			if err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{{"device", "inspect-local"}, recoveryArgs(original, domain.NewID())} {
				if code, result := cliRun(t, root, args, ""); code == 0 || result["error"].(map[string]any)["code"] != "recovery_required" {
					t.Fatal("missing or contradictory original pairing accepted", result)
				}
			}
			if _, err := os.Lstat(filepath.Join(root, "desktop-recovery.json")); !os.IsNotExist(err) {
				t.Fatal("invalid original pairing created recovery intent")
			}
			after, err := desktopHashes(clientRoot)
			if err != nil || after != before {
				t.Fatal("original pairing evidence replaced", err)
			}
			if code, result := cliRun(t, root, []string{"device", "list"}, ""); code != 0 || len(result["result"].(map[string]any)["resources"].([]any)) != 1 {
				t.Fatal("invalid original pairing created a replacement", result)
			}
		})
	}
}

func TestDesktopRecoveryRejectsMissingOriginalPairingCommitment(t *testing.T) {
	root, original := desktopFixture(t)
	revokeDesktop(t, root, original)
	request := domain.NewID()
	args := recoveryArgs(original, request)
	if code, result := cliRun(t, root, args, ""); code != 0 {
		t.Fatal(result)
	}
	record, err := loadDesktopRecovery(root)
	if err != nil {
		t.Fatal(err)
	}
	record.Phase = recoveryPrepared
	record.Original[1] = ""
	if err := writeRecoveryJSON(filepath.Join(root, "desktop-recovery.json"), record); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{args, {"device", "inspect-local"}, {"device", "pair-local", "--device-dir", filepath.Join(root, "desktop-client")}} {
		if code, result := cliRun(t, root, args, ""); code == 0 || result["error"].(map[string]any)["code"] != "recovery_required" {
			t.Fatal("missing original pairing commitment accepted", result)
		}
	}
}

func TestDesktopRecoveryEnforcesExpectedEndpointBeforeMutation(t *testing.T) {
	root, original := desktopFixture(t)
	revokeDesktop(t, root, original)
	clientRoot := filepath.Join(root, "desktop-client")
	before, err := desktopHashes(clientRoot)
	if err != nil {
		t.Fatal(err)
	}
	request := domain.NewID()
	for _, args := range [][]string{{"device", "inspect-local"}, recoveryArgs(original, request)} {
		args = append(args, "--expected-endpoint", "http://127.0.0.1:1")
		if code, result := cliRun(t, root, args, ""); code == 0 || result["error"].(map[string]any)["code"] != "unsupported" {
			t.Fatal("native endpoint mismatch was not rejected", result)
		}
	}
	for _, path := range []string{"desktop-recovery.json", "desktop-recoveries", filepath.Join("pairing-codes", string(request)+".pending.json")} {
		if _, err := os.Lstat(filepath.Join(root, path)); !os.IsNotExist(err) {
			t.Fatal("endpoint mismatch wrote recovery state", path)
		}
	}
	after, err := desktopHashes(clientRoot)
	if err != nil || before != after {
		t.Fatal("endpoint mismatch changed the active credential", err)
	}
	if code, result := cliRun(t, root, []string{"device", "list"}, ""); code != 0 || len(result["result"].(map[string]any)["resources"].([]any)) != 1 {
		t.Fatal("endpoint mismatch created a replacement", result)
	}
	args := append(recoveryArgs(original, request), "--expected-endpoint", original.Endpoint)
	if code, result := cliRun(t, root, args, ""); code != 0 {
		t.Fatal("matching endpoint rejected", result)
	}
}

func TestDesktopRecoveryRetainsOriginalAndUsesOneReplacement(t *testing.T) {
	root, original := desktopFixture(t)
	clientRoot := filepath.Join(root, "desktop-client")
	before, err := desktopHashes(clientRoot)
	if err != nil {
		t.Fatal(err)
	}
	request := domain.NewID()
	args := recoveryArgs(original, request)
	if code, _ := cliRun(t, root, args, ""); code == 0 {
		t.Fatal("authorized desktop replaced")
	}
	revokeDesktop(t, root, original)
	if code, result := cliRun(t, root, []string{"device", "inspect-local"}, ""); code != 0 || result["result"].(map[string]any)["state"] != "revoked" {
		t.Fatal(result)
	}
	if code, _ := cliRun(t, root, []string{"device", "pair-local", "--device-dir", clientRoot}, ""); code == 0 {
		t.Fatal("implicit replacement")
	}
	for i := 0; i < 2; i++ {
		if code, result := cliRun(t, root, args, ""); code != 0 {
			t.Fatal(result)
		}
	}
	replacement, err := worker.LoadCredential(clientRoot)
	if err != nil {
		t.Fatal(err)
	}
	if replacement.DeviceID == original.DeviceID || replacement.Token == original.Token || replacement.ServerID != original.ServerID {
		t.Fatal("invalid replacement identity")
	}
	archived, err := desktopHashes(filepath.Join(root, "desktop-recoveries", string(request), "original"))
	if err != nil || before != archived {
		t.Fatal("original archive changed", err)
	}
	if code, result := cliRun(t, root, []string{"device", "list"}, ""); code != 0 || len(result["result"].(map[string]any)["resources"].([]any)) != 2 {
		t.Fatal("duplicate replacement", result)
	}
	if code, result := cliRun(t, root, []string{"device", "inspect-local"}, ""); code != 0 || result["result"].(map[string]any)["state"] != "authorized" {
		t.Fatal(result)
	}
	if code, result := cliRun(t, root, []string{"device", "pair-local", "--device-dir", clientRoot}, ""); code != 0 {
		t.Fatal(result)
	}
	// A prior completed recovery independently binds the current credential,
	// including scopes recovered before the first-pairing commitment existed.
	if err := os.Remove(filepath.Join(root, "desktop-registration.json")); err != nil {
		t.Fatal(err)
	}
	// Losing the final response cannot recreate the credential, even after the
	// replacement itself is later revoked.
	revokeDesktop(t, root, replacement)
	if code, _ := cliRun(t, root, args, ""); code == 0 {
		t.Fatal("revoked replacement reauthorized")
	}
	if code, result := cliRun(t, root, recoveryArgs(replacement, domain.NewID()), ""); code != 0 {
		t.Fatal(result)
	}
	if code, _ := cliRun(t, root, args, ""); code == 0 {
		t.Fatal("old request overwrote later replacement")
	}
}
func TestDesktopRecoveryInterruptedPublicationAndTampering(t *testing.T) {
	for _, prefix := range []int{0, 1, 2} {
		t.Run(string(rune('0'+prefix)), func(t *testing.T) {
			root, original := desktopFixture(t)
			revokeDesktop(t, root, original)
			request := domain.NewID()
			args := recoveryArgs(original, request)
			if code, result := cliRun(t, root, args, ""); code != 0 {
				t.Fatal(result)
			}
			record, err := loadDesktopRecovery(root)
			if err != nil {
				t.Fatal(err)
			}
			clientRoot := filepath.Join(root, "desktop-client")
			archive := filepath.Join(root, "desktop-recoveries", string(request), "original")
			// Reconstruct a process interruption after each atomic fixed-file publish.
			for _, i := range []int{0, 1} {
				if prefix == 1 && i == 1 {
					continue
				}
				if prefix == 2 {
					continue
				}
				raw, _, err := desktopFile(archive, desktopFiles[i])
				if err != nil {
					t.Fatal(err)
				}
				if err := security.WriteAtomic(filepath.Join(clientRoot, desktopFiles[i]), raw); err != nil {
					t.Fatal(err)
				}
				clear(raw)
			}
			record.Phase = recoveryPublishing
			if err := writeRecoveryJSON(filepath.Join(root, "desktop-recovery.json"), record); err != nil {
				t.Fatal(err)
			}
			if code, result := cliRun(t, root, []string{"device", "inspect-local"}, ""); code != 0 || result["result"].(map[string]any)["request_id"] != string(request) {
				t.Fatal(result)
			}
			if code, _ := cliRun(t, root, []string{"device", "pair-local", "--device-dir", clientRoot}, ""); code == 0 {
				t.Fatal("pending recovery bootstrapped")
			}
			if code, _ := cliRun(t, root, recoveryArgs(original, domain.NewID()), ""); code == 0 {
				t.Fatal("pending request replaced")
			}
			if code, result := cliRun(t, root, args, ""); code != 0 {
				t.Fatal(result)
			}
			// A foreign private file must remain untouched on a retry.
			if err := security.WriteAtomic(filepath.Join(clientRoot, "local-pairing.json"), []byte("foreign")); err != nil {
				t.Fatal(err)
			}
			if code, _ := cliRun(t, root, args, ""); code == 0 {
				t.Fatal("tampering repaired silently")
			}
			raw, _ := os.ReadFile(filepath.Join(clientRoot, "local-pairing.json"))
			if string(raw) != "foreign" {
				t.Fatal("foreign evidence overwritten")
			}
		})
	}
}
func TestDesktopRecoveryValidationAndMissingPairJournal(t *testing.T) {
	root, original := desktopFixture(t)
	revokeDesktop(t, root, original)
	request := domain.NewID()
	args := recoveryArgs(original, request)
	stale := append([]string{}, args...)
	stale[len(stale)-1] = "1"
	if code, _ := cliRun(t, root, stale, ""); code == 0 {
		t.Fatal("stale revision accepted")
	}
	if code, _ := cliRun(t, root, append([]string{"--server", original.Endpoint}, args...), ""); code == 0 {
		t.Fatal("remote override accepted")
	}
	if code, _ := cliRun(t, root, args[2:], ""); code == 0 {
		t.Fatal("implicit mutation identity accepted")
	}
	lock, err := security.TryLock(filepath.Join(root, "desktop-recovery.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := cliRun(t, root, args, ""); code == 0 {
		t.Fatal("concurrent recovery accepted")
	}
	lock.Close()
	if code, result := cliRun(t, root, args, ""); code != 0 {
		t.Fatal(result)
	}
	record, err := loadDesktopRecovery(root)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate loss of the original candidate journal after pairing acceptance.
	candidate := filepath.Join(root, "desktop-recoveries", string(request), "candidate")
	if err := os.Remove(filepath.Join(candidate, "device.json")); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(root, "desktop-recoveries", string(request), "original")
	for _, name := range desktopFiles[:2] {
		raw, _, err := desktopFile(archive, name)
		if err != nil {
			t.Fatal(err)
		}
		if err := security.WriteAtomic(filepath.Join(root, "desktop-client", name), raw); err != nil {
			t.Fatal(err)
		}
		clear(raw)
	}
	record.Phase = recoveryPairing
	record.Replacement = [3]string{}
	if err := writeRecoveryJSON(filepath.Join(root, "desktop-recovery.json"), record); err != nil {
		t.Fatal(err)
	}
	code, result := cliRun(t, root, args, "")
	if code == 0 || result["error"].(map[string]any)["code"] != "recovery_required" {
		t.Fatal(result)
	}
	output, _ := json.Marshal(result)
	if strings.Contains(string(output), original.Token) {
		t.Fatal("credential disclosure")
	}
	if code, result := cliRun(t, root, []string{"device", "list"}, ""); code != 0 || len(result["result"].(map[string]any)["resources"].([]any)) != 2 {
		t.Fatal("lost journal minted another device", result)
	}
}

func TestDesktopRecoveryLostPairingResponsesReuseOriginalIdentity(t *testing.T) {
	for _, method := range []string{"CreatePairing", "PairDevice"} {
		t.Run(method, func(t *testing.T) {
			var realEndpoint string
			var dropped, capture atomic.Bool
			var bodies [][]byte
			var bodiesLock sync.Mutex
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				if capture.Load() && strings.HasSuffix(r.URL.Path, "/"+method) {
					bodiesLock.Lock()
					bodies = append(bodies, append([]byte(nil), raw...))
					bodiesLock.Unlock()
				}
				forwarded, err := http.NewRequestWithContext(r.Context(), r.Method, realEndpoint+r.URL.RequestURI(), strings.NewReader(string(raw)))
				if err != nil {
					t.Error(err)
					return
				}
				forwarded.Header = r.Header.Clone()
				response, err := http.DefaultClient.Do(forwarded)
				if err != nil {
					t.Error(err)
					return
				}
				defer response.Body.Close()
				body, err := io.ReadAll(response.Body)
				if err != nil {
					t.Error(err)
					return
				}
				if capture.Load() && strings.HasSuffix(r.URL.Path, "/"+method) && response.StatusCode == 200 && dropped.CompareAndSwap(false, true) {
					// The server committed, but the client receives only an incomplete frame.
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("Content-Length", "1000")
					w.WriteHeader(200)
					w.Write([]byte("{"))
					return
				}
				for key, values := range response.Header {
					for _, value := range values {
						w.Header().Add(key, value)
					}
				}
				w.WriteHeader(response.StatusCode)
				w.Write(body)
			}))
			defer proxy.Close()
			// Pair through the proxy from the beginning so the original credential
			// and its independent commitment are never rewritten for fault injection.
			root, original := desktopFixtureEndpoint(t, func(root string, endpoint server.Endpoint) {
				realEndpoint = endpoint.URL
				endpoint.URL = proxy.URL
				if err := writeRecoveryJSON(filepath.Join(root, "server.json"), endpoint); err != nil {
					t.Fatal(err)
				}
			})
			revokeDesktop(t, root, original)
			capture.Store(true)
			request := domain.NewID()
			args := recoveryArgs(original, request)
			if code, _ := cliRun(t, root, args, ""); code == 0 {
				t.Fatal("lost response claimed success")
			}
			if !dropped.Load() {
				t.Fatal("response was not dropped")
			}
			if code, result := cliRun(t, root, args, ""); code != 0 {
				t.Fatal(result)
			}
			bodiesLock.Lock()
			defer bodiesLock.Unlock()
			if len(bodies) != 2 || string(bodies[0]) != string(bodies[1]) {
				t.Fatal("pairing retry changed original request")
			}
			if code, result := cliRun(t, root, []string{"device", "list"}, ""); code != 0 || len(result["result"].(map[string]any)["resources"].([]any)) != 2 {
				t.Fatal("lost response duplicated registration", result)
			}
		})
	}
}

func TestDesktopRecoveryRefusesMissingOwnerForeignTokenAndChangedArchive(t *testing.T) {
	root, original := desktopFixture(t)
	// A registered device with a different token is not currently authorized.
	changed := original
	changed.Token, _ = worker.RandomToken()
	if err := writeRecoveryJSON(filepath.Join(root, "desktop-client", "device.json"), changed); err != nil {
		t.Fatal(err)
	}
	if code, result := cliRun(t, root, []string{"device", "inspect-local"}, ""); code == 0 || result["error"].(map[string]any)["code"] != "unauthenticated" {
		t.Fatal("foreign token claimed authorization", result)
	}
	if err := writeRecoveryJSON(filepath.Join(root, "desktop-client", "device.json"), original); err != nil {
		t.Fatal(err)
	}
	revokeDesktop(t, root, original)
	ownerPath := filepath.Join(root, "owner.json")
	owner, err := security.ReadPrivate(ownerPath, 4096)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(owner)
	if err := os.Remove(ownerPath); err != nil {
		t.Fatal(err)
	}
	args := recoveryArgs(original, domain.NewID())
	if code, result := cliRun(t, root, args, ""); code == 0 || result["error"].(map[string]any)["code"] != "unauthenticated" {
		t.Fatal("missing owner accepted", result)
	}
	if _, err := os.Lstat(filepath.Join(root, "desktop-recovery.json")); !os.IsNotExist(err) {
		t.Fatal("missing owner wrote intent")
	}
	if err := security.WriteAtomic(ownerPath, owner); err != nil {
		t.Fatal(err)
	}
	if code, result := cliRun(t, root, args, ""); code != 0 {
		t.Fatal(result)
	}
	replacement, err := worker.LoadCredential(filepath.Join(root, "desktop-client"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "desktop-recoveries", args[1], "original", "device.json")
	if err := security.WriteAtomic(path, []byte("altered archive")); err != nil {
		t.Fatal(err)
	}
	if code, _ := cliRun(t, root, args, ""); code == 0 {
		t.Fatal("altered archive accepted")
	}
	retained, err := worker.LoadCredential(filepath.Join(root, "desktop-client"))
	if err != nil || retained != replacement {
		t.Fatal("failed recovery changed active client", err)
	}
}

func TestDesktopRecoveryNeverRecreatesLostGrantOwnership(t *testing.T) {
	root, original := desktopFixture(t)
	revokeDesktop(t, root, original)
	request := domain.NewID()
	args := recoveryArgs(original, request)
	if code, result := cliRun(t, root, args, ""); code != 0 {
		t.Fatal(result)
	}
	record, err := loadDesktopRecovery(root)
	if err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(root, "desktop-recoveries", string(request), "original")
	for _, name := range desktopFiles[:2] {
		raw, _, err := desktopFile(archive, name)
		if err != nil {
			t.Fatal(err)
		}
		if err := security.WriteAtomic(filepath.Join(root, "desktop-client", name), raw); err != nil {
			t.Fatal(err)
		}
		clear(raw)
	}
	record.Phase = recoveryGrant
	record.Replacement = [3]string{}
	if err := writeRecoveryJSON(filepath.Join(root, "desktop-recovery.json"), record); err != nil {
		t.Fatal(err)
	}
	pending := filepath.Join(root, "pairing-codes", string(request)+".pending.json")
	if err := os.Remove(pending); err != nil {
		t.Fatal(err)
	}
	if code, result := cliRun(t, root, args, ""); code == 0 || result["error"].(map[string]any)["code"] != "recovery_required" {
		t.Fatal(result)
	}
	if _, err := os.Lstat(pending); !os.IsNotExist(err) {
		t.Fatal("lost grant intent recreated")
	}
}

func TestDesktopRecoveryPreservesCompletedReceiptOwnership(t *testing.T) {
	root, original := desktopFixture(t)
	revokeDesktop(t, root, original)
	request := domain.NewID()
	args := recoveryArgs(original, request)
	if code, result := cliRun(t, root, args, ""); code != 0 {
		t.Fatal(result)
	}
	replacement, err := worker.LoadCredential(filepath.Join(root, "desktop-client"))
	if err != nil {
		t.Fatal(err)
	}
	receipt := filepath.Join(root, "desktop-recoveries", string(request), "receipt.json")
	if err := os.Remove(receipt); err != nil {
		t.Fatal(err)
	}
	if code, _ := cliRun(t, root, args, ""); code == 0 {
		t.Fatal("lost completion receipt reconstructed")
	}
	revokeDesktop(t, root, replacement)
	if code, _ := cliRun(t, root, recoveryArgs(replacement, domain.NewID()), ""); code == 0 {
		t.Fatal("new recovery discarded missing receipt history")
	}
	if _, err := os.Lstat(receipt); !os.IsNotExist(err) {
		t.Fatal("missing receipt recreated")
	}
	retained, err := worker.LoadCredential(filepath.Join(root, "desktop-client"))
	if err != nil || retained != replacement {
		t.Fatal("completed identity changed", err)
	}
}
