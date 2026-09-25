package connections_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/connections"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func TestProfileWorkersStayDistinctAndNeverReplaceRevokedOrLostAuthority(t *testing.T) {
	f, other := start(t), start(t)
	root, ctx := filepath.Join(t.TempDir(), "client"), context.Background()
	first, second := domain.NewID(), domain.NewID()
	for id, server := range map[domain.ID]fixture{first: f, second: other} {
		if _, err := connections.Pair(ctx, root, id, "fixture", grant(t, server)); err != nil {
			t.Fatal(err)
		}
		if _, err := connections.WorkerCredential(root, id); err == nil {
			t.Fatal("unregistered Worker inspection succeeded")
		}
		path, _ := connections.WorkerRoot(root, id)
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatal("inspection created Worker state")
		}
	}
	a, err := connections.RegisterWorker(ctx, root, first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := connections.RegisterWorker(ctx, root, second)
	if err != nil || a.ServerID == b.ServerID || a.DeviceID == b.DeviceID || a.MachineID == b.MachineID || a.Token == b.Token {
		t.Fatal("profile Worker identities were shared", err)
	}
	repeated, err := connections.RegisterWorker(ctx, root, first)
	if err != nil || repeated != a {
		t.Fatal("registration replaced Worker", err)
	}
	client, _ := worker.LoadCredential(filepath.Join(root, "connections", string(first), "client"))
	if client.Token == a.Token || client.DeviceID == a.DeviceID {
		t.Fatal("Worker borrowed client authority")
	}
	if _, err := f.devices.RevokeDevice(ctx, owner(f, &pb.RevokeDeviceRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(a.DeviceID), ExpectedRevision: 1}})); err != nil {
		t.Fatal(err)
	}
	if _, err := connections.RegisterWorker(ctx, root, first); domain.SafeError(err).Code != domain.Unauthenticated {
		t.Fatal("revoked Worker registration was reported healthy", err)
	}
	retained, err := connections.WorkerCredential(root, first)
	if err != nil || retained != a {
		t.Fatal("revocation destroyed offline inspection/stop authority", err)
	}
	path, _ := connections.WorkerRoot(root, first)
	if err := os.Remove(filepath.Join(path, "device.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := connections.RegisterWorker(ctx, root, first); err == nil {
		t.Fatal("missing completed credential replaced")
	}
	if _, err := os.Stat(filepath.Join(path, "device.json")); !os.IsNotExist(err) {
		t.Fatal("registration recreated missing token")
	}
	for _, name := range []string{"owner.json", "server.json", "state.sqlite", "worker"} {
		if _, err := os.Lstat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatal("profile initialized default authority")
		}
	}
}

func TestProfileWorkerRetriesBothLostAcknowledgmentsWithOriginalBytes(t *testing.T) {
	for _, loseJournal := range []bool{false, true} {
		t.Run(map[bool]string{false: "exact replay", true: "lost journal"}[loseJournal], func(t *testing.T) {
			f := start(t)
			root, id, ctx := filepath.Join(t.TempDir(), "client"), domain.NewID(), context.Background()
			httpClient, transport := rpc.HTTPClient()
			defer transport.CloseIdleConnections()
			var mu sync.Mutex
			armed := false
			requests := map[string][][]byte{}
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
				forward, _ := http.NewRequestWithContext(r.Context(), r.Method, f.endpoint.URL+r.URL.Path, bytes.NewReader(raw))
				forward.Header = r.Header.Clone()
				response, err := httpClient.Do(forward)
				if err != nil {
					w.WriteHeader(http.StatusBadGateway)
					return
				}
				defer response.Body.Close()
				body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
				mu.Lock()
				lose := false
				if armed && (r.URL.Path == delidevv1connect.DeviceServiceCreatePairingProcedure || r.URL.Path == delidevv1connect.DeviceServicePairDeviceProcedure) {
					requests[r.URL.Path] = append(requests[r.URL.Path], raw)
					lose = len(requests[r.URL.Path]) == 1
				}
				mu.Unlock()
				if lose {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
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
			code := grant(t, f)
			code.Endpoint = proxy.URL
			if _, err := connections.Pair(ctx, root, id, "fixture", code); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			armed = true
			mu.Unlock()
			for i := 0; i < 2; i++ {
				if _, err := connections.RegisterWorker(ctx, root, id); err == nil {
					t.Fatal("lost acknowledgment reported complete")
				}
			}
			if loseJournal {
				path, _ := connections.WorkerRoot(root, id)
				if err := os.Remove(filepath.Join(path, "pairing-pending.json")); err != nil {
					t.Fatal(err)
				}
			}
			credential, err := connections.RegisterWorker(ctx, root, id)
			if loseJournal && domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatal("lost journal recreated", err)
			} else if !loseJournal && (err != nil || credential.MachineID == "") {
				t.Fatal("exact registration did not complete", err)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(requests) != 2 {
				t.Fatal("both remote acceptance boundaries were not exercised")
			}
			for procedure, values := range requests {
				expected := 2
				if loseJournal && procedure == delidevv1connect.DeviceServicePairDeviceProcedure {
					expected = 1
				}
				if len(values) != expected || (len(values) == 2 && !bytes.Equal(values[0], values[1])) {
					t.Fatal("retry changed grant or device request ownership")
				}
			}
		})
	}
}

func TestProfileWorkerRejectsForeignPrivateRegistrationAndClientRevocation(t *testing.T) {
	f := start(t)
	root, id, ctx := filepath.Join(t.TempDir(), "client"), domain.NewID(), context.Background()
	profile, err := connections.Pair(ctx, root, id, "fixture", grant(t, f))
	if err != nil {
		t.Fatal(err)
	}
	credential, err := connections.RegisterWorker(ctx, root, id)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "connections", string(id), "local-worker.json")
	original, _ := os.ReadFile(path)
	var changed map[string]any
	if err := json.Unmarshal(original, &changed); err != nil {
		t.Fatal(err)
	}
	changed["client_id"] = domain.NewID()
	raw, _ := json.Marshal(changed)
	if err := security.WriteAtomic(path, raw); err != nil {
		t.Fatal(err)
	}
	if _, err := connections.WorkerCredential(root, id); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("foreign client pin accepted", err)
	}
	if err := security.WriteAtomic(path, original); err != nil {
		t.Fatal(err)
	}
	if _, err := f.devices.RevokeDevice(ctx, owner(f, &pb.RevokeDeviceRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(profile.DeviceID), ExpectedRevision: 1}})); err != nil {
		t.Fatal(err)
	}
	if _, err := connections.RegisterWorker(ctx, root, id); domain.SafeError(err).Code != domain.Unauthenticated {
		t.Fatal("revoked client registered a Worker", err)
	}
	retained, err := connections.WorkerCredential(root, id)
	if err != nil || retained != credential {
		t.Fatal("client revocation changed offline Worker identity", err)
	}
}
