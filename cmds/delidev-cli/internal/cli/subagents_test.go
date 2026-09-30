package cli

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type subagentReadClient struct {
	delidevv1connect.ResourceServiceClient
	requests []*pb.ListResourcesRequest
}

func (c *subagentReadClient) ListResources(_ context.Context, request *connect.Request[pb.ListResourcesRequest]) (*connect.Response[pb.ListResourcesResponse], error) {
	c.requests = append(c.requests, request.Msg)
	return connect.NewResponse(&pb.ListResourcesResponse{NextPageToken: "next-original-page"}), nil
}

func TestSubagentCLIOnlyReadsOriginalSessionPages(t *testing.T) {
	reader := &subagentReadClient{}
	c := client{resources: reader}
	session := string(domain.NewID())
	for _, cursor := range []string{"", "original-page"} {
		result, err := sessionCommand(context.Background(), c, options{}, []string{"subagents", "--id", session, "--limit", "50", "--page-token", cursor}, IO{})
		if err != nil || result.(map[string]any)["next_page_token"] != "next-original-page" {
			t.Fatal("child read failed", err)
		}
		request := reader.requests[len(reader.requests)-1]
		if request.Filter.Kind != pb.EntityKind_ENTITY_KIND_SUBAGENT || request.Filter.SessionId != session || request.Filter.PageSize != 50 || request.Filter.PageToken != cursor {
			t.Fatal("child read replaced original session or page")
		}
	}
	for _, args := range [][]string{{"--id", "foreign"}, {"--id", session, "--limit", "0"}, {"--id", session, "--limit", "101"}, {"--id", session, "--resume"}} {
		if _, err := sessionSubagents(context.Background(), c, args); err == nil {
			t.Fatal("invalid or control arguments accepted")
		}
	}
	if len(reader.requests) != 2 {
		t.Fatal("invalid arguments issued a resource read")
	}
}
