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
