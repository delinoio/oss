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
	"strings"
	"testing"
)

func TestClaudeConfigurationCLIExactReviewAndCapabilityGate(t *testing.T) {
	mux := http.NewServeMux()
	supported := true
	calls := 0
	requestID := domain.NewID()
	input := `{"exact":"reviewed bytes"}`
	mux.Handle(delidevv1connect.SystemServiceGetStatusProcedure, connect.NewUnaryHandler(delidevv1connect.SystemServiceGetStatusProcedure, func(context.Context, *connect.Request[pb.GetStatusRequest]) (*connect.Response[pb.GetStatusResponse], error) {
		caps := []pb.SystemCapability{}
		if supported {
			caps = append(caps, pb.SystemCapability_SYSTEM_CAPABILITY_CLAUDE_CONFIGURATION_IMPORT_V1)
		}
		return connect.NewResponse(&pb.GetStatusResponse{Capabilities: caps}), nil
	}))
	mux.Handle(delidevv1connect.ConfigurationServicePreviewClaudeConfigurationImportProcedure, connect.NewUnaryHandler(delidevv1connect.ConfigurationServicePreviewClaudeConfigurationImportProcedure, func(_ context.Context, r *connect.Request[pb.PreviewClaudeConfigurationImportRequest]) (*connect.Response[pb.PreviewClaudeConfigurationImportResponse], error) {
		calls++
		if string(r.Msg.SelectionJson) != input {
			t.Error("preview altered selection")
		}
		return connect.NewResponse(&pb.PreviewClaudeConfigurationImportResponse{PreviewJson: []byte(input)}), nil
	}))
	mux.Handle(delidevv1connect.ConfigurationServiceApplyClaudeConfigurationImportProcedure, connect.NewUnaryHandler(delidevv1connect.ConfigurationServiceApplyClaudeConfigurationImportProcedure, func(_ context.Context, r *connect.Request[pb.ApplyClaudeConfigurationImportRequest]) (*connect.Response[pb.ApplyClaudeConfigurationImportResponse], error) {
		calls++
		if string(r.Msg.PreviewJson) != input || r.Msg.RequestId != string(requestID) {
			t.Error("apply lost exact review/request identity")
		}
		return connect.NewResponse(&pb.ApplyClaudeConfigurationImportResponse{Imported: &pb.Resource{Kind: pb.EntityKind_ENTITY_KIND_IMPORTED_NATIVE_CONFIGURATION}}), nil
	}))
	server := httptest.NewServer(mux)
	defer server.Close()
	c := client{system: delidevv1connect.NewSystemServiceClient(server.Client(), server.URL), configuration: delidevv1connect.NewConfigurationServiceClient(server.Client(), server.URL)}
	for _, operation := range []string{"claude-preview", "claude-apply"} {
		if _, e := configurationTransfer(context.Background(), c, options{requestID: requestID}, []string{operation}, IO{In: strings.NewReader(input)}); e != nil {
			t.Fatal(e)
		}
	}
	supported = false
	if _, e := configurationTransfer(context.Background(), c, options{}, []string{"claude-preview"}, IO{In: strings.NewReader(input)}); domain.SafeError(e).Code != domain.Unsupported {
		t.Fatal("unsupported server accepted import", e)
	}
	if calls != 2 {
		t.Fatal("capability refusal sent native request")
	}
}
