// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type terminalAttachFixture struct {
	delidevv1connect.UnimplementedWorkerServiceHandler
	sync.Mutex
	serverID domain.ID
	cancel   context.CancelFunc
	requests []*pb.AttachWorkerRequest
}

func (f *terminalAttachFixture) AttachWorker(_ context.Context, req *connect.Request[pb.AttachWorkerRequest]) (*connect.Response[pb.AttachWorkerResponse], error) {
	f.Lock()
	defer f.Unlock()
	f.requests = append(f.requests, req.Msg)
	if len(f.requests) == 3 {
		f.cancel()
	}
	return connect.NewResponse(&pb.AttachWorkerResponse{ServerId: string(f.serverID)}), nil
}

func (f *terminalAttachFixture) WatchWork(context.Context, *connect.Request[pb.WatchWorkRequest], *connect.ServerStream[pb.WatchWorkResponse]) error {
	return connect.NewError(connect.CodeUnavailable, nil)
}

func TestTerminalCapabilitySurvivesPreliminaryReconnectAttach(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	f := &terminalAttachFixture{serverID: domain.NewID(), cancel: cancel}
	_, handler := delidevv1connect.NewWorkerServiceHandler(f)
	server := httptest.NewServer(handler)
	defer server.Close()
	credential := Credential{Endpoint: server.URL, ServerID: f.serverID, MachineID: domain.NewID()}
	if err := runConnected(ctx, Config{Root: t.TempDir(), Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}, credential); err != nil {
		t.Fatal(err)
	}
	f.Lock()
	defer f.Unlock()
	if len(f.requests) != 3 {
		t.Fatal("Worker did not reconnect after its first negotiated attach")
	}
	for _, req := range f.requests {
		if !slices.Contains(req.Capabilities, pb.WorkerCapability_WORKER_CAPABILITY_SESSION_TERMINALS_V1) || !slices.Contains(req.Capabilities, pb.WorkerCapability_WORKER_CAPABILITY_SESSION_FORWARDING_V1) {
			t.Fatal("attach removed process-owned terminal or forwarding support")
		}
		if slices.Contains(req.Capabilities, pb.WorkerCapability_WORKER_CAPABILITY_AUTOMATIC_TITLES_CODEX_V1) {
			t.Fatal("attach advertised an unverified title capability")
		}
		if req.InstanceId != f.requests[0].InstanceId {
			t.Fatal("reconnect replaced original process identity")
		}
	}
}
