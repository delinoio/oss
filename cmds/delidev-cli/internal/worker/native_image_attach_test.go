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

type nativeImageAttachFixture struct {
	delidevv1connect.UnimplementedWorkerServiceHandler
	sync.Mutex
	serverID  domain.ID
	machineID domain.ID
	cancel    context.CancelFunc
	support   []bool
	requests  []*pb.AttachWorkerRequest
}

func (f *nativeImageAttachFixture) AttachWorker(_ context.Context, req *connect.Request[pb.AttachWorkerRequest]) (*connect.Response[pb.AttachWorkerResponse], error) {
	f.Lock()
	defer f.Unlock()
	if req.Header().Get("Authorization") != "Bearer synthetic-image-worker" || req.Msg.MachineId != string(f.machineID) {
		f.cancel()
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}
	index := len(f.requests)
	f.requests = append(f.requests, req.Msg)
	if index >= len(f.support) {
		f.cancel()
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}
	image := pb.WorkerCapability_WORKER_CAPABILITY_NATIVE_IMAGE_GENERATION_V1
	// A compatible pre-image server rejects the newly allocated enum, rather
	// than accepting it as an unknown value and falsely granting image support.
	if !f.support[index] && slices.Contains(req.Msg.Capabilities, image) {
		f.cancel()
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}
	response := &pb.AttachWorkerResponse{ServerId: string(f.serverID)}
	if f.support[index] {
		response.SupportedWorkerCapabilities = []pb.WorkerCapability{image}
	}
	if len(f.requests) == len(f.support) {
		f.cancel()
	}
	return connect.NewResponse(response), nil
}

func (f *nativeImageAttachFixture) WatchWork(context.Context, *connect.Request[pb.WatchWorkRequest], *connect.ServerStream[pb.WatchWorkResponse]) error {
	return connect.NewError(connect.CodeUnavailable, nil)
}

func TestNativeImageNegotiationPreservesCompatibleAttachmentAndReconnect(t *testing.T) {
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
			fixture := &nativeImageAttachFixture{serverID: domain.NewID(), machineID: domain.NewID(), cancel: cancel, support: tc.support}
			_, handler := delidevv1connect.NewWorkerServiceHandler(fixture)
			server := httptest.NewServer(handler)
			defer server.Close()
			credential := Credential{Endpoint: server.URL, ServerID: fixture.serverID, MachineID: fixture.machineID, Token: "synthetic-image-worker"}
			if err := runConnected(ctx, Config{Root: t.TempDir(), Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}, credential); err != nil {
				t.Fatal(err)
			}
			fixture.Lock()
			defer fixture.Unlock()
			if len(fixture.requests) != 4 {
				t.Fatal("negotiation did not reach its original reconnect")
			}
			first, negotiated, reattach, renegotiated := fixture.requests[0], fixture.requests[1], fixture.requests[2], fixture.requests[3]
			if slices.Contains(first.Capabilities, image) || slices.Contains(reattach.Capabilities, image) {
				t.Fatal("preliminary attachment advertised image generation before negotiation")
			}
			if slices.Contains(negotiated.Capabilities, image) != tc.support[0] || slices.Contains(renegotiated.Capabilities, image) != tc.support[2] {
				t.Fatal("image generation did not follow current authenticated server support")
			}
			if first.RequestId != reattach.RequestId || !slices.Equal(first.Capabilities, reattach.Capabilities) {
				t.Fatal("preliminary reconnect changed the original request")
			}
			if (negotiated.RequestId == renegotiated.RequestId) != tc.sameRequest {
				t.Fatal("retained negotiation identity ignored its image capability profile")
			}
			baseline := []pb.WorkerCapability{
				pb.WorkerCapability_WORKER_CAPABILITY_INLINE_MODEL_EXECUTION_V1,
				pb.WorkerCapability_WORKER_CAPABILITY_NATIVE_SKILLS_V1,
				pb.WorkerCapability_WORKER_CAPABILITY_IMAGE_INPUTS_V1,
				pb.WorkerCapability_WORKER_CAPABILITY_EXECUTION_STARTUP_V1,
				pb.WorkerCapability_WORKER_CAPABILITY_NATIVE_CODEX_MODEL_DISCOVERY_V1,
				pb.WorkerCapability_WORKER_CAPABILITY_SESSION_FORWARDING_V1,
				pb.WorkerCapability_WORKER_CAPABILITY_SESSION_TERMINALS_V1,
			}
			if !slices.Equal(first.Capabilities, baseline) {
				t.Fatal("image negotiation changed the compatible baseline")
			}
			for _, request := range fixture.requests {
				if request.InstanceId != first.InstanceId {
					t.Fatal("image negotiation replaced original Worker process authority")
				}
				for _, capability := range baseline {
					if !slices.Contains(request.Capabilities, capability) {
						t.Fatal("image negotiation removed an existing baseline capability")
					}
				}
			}
		})
	}
}
