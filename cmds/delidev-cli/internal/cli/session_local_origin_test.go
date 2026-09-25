package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type localOriginStatus struct {
	delidevv1connect.UnimplementedSystemServiceHandler
	server   domain.ID
	protocol uint32
	calls    int
}

func (s *localOriginStatus) GetStatus(_ context.Context, r *connect.Request[pb.GetStatusRequest]) (*connect.Response[pb.GetStatusResponse], error) {
	s.calls++
	if r.Header().Get("Authorization") != "Bearer owner-fixture" {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}
	return connect.NewResponse(&pb.GetStatusResponse{ServerId: string(s.server), ProtocolVersion: s.protocol}), nil
}

func TestCLILocalCreationLoadsOnlyMatchingPrivateWorkerScope(t *testing.T) {
	for _, scenario := range []string{"valid", "missing-flag", "nonlocal", "missing-file", "client", "machine", "endpoint", "server", "protocol"} {
		t.Run(scenario, func(t *testing.T) {
			status := &localOriginStatus{server: domain.NewID(), protocol: rpc.ProtocolVersion}
			_, handler := delidevv1connect.NewSystemServiceHandler(status)
			h := httptest.NewServer(handler)
			defer h.Close()
			c := client{endpoint: h.URL, token: "owner-fixture", system: delidevv1connect.NewSystemServiceClient(http.DefaultClient, h.URL)}
			token, err := worker.RandomToken()
			if err != nil {
				t.Fatal(err)
			}
			credential := worker.Credential{Version: 1, Type: domain.WorkerDevice, Endpoint: h.URL, ServerID: status.server, DeviceID: domain.NewID(), MachineID: domain.NewID(), PairingID: domain.NewID(), Token: token}
			input := domain.CreateSession{Workspace: domain.Local, MachineID: credential.MachineID}
			root := filepath.Join(t.TempDir(), "worker")
			if err := security.PrivateDir(root); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "client":
				credential.Type, credential.MachineID = domain.ClientDevice, ""
			case "machine":
				credential.MachineID = domain.NewID()
			case "endpoint":
				credential.Endpoint = "http://127.0.0.1:1"
			case "server":
				credential.ServerID = domain.NewID()
			case "protocol":
				status.protocol++
			case "nonlocal":
				input.Workspace = domain.Worktree
			}
			raw, _ := json.Marshal(credential)
			if scenario != "missing-file" {
				if err := security.WriteAtomic(filepath.Join(root, "device.json"), raw); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "missing-flag" {
				root = ""
			}
			got, err := localCreationCredential(context.Background(), c, input, root)
			if scenario == "valid" {
				if err != nil || got != token || status.calls != 1 {
					t.Fatal("valid Local scope refused", err)
				}
			} else {
				if err == nil || got != "" {
					t.Fatal("foreign Local scope leaked authority")
				}
				if scenario != "server" && scenario != "protocol" && status.calls != 0 {
					t.Fatal("invalid local scope reached network")
				}
			}
		})
	}
}
