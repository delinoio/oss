// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func TestSnapshotInventoryNegotiatesSupportAndRetainsPagination(t *testing.T) {
	status := &accountSwitchStatusStub{}
	resources := &resourceListStub{}
	c := client{system: status, resources: resources}
	session := string(domain.NewID())
	args := []string{"--session-id", session, "--limit", "17", "--page-token", "original-opaque-page"}
	if _, err := snapshotListCommand(context.Background(), c, args); domain.SafeError(err).Code != domain.Unsupported || resources.calls != 0 {
		t.Fatal("unsupported server received snapshot query", err)
	}
	status.capabilities = []pb.SystemCapability{pb.SystemCapability_SYSTEM_CAPABILITY_WORKSPACE_STORAGE_V1}
	if _, err := snapshotListCommand(context.Background(), c, args); err != nil {
		t.Fatal(err)
	}
	f := resources.request.Filter
	if resources.calls != 1 || f.Kind != pb.EntityKind_ENTITY_KIND_SNAPSHOT || f.SessionId != session || f.PageSize != 17 || f.PageToken != "original-opaque-page" {
		t.Fatal("snapshot page identity changed", f)
	}
	for _, invalid := range [][]string{{"--session-id", "bad"}, {"--session-id", session, "--limit", "101"}, {"--session-id", session, "--limit", "0"}} {
		before := status.calls
		if _, err := snapshotListCommand(context.Background(), c, invalid); err == nil || status.calls != before {
			t.Fatal("invalid input reached status", err)
		}
	}
}

func TestSnapshotListCLIRoutesAuthenticatedResourcePages(t *testing.T) {
	session, snapshot := string(domain.NewID()), string(domain.NewID())
	calls := 0
	mux := http.NewServeMux()
	mux.Handle(delidevv1connect.SystemServiceGetStatusProcedure, connect.NewUnaryHandler(delidevv1connect.SystemServiceGetStatusProcedure, func(context.Context, *connect.Request[pb.GetStatusRequest]) (*connect.Response[pb.GetStatusResponse], error) {
		return connect.NewResponse(&pb.GetStatusResponse{Capabilities: []pb.SystemCapability{pb.SystemCapability_SYSTEM_CAPABILITY_WORKSPACE_STORAGE_V1}}), nil
	}))
	mux.Handle(delidevv1connect.ResourceServiceListResourcesProcedure, connect.NewUnaryHandler(delidevv1connect.ResourceServiceListResourcesProcedure, func(_ context.Context, r *connect.Request[pb.ListResourcesRequest]) (*connect.Response[pb.ListResourcesResponse], error) {
		calls++
		if r.Header().Get("Authorization") != "Bearer fixture-only" || r.Msg.Filter.SessionId != session || r.Msg.Filter.Kind != pb.EntityKind_ENTITY_KIND_SNAPSHOT || r.Msg.Filter.PageToken != "original-page" {
			t.Error("CLI lost authenticated snapshot page")
		}
		return connect.NewResponse(&pb.ListResourcesResponse{Resources: []*pb.Resource{{Id: snapshot, SessionId: session, Kind: pb.EntityKind_ENTITY_KIND_SNAPSHOT, Revision: 9007199254740993, DocumentJson: []byte(`{"deleted":false}`)}}, NextPageToken: "next-page"}), nil
	}))
	server := httptest.NewServer(mux)
	defer server.Close()
	var out, stderr bytes.Buffer
	code := Run(context.Background(), []string{"--server", server.URL, "--token-stdin", "snapshot", "list", "--session-id", session, "--page-token", "original-page"}, IO{In: bytes.NewBufferString("fixture-only\n"), Out: &out, Err: &stderr})
	if code != 0 || calls != 1 {
		t.Fatal("documented snapshot command failed", code, out.String(), stderr.String())
	}
	var value map[string]any
	decoder := json.NewDecoder(bytes.NewReader(out.Bytes()))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		t.Fatal("invalid output")
	}
	result := value["result"].(map[string]any)
	if result["next_page_token"] != "next-page" || result["resources"].([]any)[0].(map[string]any)["revision"] != json.Number("9007199254740993") {
		t.Fatal("inventory pagination or precision lost", result)
	}
}
