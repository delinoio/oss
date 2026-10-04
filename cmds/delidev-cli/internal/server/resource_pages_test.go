package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestAccountListFiltersAreAppliedBeforePaginationAndBoundToCursor(t *testing.T) {
	f := newAccountFixture(t)
	apiProviderA := f.save(pb.EntityKind_ENTITY_KIND_PROVIDER, domain.Provider{Name: "API A", Endpoint: "https://a.example.test/v1", Protocol: domain.OpenAIChat, Authentication: domain.BearerAuth})
	apiProviderB := f.save(pb.EntityKind_ENTITY_KIND_PROVIDER, domain.Provider{Name: "API B", Endpoint: "https://b.example.test/v1", Protocol: domain.OpenAIChat, Authentication: domain.BearerAuth})
	providers := []*pb.Resource{apiProviderA, apiProviderB}
	for i := 0; i < 102; i++ {
		kind, providerID := domain.APIAccount, providers[(i/2)%len(providers)].Id
		if i%2 == 1 {
			kind, providerID = domain.SubscriptionAccount, ""
		}
		account := domain.Account{Alias: fmt.Sprintf("account-%03d", i), ProviderID: domain.ID(providerID), Type: kind, Enabled: true, Health: domain.AccountDisconnected}
		if kind == domain.SubscriptionAccount {
			account.SubscriptionService = domain.SubscriptionChatGPT
		}
		f.save(pb.EntityKind_ENTITY_KIND_ACCOUNT, account)
	}

	list := func(input *pb.ListResourcesRequest) (*pb.ListResourcesResponse, error) {
		t.Helper()
		response, err := f.resources.ListResources(context.Background(), ownerRequest(f.identity, input))
		if err != nil {
			return nil, err
		}
		return response.Msg, nil
	}

	// Old callers that omit both additive selectors continue to receive both
	// account types over the complete paginated collection.
	seenAll := map[string]bool{}
	filter := &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_ACCOUNT, PageSize: 50}
	for {
		page, err := list(&pb.ListResourcesRequest{Filter: filter})
		if err != nil {
			t.Fatal(err)
		}
		for _, resource := range page.Resources {
			seenAll[resource.Id] = true
		}
		if page.NextPageToken == "" {
			break
		}
		filter = &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_ACCOUNT, PageSize: 50, PageToken: page.NextPageToken}
	}
	if len(seenAll) != 102 {
		t.Fatalf("legacy unfiltered list omitted account types: %d", len(seenAll))
	}

	// Type and provider selectors are composed in SQLite before LIMIT, so a
	// sparse provider page can still advance through every matching record.
	for _, probe := range []*pb.ListResourcesRequest{
		{Filter: &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_ACCOUNT, PageSize: 1}, AccountType: pb.AccountTypeFilter_ACCOUNT_TYPE_FILTER_API},
		{Filter: &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_ACCOUNT, PageSize: 1}, ProviderId: apiProviderB.Id},
	} {
		page, err := list(probe)
		if err != nil || len(page.Resources) != 1 {
			t.Fatalf("individual account filter returned %d rows: %v", len(page.GetResources()), err)
		}
	}
	seenScoped := map[string]bool{}
	filter = &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_ACCOUNT, PageSize: 1}
	accountType := pb.AccountTypeFilter_ACCOUNT_TYPE_FILTER_API
	for {
		page, err := list(&pb.ListResourcesRequest{Filter: filter, ProviderId: apiProviderB.Id, AccountType: accountType})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Resources) == 0 && page.NextPageToken == "" {
			break
		}
		if len(page.Resources) != 1 {
			t.Fatalf("scoped page has %d resources", len(page.Resources))
		}
		resource := page.Resources[0]
		var account domain.Account
		if err = domain.Decode(resource.DocumentJson, &account); err != nil {
			t.Fatal(err)
		}
		if account.Type != domain.APIAccount || account.ProviderID != domain.ID(apiProviderB.Id) || seenScoped[resource.Id] {
			t.Fatalf("wrong or repeated account in provider/type page: %+v", account)
		}
		seenScoped[resource.Id] = true
		if page.NextPageToken == "" {
			break
		}
		filter = &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_ACCOUNT, PageSize: 1, PageToken: page.NextPageToken}
	}
	if len(seenScoped) != 25 {
		t.Fatalf("combined filters returned %d matching accounts, want 25", len(seenScoped))
	}

	first, err := list(&pb.ListResourcesRequest{Filter: &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_ACCOUNT, PageSize: 1}, AccountType: pb.AccountTypeFilter_ACCOUNT_TYPE_FILTER_API})
	if err != nil || first.NextPageToken == "" {
		t.Fatalf("first API page missing a continuation: %v", err)
	}
	_, err = list(&pb.ListResourcesRequest{Filter: &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_ACCOUNT, PageSize: 1, PageToken: first.NextPageToken}, AccountType: pb.AccountTypeFilter_ACCOUNT_TYPE_FILTER_SUBSCRIPTION})
	if err == nil {
		t.Fatal("cursor was accepted under a different account type")
	}
	if _, err = list(&pb.ListResourcesRequest{Filter: &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_PROVIDER}, AccountType: pb.AccountTypeFilter_ACCOUNT_TYPE_FILTER_API}); err == nil {
		t.Fatal("account type filter was accepted for a provider list")
	}
	if _, err = list(&pb.ListResourcesRequest{Filter: &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_ACCOUNT}, AccountType: pb.AccountTypeFilter(99)}); err == nil {
		t.Fatal("unknown account type enum was accepted")
	}

	// The ordinary snapshot has no list-only selectors and retains both types.
	snapshot, err := f.resources.GetSnapshot(context.Background(), ownerRequest(f.identity, &pb.GetSnapshotRequest{Filter: &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_ACCOUNT, PageSize: 200}}))
	if err != nil {
		t.Fatal("account snapshot failed", err)
	}
	if len(snapshot.Msg.Resources) != 102 || snapshot.Msg.Cursor == "" {
		t.Fatalf("snapshot scope changed with additive list filters: count=%d", len(snapshot.Msg.Resources))
	}
}

func TestSnapshotsRejectAggregateByteOverflowBeforeSerialization(t *testing.T) {
	f := newAccountFixture(t)
	content := strings.Repeat("x", 128<<10)
	var expected []*pb.Resource
	for i := 1; i <= 40; i++ {
		expected = append(expected, f.save(pb.EntityKind_ENTITY_KIND_TEMPLATE, domain.Template{Name: fmt.Sprintf("snapshot-%d", i), Contents: content}))
		if i != 10 && i != 30 && i != 40 {
			continue
		}
		if i == 30 {
			unbounded := &pb.GetSnapshotResponse{Resources: expected}
			encoded, err := protojson.Marshal(unbounded)
			if err != nil || proto.Size(unbounded) >= 5<<20 || len(encoded) <= 5<<20 {
				t.Fatal("fixture must expose JSON-only transport overflow", err)
			}
		}
		for _, jsonWire := range []bool{false, true} {
			var opts []connect.ClientOption
			if jsonWire {
				opts = append(opts, connect.WithProtoJSON())
			}
			resources := delidevv1connect.NewResourceServiceClient(http.DefaultClient, f.endpoint.URL, opts...)
			response, err := resources.GetSnapshot(context.Background(), ownerRequest(f.identity, &pb.GetSnapshotRequest{Filter: &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_TEMPLATE, PageSize: 50}}))
			if i > 10 {
				problem := rpc.ClientError(err)
				if response != nil || connect.CodeOf(err) != connect.CodeResourceExhausted || problem.Code != domain.ResourceExhausted || !strings.Contains(problem.Guidance, "narrower") || problem.CorrelationID == "" {
					t.Fatalf("count=%d json=%t: expected explicit narrower-scope failure without partial snapshot: %v", i, jsonWire, err)
				}
				continue
			}
			if err != nil || len(response.Msg.Resources) != len(expected) || response.Msg.Cursor == "" {
				t.Fatal("bounded snapshot lost coherent state/cursor", err)
			}
			for j, resource := range response.Msg.Resources {
				if !proto.Equal(resource, expected[j]) {
					t.Fatal("snapshot changed retained resources")
				}
			}
		}
	}
}

func TestResourcePagesBoundBothEncodingsWithoutSkippingLargeTemplates(t *testing.T) {
	f := newAccountFixture(t)
	expected := map[string]bool{}
	content := strings.Repeat("x", 128<<10)
	for i := 0; i < 41; i++ {
		resource := f.save(pb.EntityKind_ENTITY_KIND_TEMPLATE, domain.Template{Name: fmt.Sprintf("large-%d", i), Contents: content})
		expected[resource.Id] = true
	}
	for _, jsonWire := range []bool{false, true} {
		var opts []connect.ClientOption
		if jsonWire {
			opts = append(opts, connect.WithProtoJSON())
		}
		resources := delidevv1connect.NewResourceServiceClient(http.DefaultClient, f.endpoint.URL, opts...)
		seen := map[string]bool{}
		cursor, last, pages := "", "", 0
		for {
			input := &pb.ListResourcesRequest{Filter: &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_TEMPLATE, PageToken: cursor}}
			response, err := resources.ListResources(context.Background(), ownerRequest(f.identity, input))
			if err != nil {
				t.Fatal(err)
			}
			pages++
			encoded, err := protojson.Marshal(response.Msg)
			if err != nil || len(encoded) >= 5<<20 || proto.Size(response.Msg) >= 5<<20 {
				t.Fatal("response exceeded transport", err)
			}
			for _, resource := range response.Msg.Resources {
				if !expected[resource.Id] || seen[resource.Id] || resource.Id <= last {
					t.Fatal("page skipped, duplicated or reordered an identity")
				}
				seen[resource.Id], last = true, resource.Id
			}
			replay, err := resources.ListResources(context.Background(), ownerRequest(f.identity, input))
			if err != nil || len(response.Msg.Resources) != len(replay.Msg.Resources) {
				t.Fatal("same cursor changed stable page", err)
			}
			// Cursor expiry is freshly signed for each response; retained
			// resource identities and bytes, rather than token bytes, are stable.
			for i, resource := range response.Msg.Resources {
				if !proto.Equal(resource, replay.Msg.Resources[i]) {
					t.Fatal("same cursor changed a retained resource")
				}
			}
			if response.Msg.NextPageToken == "" {
				break
			}
			if len(response.Msg.Resources) == 0 || response.Msg.NextPageToken == cursor || pages > 41 {
				t.Fatal("pagination failed to advance")
			}
			cursor = response.Msg.NextPageToken
		}
		if len(seen) != len(expected) || pages < 2 {
			t.Fatal("incomplete pagination", len(seen), pages)
		}
	}
}

func TestProviderScopedAccountListsBindCursorAndLeaveSnapshotsUnchanged(t *testing.T) {
	f := newAccountFixture(t)
	first := f.newAccount(domain.BearerAuth)
	second := f.newAccount(domain.BearerAuth)
	firstBody := accountBody(t, first)
	f.save(pb.EntityKind_ENTITY_KIND_ACCOUNT, domain.Account{Alias: "same provider", ProviderID: firstBody.ProviderID, Type: domain.APIAccount, Enabled: true, Health: domain.AccountDisconnected})
	client := delidevv1connect.NewResourceServiceClient(http.DefaultClient, f.endpoint.URL)
	filter := &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_ACCOUNT, PageSize: 1}
	page, err := client.ListResources(context.Background(), ownerRequest(f.identity, &pb.ListResourcesRequest{Filter: filter, ProviderId: string(firstBody.ProviderID)}))
	if err != nil || len(page.Msg.Resources) != 1 || page.Msg.NextPageToken == "" {
		t.Fatalf("first provider-scoped page: %v", err)
	}
	next, err := client.ListResources(context.Background(), ownerRequest(f.identity, &pb.ListResourcesRequest{Filter: &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_ACCOUNT, PageSize: 1, PageToken: page.Msg.NextPageToken}, ProviderId: string(firstBody.ProviderID)}))
	if err != nil || len(next.Msg.Resources) != 1 || next.Msg.NextPageToken != "" {
		t.Fatalf("provider-scoped continuation: %v", err)
	}
	_, err = client.ListResources(context.Background(), ownerRequest(f.identity, &pb.ListResourcesRequest{Filter: &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_ACCOUNT, PageSize: 1, PageToken: page.Msg.NextPageToken}, ProviderId: string(accountBody(t, second).ProviderID)}))
	if err == nil {
		t.Fatal("account cursor was reused for a different provider")
	}
	_, err = client.ListResources(context.Background(), ownerRequest(f.identity, &pb.ListResourcesRequest{Filter: &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_MODEL}, ProviderId: string(firstBody.ProviderID)}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("provider filter accepted a non-account resource kind: %v", err)
	}
	snapshot, err := client.GetSnapshot(context.Background(), ownerRequest(f.identity, &pb.GetSnapshotRequest{Filter: &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_ACCOUNT, PageSize: 50}}))
	if err != nil || len(snapshot.Msg.Resources) != 3 {
		count := 0
		if snapshot != nil {
			count = len(snapshot.Msg.Resources)
		}
		t.Fatalf("snapshot semantics changed with provider list support: resources=%d err=%v", count, err)
	}
}
