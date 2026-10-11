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

type backgroundCLIService struct {
	delidevv1connect.UnimplementedSessionServiceHandler
	t                                   *testing.T
	session, execution, action, receipt domain.ID
	mutations, reads                    int
}

func (s *backgroundCLIService) GetNativeBackground(_ context.Context, r *connect.Request[pb.GetNativeBackgroundRequest]) (*connect.Response[pb.GetNativeBackgroundResponse], error) {
	s.reads++
	if r.Msg.SessionId != string(s.session) {
		s.t.Error("read changed original session")
	}
	return connect.NewResponse(&pb.GetNativeBackgroundResponse{Session: &pb.Resource{Id: string(s.session), DocumentJson: []byte(`{"native_background":{"owner_lost":true}}`)}}), nil
}
func (s *backgroundCLIService) RequestNativeBackground(_ context.Context, r *connect.Request[pb.RequestNativeBackgroundRequest]) (*connect.Response[pb.RequestNativeBackgroundResponse], error) {
	s.mutations++
	if r.Header().Get("Authorization") != "Bearer original-cli" || r.Msg.Mutation.Id != string(s.session) || r.Msg.Mutation.RequestId != string(s.receipt) || r.Msg.Mutation.ExpectedRevision != 7 || r.Msg.InventorySequence != 3 || r.Msg.ExecutionId != string(s.execution) || r.Msg.Operation != pb.NativeBackgroundOperation_NATIVE_BACKGROUND_OPERATION_TERMINATE || r.Msg.ItemId != "original-item" || r.Msg.ProcessId != "500" {
		s.t.Error("CLI changed selected native control bytes/owner")
	}
	return connect.NewResponse(&pb.RequestNativeBackgroundResponse{Action: &pb.Resource{Id: string(s.action), Revision: 1, DocumentJson: []byte(`{"state":"queued"}`)}, RequestId: string(s.receipt)}), nil
}
func TestNativeBackgroundCLIUsesConnectReceiptAndRetainedOnlyStatus(t *testing.T) {
	s := &backgroundCLIService{t: t, session: domain.NewID(), execution: domain.NewID(), action: domain.NewID(), receipt: domain.NewID()}
	_, handler := delidevv1connect.NewSessionServiceHandler(s)
	server := httptest.NewServer(handler)
	defer server.Close()
	c := client{token: "original-cli", sessions: delidevv1connect.NewSessionServiceClient(http.DefaultClient, server.URL)}
	if _, e := sessionNativeBackgroundCommand(context.Background(), c, options{requestID: s.receipt}, []string{"status", "--id", string(s.session)}); e != nil || s.reads != 1 || s.mutations != 0 {
		t.Fatal("status attempted native control", e)
	}
	args := []string{"terminate", "--id", string(s.session), "--execution", string(s.execution), "--revision", "7", "--inventory-sequence", "3", "--item", "original-item", "--process", "500", "--wait=false"}
	if _, e := sessionNativeBackgroundCommand(context.Background(), c, options{requestID: s.receipt}, args); e != nil || s.mutations != 1 {
		t.Fatal("exact CLI control lost Connect parity", e)
	}
	for _, bad := range [][]string{{"terminate", "--id", string(s.session), "--process", "999"}, {"clean", "--id", string(s.session), "--execution", string(s.execution), "--revision", "7", "--inventory-sequence", "3", "--process", "500"}} {
		if _, e := sessionNativeBackgroundCommand(context.Background(), c, options{requestID: domain.NewID()}, bad); e == nil || s.mutations != 1 {
			t.Fatal("incomplete or mixed native scope was sent")
		}
	}
}
