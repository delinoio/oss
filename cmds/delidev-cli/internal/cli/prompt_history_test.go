// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"connectrpc.com/connect"
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"net/http"
	"net/http/httptest"
	"testing"
)

type historyCLIHandler struct {
	delidevv1connect.UnimplementedConfigurationServiceHandler
	project, request string
	clears           int
}

func (h *historyCLIHandler) ListProjectPromptHistory(ctx context.Context, r *connect.Request[pb.ListProjectPromptHistoryRequest]) (*connect.Response[pb.ListProjectPromptHistoryResponse], error) {
	h.project = r.Msg.ProjectId
	return connect.NewResponse(&pb.ListProjectPromptHistoryResponse{Entries: []*pb.ProjectPromptHistoryEntry{{Id: string(domain.NewID()), ProjectId: r.Msg.ProjectId, Prompt: " first\n한글 ", AcceptanceSequence: 1, AcceptedAt: "2026-10-08T00:00:00Z"}}, NextPageToken: "next"}), nil
}
func (h *historyCLIHandler) ClearProjectPromptHistory(ctx context.Context, r *connect.Request[pb.ClearProjectPromptHistoryRequest]) (*connect.Response[pb.ClearProjectPromptHistoryResponse], error) {
	h.clears++
	h.request = r.Msg.RequestId
	return connect.NewResponse(&pb.ClearProjectPromptHistoryResponse{ProjectId: r.Msg.ProjectId, RequestId: r.Msg.RequestId, Replayed: h.clears > 1, RemovedCount: 1}), nil
}
func TestProjectPromptHistoryCLIExactTextConfirmationAndRequestIdentity(t *testing.T) {
	h := &historyCLIHandler{}
	_, handler := delidevv1connect.NewConfigurationServiceHandler(h)
	server := httptest.NewServer(handler)
	defer server.Close()
	c := client{configuration: delidevv1connect.NewConfigurationServiceClient(http.DefaultClient, server.URL)}
	project, request := domain.NewID(), domain.NewID()
	ctx := context.Background()
	o := options{requestID: request}
	result, err := projectPromptHistoryCommand(ctx, c, o, []string{"list", "--project-id", string(project), "--limit", "100"})
	if err != nil {
		t.Fatal(err)
	}
	value := result.(map[string]any)
	entry := value["entries"].([]any)[0].(map[string]any)
	if entry["prompt"] != " first\n한글 " || value["next_page_token"] != "next" || h.project != string(project) {
		t.Fatal(result)
	}
	if _, err := projectPromptHistoryCommand(ctx, c, o, []string{"clear", "--project-id", string(project)}); domain.SafeError(err).Code != domain.ConfirmationRequired || h.clears != 0 {
		t.Fatal("clear skipped confirmation", err)
	}
	for i := 0; i < 2; i++ {
		result, err = projectPromptHistoryCommand(ctx, c, o, []string{"clear", "--project-id", string(project), "--confirm"})
		if err != nil || h.request != string(request) {
			t.Fatal(result, err)
		}
		if i == 1 && result.(map[string]any)["replayed"] != true {
			t.Fatal("lost replay metadata")
		}
	}
}
