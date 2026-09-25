package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

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
			if err != nil || !proto.Equal(response.Msg, replay.Msg) {
				t.Fatal("same cursor changed stable page", err)
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
