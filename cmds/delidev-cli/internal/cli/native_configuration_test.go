// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"bytes"
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

func TestNativeConfigurationCLIRequestIdentityAndExactDocuments(t *testing.T) {
	for _, operation := range []string{"native-preview", "native-review", "native-apply"} {
		t.Run(operation, func(t *testing.T) {
			mux := http.NewServeMux()
			capable := true
			calls := 0
			document := []byte(`{"fixture":"exact reviewed input"}`)
			machine := string(domain.NewID())
			requestID := domain.NewID()
			mux.Handle(delidevv1connect.SystemServiceGetStatusProcedure, connect.NewUnaryHandler(delidevv1connect.SystemServiceGetStatusProcedure, func(ctx context.Context, req *connect.Request[pb.GetStatusRequest]) (*connect.Response[pb.GetStatusResponse], error) {
				response := &pb.GetStatusResponse{}
				if capable {
					response.Capabilities = []pb.SystemCapability{pb.SystemCapability_SYSTEM_CAPABILITY_CODEX_CONFIGURATION_IMPORT_V1}
				}
				return connect.NewResponse(response), nil
			}))
			job := &pb.Resource{Id: string(domain.NewID()), Kind: pb.EntityKind_ENTITY_KIND_JOB, DocumentJson: []byte(`{"state":"queued"}`)}
			mux.Handle(delidevv1connect.ConfigurationServiceRequestCodexConfigurationPreviewProcedure, connect.NewUnaryHandler(delidevv1connect.ConfigurationServiceRequestCodexConfigurationPreviewProcedure, func(ctx context.Context, req *connect.Request[pb.RequestCodexConfigurationPreviewRequest]) (*connect.Response[pb.RequestCodexConfigurationPreviewResponse], error) {
				calls++
				if req.Msg.RequestId != string(requestID) || req.Msg.MachineId != machine || !bytes.Equal(req.Msg.SelectionJson, document) {
					t.Error("preview identity/document changed")
				}
				return connect.NewResponse(&pb.RequestCodexConfigurationPreviewResponse{RequestId: req.Msg.RequestId, Job: job}), nil
			}))
			mux.Handle(delidevv1connect.ConfigurationServicePreviewCodexConfigurationImportProcedure, connect.NewUnaryHandler(delidevv1connect.ConfigurationServicePreviewCodexConfigurationImportProcedure, func(ctx context.Context, req *connect.Request[pb.PreviewCodexConfigurationImportRequest]) (*connect.Response[pb.PreviewCodexConfigurationImportResponse], error) {
				calls++
				if !bytes.Equal(req.Msg.SelectionJson, document) {
					t.Error("review selection changed")
				}
				return connect.NewResponse(&pb.PreviewCodexConfigurationImportResponse{PreviewJson: document}), nil
			}))
			mux.Handle(delidevv1connect.ConfigurationServiceApplyCodexConfigurationImportProcedure, connect.NewUnaryHandler(delidevv1connect.ConfigurationServiceApplyCodexConfigurationImportProcedure, func(ctx context.Context, req *connect.Request[pb.ApplyCodexConfigurationImportRequest]) (*connect.Response[pb.ApplyCodexConfigurationImportResponse], error) {
				calls++
				if req.Msg.RequestId != string(requestID) || !bytes.Equal(req.Msg.PreviewJson, document) {
					t.Error("apply identity/document changed")
				}
				return connect.NewResponse(&pb.ApplyCodexConfigurationImportResponse{RequestId: req.Msg.RequestId, Job: job}), nil
			}))
			server := httptest.NewServer(mux)
			defer server.Close()
			c := client{system: delidevv1connect.NewSystemServiceClient(server.Client(), server.URL), configuration: delidevv1connect.NewConfigurationServiceClient(server.Client(), server.URL)}
			args := []string{operation, "--input", "-"}
			if operation == "native-preview" {
				args = append(args, "--machine", machine)
			}
			out := &bytes.Buffer{}
			code, handled := dispatchConfiguration(context.Background(), c, options{requestID: requestID}, args, IO{In: bytes.NewReader(document), Out: out, Err: out})
			if code != 0 || !handled || calls != 1 {
				t.Fatalf("CLI failed: %s", out.String())
			}
			if operation != "native-review" && !strings.Contains(out.String(), `"state":"queued"`) {
				t.Fatalf("job output encoded original JSON as bytes: %s", out.String())
			}
			capable = false
			before := calls
			if _, err := nativeConfigurationTransfer(context.Background(), c, options{requestID: requestID}, args, IO{In: bytes.NewReader(document)}); err == nil || calls != before {
				t.Fatal("unsupported server received native mutation")
			}
		})
	}
}
