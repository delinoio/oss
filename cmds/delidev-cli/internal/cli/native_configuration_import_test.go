// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type nativeImportConfigurationClient struct {
	delidevv1connect.ConfigurationServiceClient
	preview func(*pb.PreviewNativeConfigurationImportRequest) *pb.PreviewNativeConfigurationImportResponse
	apply   func(*pb.ApplyNativeConfigurationImportRequest) *pb.ApplyNativeConfigurationImportResponse
}

func (c nativeImportConfigurationClient) PreviewNativeConfigurationImport(_ context.Context, request *connect.Request[pb.PreviewNativeConfigurationImportRequest]) (*connect.Response[pb.PreviewNativeConfigurationImportResponse], error) {
	return connect.NewResponse(c.preview(request.Msg)), nil
}
func (c nativeImportConfigurationClient) ApplyNativeConfigurationImport(_ context.Context, request *connect.Request[pb.ApplyNativeConfigurationImportRequest]) (*connect.Response[pb.ApplyNativeConfigurationImportResponse], error) {
	return connect.NewResponse(c.apply(request.Msg)), nil
}

type nativeImportSystemClient struct {
	delidevv1connect.SystemServiceClient
	supported bool
}

func (s nativeImportSystemClient) GetStatus(context.Context, *connect.Request[pb.GetStatusRequest]) (*connect.Response[pb.GetStatusResponse], error) {
	capabilities := []pb.SystemCapability{}
	if s.supported {
		capabilities = append(capabilities, pb.SystemCapability_SYSTEM_CAPABILITY_NATIVE_CONFIGURATION_IMPORT_V1)
	}
	return connect.NewResponse(&pb.GetStatusResponse{Capabilities: capabilities}), nil
}
func TestCLINativeImportPreservesOriginalWireSelectionAndRequest(t *testing.T) {
	requestID, previewID := domain.NewID(), domain.NewID()
	raw := []byte("{\n \"version\":1, \"machine_revision\":\"9007199254740993\"\n}")
	observation := json.RawMessage(`{"version":1,"sources":[],"entries":[]}`)
	document, _ := json.Marshal(domain.Job{Type: domain.NativeConfigurationPreviewJob, State: domain.JobSucceeded, Input: json.RawMessage(`{}`), Output: observation})
	job := &pb.Resource{Id: string(previewID), Kind: pb.EntityKind_ENTITY_KIND_JOB, SchemaVersion: 1, DocumentJson: document}
	previews, applies := 0, 0
	c := client{system: nativeImportSystemClient{supported: true}, configuration: nativeImportConfigurationClient{
		preview: func(request *pb.PreviewNativeConfigurationImportRequest) *pb.PreviewNativeConfigurationImportResponse {
			previews++
			if request.RequestId != string(requestID) || !bytes.Equal(request.SelectionJson, raw) {
				t.Fatal("preview changed wire selection or identity")
			}
			return &pb.PreviewNativeConfigurationImportResponse{RequestId: request.RequestId, Job: job}
		},
		apply: func(request *pb.ApplyNativeConfigurationImportRequest) *pb.ApplyNativeConfigurationImportResponse {
			applies++
			if request.RequestId != string(requestID) || !bytes.Equal(request.PlanJson, raw) {
				t.Fatal("apply changed wire selection or original preview")
			}
			return &pb.ApplyNativeConfigurationImportResponse{RequestId: request.RequestId, Replayed: true, Job: job}
		},
	}}
	result, err := nativeConfigurationImport(context.Background(), c, options{requestID: requestID}, []string{"native-preview"}, IO{In: bytes.NewReader(raw)})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result)
	if !bytes.Contains(encoded, []byte(domain.HarnessEvidenceDigest(observation))) || previews != 1 || applies != 0 {
		t.Fatal("preview imported or lost original digest")
	}
	if _, err := nativeConfigurationImport(context.Background(), c, options{requestID: requestID}, []string{"native-apply"}, IO{In: bytes.NewReader(raw)}); err != nil || applies != 1 {
		t.Fatal(err)
	}
	c.system = nativeImportSystemClient{supported: false}
	if _, err := nativeConfigurationImport(context.Background(), c, options{requestID: requestID}, []string{"native-preview"}, IO{In: bytes.NewReader(raw)}); domain.SafeError(err).Code != domain.Unsupported || previews != 1 {
		t.Fatal("unsupported server received new RPC", err)
	}
}
func TestCLINativeImportRejectsAmbiguousStdinAndOversizedDescriptor(t *testing.T) {
	if _, err := nativeConfigurationImport(context.Background(), client{}, options{tokenStdin: true}, []string{"native-preview"}, IO{In: strings.NewReader(`{}`)}); domain.SafeError(err).Code != domain.MissingInput {
		t.Fatal(err)
	}
	if _, err := nativeConfigurationImport(context.Background(), client{}, options{}, []string{"native-preview"}, IO{In: strings.NewReader(`{"padding":"` + strings.Repeat("x", domain.MaxNativeConfigurationSelectionBytes) + `"}`)}); err == nil {
		t.Fatal("oversized descriptor reached the server")
	}
}
