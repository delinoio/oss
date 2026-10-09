// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestKnownSubscriptionModelsReadOnlyAndWorkerDenied(t *testing.T) {
	f := newAccountFixture(t)
	ctx := context.Background()
	before, err := f.config.ExportConfiguration(ctx, ownerRequest(f.identity, &pb.ExportConfigurationRequest{}))
	if err != nil {
		t.Fatal(err)
	}
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
	after, err := f.config.ExportConfiguration(ctx, ownerRequest(f.identity, &pb.ExportConfigurationRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before.Msg.DocumentJson, after.Msg.DocumentJson) {
		t.Fatal("read created saved models")
	}
	_, err = client.ListKnownSubscriptionModels(ctx, ownerRequest(f.identity, &pb.ListKnownSubscriptionModelsRequest{}))
	wantAccountCode(t, err, domain.InvalidArgument)
	worker, _ := pairedWorker(t, ctx, f.endpoint, f.identity)
	_, err = client.ListKnownSubscriptionModels(ctx, ownerRequest(worker, &pb.ListKnownSubscriptionModelsRequest{SubscriptionService: pb.SubscriptionServiceIdentity_SUBSCRIPTION_SERVICE_IDENTITY_CHATGPT}))
	wantAccountCode(t, err, domain.PermissionDenied)
}
