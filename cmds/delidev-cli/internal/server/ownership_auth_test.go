// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func TestRegisteredWorkerProductAccessPreservesAuthenticationAndRevocation(t *testing.T) {
	endpoint, owner, stop, done := runTestServer(t, filepath.Join(t.TempDir(), "state"))
	defer func() {
		stop()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	worker, paired := pairedWorker(t, ctx, endpoint, owner)
	system := delidevv1connect.NewSystemServiceClient(http.DefaultClient, endpoint.URL)
	config := delidevv1connect.NewConfigurationServiceClient(http.DefaultClient, endpoint.URL)
	resources := delidevv1connect.NewResourceServiceClient(http.DefaultClient, endpoint.URL)
	devices := delidevv1connect.NewDeviceServiceClient(http.DefaultClient, endpoint.URL)
	if _, err := system.GetOverview(ctx, ownerRequest(worker, &pb.GetOverviewRequest{})); err != nil {
		t.Fatal("authenticated Worker product read", err)
	}
	requestID := domain.NewID()
	saved := saveProvider(t, ctx, worker, config, requestID)
	if replay := saveProvider(t, ctx, worker, config, requestID); !replay.Replayed || replay.Resource.Id != saved.Resource.Id {
		t.Fatal("Worker product mutation lost request deduplication")
	}
	if _, err := resources.GetResource(ctx, ownerRequest(worker, &pb.GetResourceRequest{Kind: pb.EntityKind_ENTITY_KIND_DEVICE, Id: paired.Device.Id})); err != nil {
		t.Fatal("Worker product resource read", err)
	}
	// A second independently paired credential can revoke the first registration.
	other, _ := pairedWorker(t, ctx, endpoint, worker)
	if _, err := devices.RevokeDevice(ctx, ownerRequest(other, &pb.RevokeDeviceRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: paired.Device.Id, ExpectedRevision: paired.Device.Revision}})); err != nil {
		t.Fatal("authenticated cross-device revocation", err)
	}
	for _, identity := range []security.Identity{worker, {Token: randomCode()}, {}} {
		if _, err := system.GetOverview(ctx, ownerRequest(identity, &pb.GetOverviewRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Fatal("revoked, invalid or absent token accepted", err)
		}
	}
	if _, err := system.GetOverview(ctx, ownerRequest(other, &pb.GetOverviewRequest{})); err != nil {
		t.Fatal("unrevoked registration lost access", err)
	}
}
