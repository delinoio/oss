// SPDX-License-Identifier: Apache-2.0
package aiprouterv1_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	v1 "github.com/delinoio/oss/protos/aiprouter/v1"
	"github.com/delinoio/oss/protos/aiprouter/v1/aiprouterv1connect"
)

// This fixture checks generated wire behavior, not production authorization enforcement.
type usageFixture struct {
	aiprouterv1connect.UnimplementedUsageServiceHandler
}

func (usageFixture) GetUsageSummary(_ context.Context, req *connect.Request[v1.GetUsageSummaryRequest]) (*connect.Response[v1.GetUsageSummaryResponse], error) {
	if req.Msg.OrganizationId != "allowed-organization" {
		err := connect.NewError(connect.CodeNotFound, nil)
		detail, detailErr := connect.NewErrorDetail(&v1.ErrorDetail{Reason: v1.ErrorReason_ERROR_REASON_RESOURCE_NOT_FOUND, RequestId: "fixture-request"})
		if detailErr != nil {
			return nil, detailErr
		}
		err.AddDetail(detail)
		return nil, err
	}
	return connect.NewResponse(&v1.GetUsageSummaryResponse{Summary: &v1.UsageSummary{MissingUsageCount: 1, MissingPriceCount: 1}}), nil
}
func TestGeneratedConnectRoundTripAndTypedError(t *testing.T) {
	mux := http.NewServeMux()
	path, handler := aiprouterv1connect.NewUsageServiceHandler(usageFixture{})
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	defer server.Close()
	for _, json := range []bool{false, true} {
		var opts []connect.ClientOption
		if json {
			opts = append(opts, connect.WithProtoJSON())
		}
		client := aiprouterv1connect.NewUsageServiceClient(server.Client(), server.URL, opts...)
		response, err := client.GetUsageSummary(context.Background(), connect.NewRequest(&v1.GetUsageSummaryRequest{OrganizationId: "allowed-organization"}))
		if err != nil {
			t.Fatal(err)
		}
		if response.Msg.Summary.KnownUsage != nil || response.Msg.Summary.MissingUsageCount != 1 {
			t.Fatal("unknown aggregate lost on wire")
		}
		_, err = client.GetUsageSummary(context.Background(), connect.NewRequest(&v1.GetUsageSummaryRequest{OrganizationId: "other-organization"}))
		if connect.CodeOf(err) != connect.CodeNotFound {
			t.Fatalf("error code lost: %v", err)
		}
		ce, ok := err.(*connect.Error)
		if !ok || len(ce.Details()) != 1 {
			t.Fatalf("error detail lost: %v", err)
		}
		detail, err := ce.Details()[0].Value()
		if err != nil {
			t.Fatal(err)
		}
		typed, ok := detail.(*v1.ErrorDetail)
		if !ok || typed.Reason != v1.ErrorReason_ERROR_REASON_RESOURCE_NOT_FOUND {
			t.Fatalf("wrong typed detail: %v", detail)
		}
	}
}
