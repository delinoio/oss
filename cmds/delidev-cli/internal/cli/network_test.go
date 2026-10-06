// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"connectrpc.com/connect"
	"context"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/server"
)

func TestCLINetworkProfilesSelectionAndExactRetry(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- server.Serve(ctx, server.Config{DisableBackgroundMaintenanceForTesting: true, DataDir: root, Listen: "127.0.0.1:0", Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}, func(server.Endpoint) { close(ready) })
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
		t.Fatal("server readiness")
	}
	code, value := cliRun(t, root, []string{"network", "status"}, "")
	if code != 0 || value["result"].(map[string]any)["mode"] != "direct" {
		t.Fatal("default", value)
	}
	requestID := string(domain.NewID())
	args := []string{"network", "profile", "save", "--request-id", requestID, "--input", "-"}
	raw := `{"name":"Direct profile","mode":"direct"}`
	code, value = cliRun(t, root, args, raw)
	if code != 0 {
		t.Fatal("save", value)
	}
	id := value["result"].(map[string]any)["resource"].(map[string]any)["id"].(string)
	code, value = cliRun(t, root, args, raw)
	if code != 0 || value["result"].(map[string]any)["replayed"] != true {
		t.Fatal("retry", value)
	}
	for _, args := range [][]string{{"network", "profile", "get", "--id", id}, {"network", "profile", "list"}} {
		if code, value := cliRun(t, root, args, ""); code != 0 {
			t.Fatal("read", value)
		}
	}
	code, value = cliRun(t, root, []string{"network", "select", "--profile-id", id, "--profile-revision", "1"}, "")
	if code != 0 {
		t.Fatal("selection", value)
	}
	route := value["result"].(map[string]any)["resource"].(map[string]any)["id"].(string)
	if code, value := cliRun(t, root, []string{"network", "profile", "delete", "--id", id, "--revision", "1"}, ""); code == 0 {
		t.Fatal("deleted selected", value)
	}
	if code, value := cliRun(t, root, []string{"network", "select", "--id", route, "--revision", "1"}, ""); code != 0 {
		t.Fatal("Direct selection", value)
	}
	// Shared stdin cannot ambiguously carry definition, server token and proxy
	// credentials. Reject it locally before any secret/native mutation.
	if code, value := cliRun(t, root, []string{"network", "profile", "save", "--input", "-", "--credential-stdin"}, raw); code == 0 {
		t.Fatal("ambiguous stdin", value)
	}
}

func TestCLINetworkWaitsForBoundedCredentialResponse(t *testing.T) {
	// Exercise the actual response-header wait beyond the default 15 seconds.
	// Credential-store latency must not lose the original durable acknowledgment.
	requestID := domain.NewID()
	endpoint := httptest.NewServer(connect.NewUnaryHandler(delidevv1connect.NetworkServiceSaveNetworkProfileProcedure, func(ctx context.Context, req *connect.Request[pb.SaveNetworkProfileRequest]) (*connect.Response[pb.SaveNetworkProfileResponse], error) {
		if req.Msg.Mutation.RequestId != string(requestID) {
			t.Error("lost original request identity")
		}
		select {
		case <-time.After(16 * time.Second):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return connect.NewResponse(&pb.SaveNetworkProfileResponse{RequestId: string(requestID), Resource: &pb.Resource{Id: string(requestID)}}), nil
	}))
	defer endpoint.Close()
	dir := t.TempDir()
	input := filepath.Join(dir, "definition.json")
	if err := os.WriteFile(input, []byte(`{"name":"Direct","mode":"direct"}`), 0600); err != nil {
		t.Fatal(err)
	}
	var output, diagnostic strings.Builder
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	code := Run(ctx, []string{"--data-dir", filepath.Join(dir, "client"), "--server", endpoint.URL, "--token-stdin", "--request-id", string(requestID), "network", "profile", "save", "--input", input}, IO{In: strings.NewReader("isolated-fixture-token"), Out: &output, Err: &diagnostic})
	if code != 0 {
		t.Fatalf("bounded network acknowledgment was abandoned: %d %s", code, output.String())
	}
}
