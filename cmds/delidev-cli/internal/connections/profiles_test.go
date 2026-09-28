package connections_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/connections"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/server"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type fixture struct {
	endpoint server.Endpoint
	identity security.Identity
	devices  delidevv1connect.DeviceServiceClient
}

func start(t *testing.T) fixture {
	t.Helper()
	root := filepath.Join(t.TempDir(), "server")
	ctx, stop := context.WithCancel(context.Background())
	ready := make(chan server.Endpoint, 1)
	done := make(chan error, 1)
	go func() {
		done <- server.Serve(ctx, server.Config{DataDir: root, Listen: "127.0.0.1:0", Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}, func(endpoint server.Endpoint) { ready <- endpoint })
	}()
	var f fixture
	select {
	case f.endpoint = <-ready:
	case err := <-done:
		stop()
		t.Fatal(err)
	case <-time.After(10 * time.Second):
		stop()
		t.Fatal("fixture startup timed out")
	}
	t.Cleanup(func() {
		stop()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	var err error
	f.identity, err = security.LoadIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	f.devices = delidevv1connect.NewDeviceServiceClient(http.DefaultClient, f.endpoint.URL)
	return f
}
func owner[T any](f fixture, message *T) *connect.Request[T] {
	request := connect.NewRequest(message)
	request.Header().Set("Authorization", "Bearer "+f.identity.Token)
	return request
}
func grant(t *testing.T, f fixture) worker.PairingCode {
	t.Helper()
	code, err := worker.RandomToken()
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(code))
	response, err := f.devices.CreatePairing(context.Background(), owner(f, &pb.CreatePairingRequest{RequestId: string(domain.NewID()), Name: "saved client", Type: pb.DeviceType_DEVICE_TYPE_CLIENT, CodeDigest: digest[:]}))
	if err != nil {
		t.Fatal(err)
	}
	return worker.PairingCode{Version: 1, ServerID: f.identity.ServerID, PairingID: domain.ID(response.Msg.Pairing.Id), Endpoint: f.endpoint.URL, Code: code}
}
func TestSavedConnectionsPairVerifyRevokeAndNeverReplaceLostCredentials(t *testing.T) {
	f := start(t)
	root := filepath.Join(t.TempDir(), "desktop")
	id := domain.NewID()
	ctx := context.Background()
	if profiles, err := connections.List(root); err != nil || len(profiles) != 0 {
		t.Fatal("missing inventory was not empty", err)
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatal("list created state")
	}
	original := grant(t, f)
	metadata, err := connections.Pair(ctx, root, id, "Personal server", original)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.State != connections.Paired || metadata.ServerID != f.identity.ServerID || metadata.DeviceID == "" {
		t.Fatalf("bad paired metadata: %+v", metadata)
	}
	profiles, err := connections.List(root)
	if err != nil || len(profiles) != 1 || profiles[0] != metadata {
		t.Fatal("saved inventory mismatch", err)
	}
	verified, err := connections.Verify(ctx, root, id)
	if err != nil || verified.Profile != metadata || verified.ServerVersion != rpc.Version {
		t.Fatal("server verification failed", err)
	}
	for _, name := range []string{"owner.json", "server.json", "state.sqlite", "worker"} {
		if _, err := os.Lstat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatal("profile initialized server/Worker authority")
		}
	}
	for _, result := range []any{metadata, profiles, verified} {
		raw, _ := json.Marshal(result)
		if bytes.Contains(raw, []byte(original.Code)) || bytes.Contains(raw, []byte(f.identity.Token)) {
			t.Fatal("grant/owner credential leaked")
		}
	}
	repeated, err := connections.Pair(ctx, root, id, "Personal server", original)
	if err != nil || repeated != metadata {
		t.Fatal("exact pair retry changed device", err)
	}
	different := original
	different.Code, _ = worker.RandomToken()
	if _, err := connections.Pair(ctx, root, id, "Personal server", different); domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("rebound original grant", err)
	}
	response, err := f.devices.RevokeDevice(ctx, owner(f, &pb.RevokeDeviceRequest{Mutation: &pb.Mutation{Id: string(metadata.DeviceID), ExpectedRevision: 1, RequestId: string(domain.NewID())}}))
	if err != nil || response.Msg.Device == nil {
		t.Fatal(err)
	}
	if _, err := connections.Verify(ctx, root, id); domain.SafeError(err).Code != domain.Unauthenticated {
		t.Fatal("revoked connection verified", err)
	}
	retained, err := connections.Inspect(root, id)
	if err != nil || retained != metadata {
		t.Fatal("failed verification replaced metadata", err)
	}
	credentialPath := filepath.Join(root, "connections", string(id), "client", "device.json")
	if err := os.Remove(credentialPath); err != nil {
		t.Fatal(err)
	}
	if _, err := connections.Retry(ctx, root, id); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("missing credential recreated", err)
	}
	if _, err := os.Stat(credentialPath); !os.IsNotExist(err) {
		t.Fatal("lost device silently replaced")
	}
}
func TestSavedConnectionRetriesLostPairResponseWithOriginalIdentity(t *testing.T) {
	f := start(t)
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "desktop")
	id := domain.NewID()
	httpClient, transport := rpc.HTTPClient()
	defer transport.CloseIdleConnections()
	var mu sync.Mutex
	var requests [][]byte
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			w.WriteHeader(500)
			return
		}
		forwarded, _ := http.NewRequestWithContext(r.Context(), r.Method, f.endpoint.URL+r.URL.Path, bytes.NewReader(raw))
		forwarded.Header = r.Header.Clone()
		response, err := httpClient.Do(forwarded)
		if err != nil {
			w.WriteHeader(502)
			return
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		if r.URL.Path == delidevv1connect.DeviceServicePairDeviceProcedure {
			mu.Lock()
			requests = append(requests, raw)
			first := len(requests) == 1
			mu.Unlock()
			if first {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
		}
		for name, values := range response.Header {
			for _, value := range values {
				w.Header().Add(name, value)
			}
		}
		w.WriteHeader(response.StatusCode)
		_, _ = w.Write(body)
	}))
	defer proxy.Close()
	original := grant(t, f)
	original.Endpoint = proxy.URL
	if _, err := connections.Pair(ctx, root, id, "Lost response", original); err == nil {
		t.Fatal("lost response accepted locally")
	}
	pending, err := connections.Inspect(root, id)
	if err != nil || pending.State != connections.Pending {
		t.Fatal("original pending intent lost", err)
	}
	completed, err := connections.Retry(ctx, root, id)
	if err != nil || completed.State != connections.Paired {
		t.Fatal("original retry failed", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 || !bytes.Equal(requests[0], requests[1]) {
		t.Fatal("pair retry changed original request/token identity")
	}
	credential, err := worker.LoadCredential(filepath.Join(root, "connections", string(id), "client"))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(completed)
	if bytes.Contains(raw, []byte(credential.Token)) {
		t.Fatal("paired device token exposed in metadata")
	}
}
func TestSavedConnectionRejectsUnsafeOriginsAndDamagedIntentWithoutInitialization(t *testing.T) {
	code, _ := worker.RandomToken()
	base := worker.PairingCode{Version: 1, ServerID: domain.NewID(), PairingID: domain.NewID(), Code: code}
	for _, endpoint := range []string{"http://example.test", "https://example.test/%2F", "https://example.test?", "https://user:secret@example.test", "https://example.test/path", "https://example.test:0", "https://example.test/#", "https://EXAMPLE.test", "https://127.1", "https://0x7f000001", "https://example.test:0443", "https://example.test;script-src"} {
		root := filepath.Join(t.TempDir(), "absent")
		value := base
		value.Endpoint = endpoint
		if _, err := connections.Pair(context.Background(), root, domain.NewID(), "fixture", value); err == nil {
			t.Fatalf("unsafe origin accepted: %s", endpoint)
		}
		if _, err := os.Lstat(root); !os.IsNotExist(err) {
			t.Fatal("invalid input created scope")
		}
	}
	root := filepath.Join(t.TempDir(), "absent")
	if _, err := connections.Retry(context.Background(), root, domain.NewID()); err == nil {
		t.Fatal("missing retry succeeded")
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatal("retry initialized missing state")
	}
	root = filepath.Join(t.TempDir(), "desktop")
	if err := security.PrivateDir(root); err != nil {
		t.Fatal(err)
	}
	if err := security.PrivateDir(filepath.Join(root, "connections")); err != nil {
		t.Fatal(err)
	}
	id := domain.NewID()
	if err := security.PrivateDir(filepath.Join(root, "connections", string(id))); err != nil {
		t.Fatal(err)
	}
	base.Endpoint = "https://example.test"
	if _, err := connections.Pair(context.Background(), root, id, "fixture", base); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("adopted unrecognized partial intent", err)
	}
	if _, err := os.Stat(filepath.Join(root, "connections", string(id), "connection.json")); !os.IsNotExist(err) {
		t.Fatal("damaged record repaired")
	}
}

func TestSavedConnectionRetryRequiresRetainedWorkerPairOwnership(t *testing.T) {
	closed := httptest.NewServer(http.NotFoundHandler())
	endpoint := closed.URL
	closed.Close()
	code, _ := worker.RandomToken()
	original := worker.PairingCode{Version: 1, ServerID: domain.NewID(), PairingID: domain.NewID(), Endpoint: endpoint, Code: code}
	root := filepath.Join(t.TempDir(), "desktop")
	id := domain.NewID()
	if _, err := connections.Pair(context.Background(), root, id, "Offline fixture", original); err == nil {
		t.Fatal("offline server accepted pairing")
	}
	pendingPath := filepath.Join(root, "connections", string(id), "client", "pairing-pending.json")
	raw, err := os.ReadFile(pendingPath)
	if err != nil {
		t.Fatal(err)
	}
	var pending map[string]any
	if err := json.Unmarshal(raw, &pending); err != nil {
		t.Fatal(err)
	}
	pending["credential"].(map[string]any)["server_id"] = string(domain.NewID())
	changed, _ := json.Marshal(pending)
	if err := security.WriteAtomic(pendingPath, changed); err != nil {
		t.Fatal(err)
	}
	if _, err := connections.Retry(context.Background(), root, id); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("inconsistent pending ownership reached network", err)
	}
	if err := os.Remove(pendingPath); err != nil {
		t.Fatal(err)
	}
	if _, err := connections.Retry(context.Background(), root, id); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("lost original journal recreated", err)
	}
	if _, err := os.Stat(pendingPath); !os.IsNotExist(err) {
		t.Fatal("retry minted a new request/token")
	}
}
func TestSavedConnectionInventoryBoundAndForeignCredentialRefusal(t *testing.T) {
	root := filepath.Join(t.TempDir(), "desktop")
	if err := security.PrivateDir(root); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(root, "connections")
	if err := security.PrivateDir(parent); err != nil {
		t.Fatal(err)
	}
	code, _ := worker.RandomToken()
	original := worker.PairingCode{Version: 1, ServerID: domain.NewID(), PairingID: domain.NewID(), Endpoint: "https://example.test", Code: code}
	for i := 0; i < connections.MaxProfiles; i++ {
		id := domain.NewID()
		path := filepath.Join(parent, string(id))
		if err := security.PrivateDir(path); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(map[string]any{"version": 1, "id": id, "name": "fixture", "grant": original, "created_at": time.Now().UTC()})
		if err := security.WriteAtomic(filepath.Join(path, "connection.json"), raw); err != nil {
			t.Fatal(err)
		}
	}
	profiles, err := connections.List(root)
	if err != nil || len(profiles) != connections.MaxProfiles {
		t.Fatal("bounded inventory unreadable", err)
	}
	if _, err := connections.Pair(context.Background(), root, domain.NewID(), "extra", original); domain.SafeError(err).Code != domain.ResourceExhausted {
		t.Fatal("profile cap ignored", err)
	}
	path := filepath.Join(parent, string(profiles[0].ID), "client")
	if err := security.PrivateDir(path); err != nil {
		t.Fatal(err)
	}
	token, _ := worker.RandomToken()
	credential := worker.Credential{Version: 1, Type: domain.ClientDevice, Endpoint: original.Endpoint, ServerID: domain.NewID(), PairingID: original.PairingID, DeviceID: domain.NewID(), Token: token}
	raw, _ := json.Marshal(credential)
	if err := security.WriteAtomic(filepath.Join(path, "device.json"), raw); err != nil {
		t.Fatal(err)
	}
	if _, err := connections.Inspect(root, profiles[0].ID); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("foreign credential accepted", err)
	}
	if _, err := connections.List(root); err == nil {
		t.Fatal("broken profile silently omitted from inventory")
	}
}
