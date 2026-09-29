package cli

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type providerCapabilityStub struct {
	delidevv1connect.ProviderServiceClient
	capabilities   []pb.ProviderInventoryCapability
	inventoryCalls int
	searchCalls    int
	searchRequest  *pb.SearchModelsRequest
}

func (stub *providerCapabilityStub) ListProviderInventory(context.Context, *connect.Request[pb.ListProviderInventoryRequest]) (*connect.Response[pb.ListProviderInventoryResponse], error) {
	stub.inventoryCalls++
	return connect.NewResponse(&pb.ListProviderInventoryResponse{Capabilities: stub.capabilities}), nil
}

func (stub *providerCapabilityStub) SearchModels(_ context.Context, request *connect.Request[pb.SearchModelsRequest]) (*connect.Response[pb.SearchModelsResponse], error) {
	stub.searchCalls++
	stub.searchRequest = request.Msg
	return connect.NewResponse(&pb.SearchModelsResponse{}), nil
}

type resourceListStub struct {
	delidevv1connect.ResourceServiceClient
	calls   int
	request *pb.ListResourcesRequest
}

func (stub *resourceListStub) ListResources(_ context.Context, request *connect.Request[pb.ListResourcesRequest]) (*connect.Response[pb.ListResourcesResponse], error) {
	stub.calls++
	stub.request = request.Msg
	return connect.NewResponse(&pb.ListResourcesResponse{}), nil
}

func TestAccountListProviderFilterRequiresInventoryCapability(t *testing.T) {
	provider := &providerCapabilityStub{}
	resources := &resourceListStub{}
	c := client{providers: provider, resources: resources}
	filter := &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_ACCOUNT, PageSize: 10}
	id := string(domain.NewID())

	if _, err := listWithProviderFilter(context.Background(), c, filter, id); domain.SafeError(err).Code != domain.Unsupported {
		t.Fatalf("legacy server ignored provider filtering: %v", err)
	}
	if resources.calls != 0 || provider.inventoryCalls != 1 {
		t.Fatalf("provider list ran before capability check: resources=%d inventory=%d", resources.calls, provider.inventoryCalls)
	}

	provider.capabilities = []pb.ProviderInventoryCapability{pb.ProviderInventoryCapability_PROVIDER_INVENTORY_CAPABILITY_ACCOUNT_PROVIDER_FILTER}
	if _, err := listWithProviderFilter(context.Background(), c, filter, id); err != nil {
		t.Fatal(err)
	}
	if resources.calls != 1 || resources.request.ProviderId != id {
		t.Fatalf("supported provider filter was not sent: %+v", resources.request)
	}
}

func TestEnabledProviderModelSearchRequiresInventoryCapability(t *testing.T) {
	provider := &providerCapabilityStub{}
	c := client{providers: provider}
	args := []string{"search", "--enabled-providers-only"}

	if _, err := modelCatalog(context.Background(), c, args); domain.SafeError(err).Code != domain.Unsupported {
		t.Fatalf("legacy server ignored enabled-provider filtering: %v", err)
	}
	if provider.searchCalls != 0 || provider.inventoryCalls != 1 {
		t.Fatalf("model search ran before capability check: search=%d inventory=%d", provider.searchCalls, provider.inventoryCalls)
	}

	provider.capabilities = []pb.ProviderInventoryCapability{pb.ProviderInventoryCapability_PROVIDER_INVENTORY_CAPABILITY_ACTIVE_API_MODEL_FILTER}
	if _, err := modelCatalog(context.Background(), c, args); err != nil {
		t.Fatal(err)
	}
	if provider.searchCalls != 1 || !provider.searchRequest.EnabledProvidersOnly {
		t.Fatalf("supported model filter was not sent: %+v", provider.searchRequest)
	}
}
