// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type compactionCLIService struct {
	delidevv1connect.UnimplementedSessionServiceHandler
	t            *testing.T
	id, request  domain.ID
	compactCalls atomic.Int32
}

func (s *compactionCLIService) CompactSession(_ context.Context, r *connect.Request[pb.CompactSessionRequest]) (*connect.Response[pb.CompactSessionResponse], error) {
	if r.Header().Get("Authorization") != "Bearer private-cli-fixture" || r.Msg.Mutation.Id != string(s.id) || r.Msg.Mutation.RequestId != string(s.request) || r.Msg.Mutation.ExpectedRevision != 7 {
		s.t.Error("CLI replaced authenticated mutation identity")
	}
	s.compactCalls.Add(1)
	return connect.NewResponse(&pb.CompactSessionResponse{Job: &pb.Resource{Id: string(domain.NewID()), Revision: 1, DocumentJson: []byte(`{"type":"compact-session","state":"queued"}`)}, RequestId: string(s.request)}), nil
}
func (s *compactionCLIService) GetSessionContext(_ context.Context, r *connect.Request[pb.GetSessionContextRequest]) (*connect.Response[pb.GetSessionContextResponse], error) {
	if r.Header().Get("Authorization") != "Bearer private-cli-fixture" || r.Msg.SessionId != string(s.id) {
		s.t.Error("CLI context lost owner/session identity")
	}
	return connect.NewResponse(&pb.GetSessionContextResponse{DocumentJson: []byte(`{"current_tokens":null,"manual_action":null}`), Capabilities: []pb.SessionContextCapability{pb.SessionContextCapability_SESSION_CONTEXT_CAPABILITY_CLAUDE_NATIVE_OBSERVATIONS_V1}}), nil
}

func TestSessionCompactionCLIConnectParity(t *testing.T) {
	service := &compactionCLIService{t: t, id: domain.NewID(), request: domain.NewID()}
	_, handler := delidevv1connect.NewSessionServiceHandler(service)
	server := httptest.NewServer(handler)
	defer server.Close()
	c := client{token: "private-cli-fixture", sessions: delidevv1connect.NewSessionServiceClient(http.DefaultClient, server.URL)}
	o := options{requestID: service.request}
	value, e := sessionCompactionCommand(context.Background(), c, o, []string{"context", "--id", string(service.id)})
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(value)
	var view map[string]any
	json.Unmarshal(raw, &view)
	if view["context"].(map[string]any)["current_tokens"] != nil || len(view["capabilities"].([]any)) != 1 || service.compactCalls.Load() != 0 {
		t.Fatal("read inferred measured tokens or dispatched an action")
	}
	value, e = sessionCompactionCommand(context.Background(), c, o, []string{"compact", "--id", string(service.id), "--revision", "7"})
	if e != nil || value.(map[string]any)["request_id"] != string(service.request) || service.compactCalls.Load() != 1 {
		t.Fatal("manual command lost RPC/receipt parity", e)
	}
	if _, e := sessionCompactionCommand(context.Background(), c, o, []string{"compact", "--id", string(service.id)}); domain.SafeError(e).Code != domain.MissingInput || service.compactCalls.Load() != 1 {
		t.Fatal("missing revision invoked compaction", e)
	}
}
