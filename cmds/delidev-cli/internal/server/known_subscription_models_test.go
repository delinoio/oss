// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestKnownSubscriptionModelsReadOnlyAndWorkerDenied(t *testing.T) {
	f := newAccountFixture(t)
	ctx := context.Background()
	before := catalogSearch(t, f, &pb.SearchModelsRequest{SubscriptionService: pb.SubscriptionServiceIdentity_SUBSCRIPTION_SERVICE_IDENTITY_CHATGPT})
	client := catalogClient(f)
	for _, service := range []pb.SubscriptionServiceIdentity{pb.SubscriptionServiceIdentity_SUBSCRIPTION_SERVICE_IDENTITY_CHATGPT, pb.SubscriptionServiceIdentity_SUBSCRIPTION_SERVICE_IDENTITY_CLAUDE, pb.SubscriptionServiceIdentity_SUBSCRIPTION_SERVICE_IDENTITY_GROK} {
		response, err := client.ListKnownSubscriptionModels(ctx, ownerRequest(f.identity, &pb.ListKnownSubscriptionModelsRequest{SubscriptionService: service}))
		if err != nil {
			t.Fatal(err)
		}
		if response.Msg.SubscriptionService != service || len(response.Msg.Models) == 0 || len(response.Msg.Models) > 200 || response.Msg.Source != pb.KnownSubscriptionModelCatalogSource_KNOWN_SUBSCRIPTION_MODEL_CATALOG_SOURCE_BUNDLED || response.Msg.CatalogVersion == "" || response.Msg.UpdatedAt == "" {
			t.Fatalf("invalid known catalog: %+v", response.Msg)
		}
	}
	after := catalogSearch(t, f, &pb.SearchModelsRequest{SubscriptionService: pb.SubscriptionServiceIdentity_SUBSCRIPTION_SERVICE_IDENTITY_CHATGPT})
	if len(before.Models) != len(after.Models) {
		t.Fatal("read created saved models")
	}
	_, err := client.ListKnownSubscriptionModels(ctx, ownerRequest(f.identity, &pb.ListKnownSubscriptionModelsRequest{}))
	wantAccountCode(t, err, domain.InvalidArgument)
	worker, _ := pairedWorker(t, ctx, f.endpoint, f.identity)
	_, err = client.ListKnownSubscriptionModels(ctx, ownerRequest(worker, &pb.ListKnownSubscriptionModelsRequest{SubscriptionService: pb.SubscriptionServiceIdentity_SUBSCRIPTION_SERVICE_IDENTITY_CHATGPT}))
	if err != nil {
		t.Fatal("authenticated Worker catalog read failed", err)
	}
}
