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

type inspectionAttachFixture struct {
	delidevv1connect.UnimplementedWorkerServiceHandler
	sync.Mutex
	serverID domain.ID
	cancel   context.CancelFunc
	support  []bool
	requests []*pb.AttachWorkerRequest
}

func (f *inspectionAttachFixture) AttachWorker(_ context.Context, req *connect.Request[pb.AttachWorkerRequest]) (*connect.Response[pb.AttachWorkerResponse], error) {
	f.Lock()
	defer f.Unlock()
	f.requests = append(f.requests, req.Msg)
	response := &pb.AttachWorkerResponse{ServerId: string(f.serverID)}
	if f.support[len(f.requests)-1] {
		response.SupportedWorkerCapabilities = []pb.WorkerCapability{pb.WorkerCapability_WORKER_CAPABILITY_REPOSITORY_INSPECTION_METADATA_V1}
	}
	if len(f.requests) == len(f.support) {
		f.cancel()
	}
	return connect.NewResponse(response), nil
}

func (f *inspectionAttachFixture) WatchWork(context.Context, *connect.Request[pb.WatchWorkRequest], *connect.ServerStream[pb.WatchWorkResponse]) error {
	return connect.NewError(connect.CodeUnavailable, nil)
}

func TestInspectionMetadataNegotiationPreservesInitialAttachmentAndRequestIdentity(t *testing.T) {
	metadata := pb.WorkerCapability_WORKER_CAPABILITY_REPOSITORY_INSPECTION_METADATA_V1
	for _, tc := range []struct {
		name        string
		support     []bool
		requested   bool
		sameRequest bool
	}{
		{"legacy", []bool{false, false, false, false}, false, true},
		{"supported", []bool{true, true, true, true}, true, true},
		{"support-removed", []bool{true, true, false, false}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			fixture := &inspectionAttachFixture{serverID: domain.NewID(), cancel: cancel, support: tc.support}
			_, handler := delidevv1connect.NewWorkerServiceHandler(fixture)
			server := httptest.NewServer(handler)
			defer server.Close()
			credential := Credential{Endpoint: server.URL, ServerID: fixture.serverID, MachineID: domain.NewID()}
			if err := runConnected(ctx, Config{Root: t.TempDir(), Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}, credential); err != nil {
				t.Fatal(err)
			}
			fixture.Lock()
			defer fixture.Unlock()
			if len(fixture.requests) != 4 {
				t.Fatal("negotiation did not reach its original reconnect")
			}
			first, negotiated, reattach, renegotiated := fixture.requests[0], fixture.requests[1], fixture.requests[2], fixture.requests[3]
			if slices.Contains(first.Capabilities, metadata) || slices.Contains(reattach.Capabilities, metadata) {
				t.Fatal("preliminary attachment advertised metadata without server support")
			}
			if slices.Contains(negotiated.Capabilities, metadata) != tc.requested || slices.Contains(renegotiated.Capabilities, metadata) != tc.support[2] {
				t.Fatal("second attachment did not follow current server support")
			}
			if (negotiated.RequestId == renegotiated.RequestId) != tc.sameRequest {
				t.Fatal("retained negotiation request identity ignored its support profile")
			}
			for _, request := range fixture.requests {
				if request.InstanceId != first.InstanceId || !slices.Contains(request.Capabilities, pb.WorkerCapability_WORKER_CAPABILITY_SESSION_FORWARDING_V1) || !slices.Contains(request.Capabilities, pb.WorkerCapability_WORKER_CAPABILITY_SESSION_TERMINALS_V1) {
					t.Fatal("metadata negotiation replaced existing process authority")
				}
			}
		})
	}
}
