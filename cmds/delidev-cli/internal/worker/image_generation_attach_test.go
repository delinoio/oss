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

type imageGenerationAttachFixture struct {
	delidevv1connect.UnimplementedWorkerServiceHandler
	sync.Mutex
	serverID domain.ID
	cancel   context.CancelFunc
	support  []bool
	requests []*pb.AttachWorkerRequest
}

func (f *imageGenerationAttachFixture) AttachWorker(_ context.Context, req *connect.Request[pb.AttachWorkerRequest]) (*connect.Response[pb.AttachWorkerResponse], error) {
	f.Lock()
	defer f.Unlock()
	if req.Header().Get("Authorization") != "Bearer isolated-image-attachment" {
		f.cancel()
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}
	index := len(f.requests)
	if index >= len(f.support) {
		f.cancel()
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}
	f.requests = append(f.requests, req.Msg)
	image := pb.WorkerCapability_WORKER_CAPABILITY_NATIVE_IMAGE_GENERATION_V1
	// The compatibility peer rejects the new enum before negotiation. The new
	// peer admits it only after its own original response advertised support.
	if slices.Contains(req.Msg.Capabilities, image) && (index%2 == 0 || !f.support[index-1]) {
		f.cancel()
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}
	response := &pb.AttachWorkerResponse{ServerId: string(f.serverID)}
	if f.support[index] {
		response.SupportedWorkerCapabilities = []pb.WorkerCapability{image}
	}
	if index+1 == len(f.support) {
		f.cancel()
	}
	return connect.NewResponse(response), nil
}

func (f *imageGenerationAttachFixture) WatchWork(context.Context, *connect.Request[pb.WatchWorkRequest], *connect.ServerStream[pb.WatchWorkResponse]) error {
	return connect.NewError(connect.CodeUnavailable, nil)
}

func TestNativeImageGenerationAttachmentNegotiatesBeforeAdvertisingAndPreservesReconnect(t *testing.T) {
	image := pb.WorkerCapability_WORKER_CAPABILITY_NATIVE_IMAGE_GENERATION_V1
	for _, tc := range []struct {
		name        string
		support     []bool
		sameRequest bool
	}{
		{"legacy", []bool{false, false, false, false}, true},
		{"supported", []bool{true, true, true, true}, true},
		{"support-removed", []bool{true, true, false, false}, false},
		{"support-added", []bool{false, false, true, true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			fixture := &imageGenerationAttachFixture{serverID: domain.NewID(), cancel: cancel, support: tc.support}
			_, handler := delidevv1connect.NewWorkerServiceHandler(fixture)
			server := httptest.NewServer(handler)
			defer server.Close()
			credential := Credential{Endpoint: server.URL, ServerID: fixture.serverID, MachineID: domain.NewID(), Token: "isolated-image-attachment"}
			if err := runConnected(ctx, Config{Root: t.TempDir(), Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}, credential); err != nil {
				t.Fatal(err)
			}
			fixture.Lock()
			defer fixture.Unlock()
			if len(fixture.requests) != 4 {
				t.Fatal("strict peer rejected attachment before negotiated reconnect")
			}
			first, negotiated, reattach, renegotiated := fixture.requests[0], fixture.requests[1], fixture.requests[2], fixture.requests[3]
			if slices.Contains(first.Capabilities, image) || slices.Contains(reattach.Capabilities, image) {
				t.Fatal("compatibility attachment advertised unnegotiated image generation")
			}
			if slices.Contains(negotiated.Capabilities, image) != tc.support[0] || slices.Contains(renegotiated.Capabilities, image) != tc.support[2] {
				t.Fatal("image generation advertisement did not follow the original server response")
			}
			if first.RequestId != reattach.RequestId || (negotiated.RequestId == renegotiated.RequestId) != tc.sameRequest {
				t.Fatal("reconnect lost its original request identity or negotiated support profile")
			}
			for _, request := range fixture.requests {
				if request.InstanceId != first.InstanceId || request.MachineId != string(credential.MachineID) {
					t.Fatal("negotiation replaced original Worker authority")
				}
				for _, baseline := range first.Capabilities {
					if !slices.Contains(request.Capabilities, baseline) {
						t.Fatal("negotiation removed an existing baseline capability")
					}
				}
			}
		})
	}
}
