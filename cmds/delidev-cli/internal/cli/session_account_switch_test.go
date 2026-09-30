// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type accountSwitchStatusStub struct {
	delidevv1connect.SystemServiceClient
	capabilities []pb.SystemCapability
	err          error
	calls        int
}

func (s *accountSwitchStatusStub) GetStatus(context.Context, *connect.Request[pb.GetStatusRequest]) (*connect.Response[pb.GetStatusResponse], error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return connect.NewResponse(&pb.GetStatusResponse{Capabilities: s.capabilities}), nil
}

type accountSwitchSessionStub struct {
	delidevv1connect.SessionServiceClient
	requests []*pb.SwitchSessionAccountRequest
}

func (s *accountSwitchSessionStub) SwitchSessionAccount(_ context.Context, request *connect.Request[pb.SwitchSessionAccountRequest]) (*connect.Response[pb.SwitchSessionAccountResponse], error) {
	s.requests = append(s.requests, request.Msg)
	return connect.NewResponse(&pb.SwitchSessionAccountResponse{Change: &pb.SessionChange{}}), nil
}

func TestCLISwitchAccountNegotiatesSupportAndPreservesExactReceipt(t *testing.T) {
	status, sessions := &accountSwitchStatusStub{}, &accountSwitchSessionStub{}
	c := client{system: status, sessions: sessions}
	id, account, requestID := domain.NewID(), domain.NewID(), domain.NewID()
	args := []string{"switch-account", "--id", string(id), "--revision", "9007199254740993", "--account-id", string(account)}
	opts := options{requestID: requestID}
	if _, err := sessionCommand(context.Background(), c, opts, args, IO{}); domain.SafeError(err).Code != domain.Unsupported {
		t.Fatalf("legacy server was not refused: %v", err)
	}
	if len(sessions.requests) != 0 {
		t.Fatal("mutation preceded capability negotiation")
	}
	status.err = connect.NewError(connect.CodeUnavailable, nil)
	if _, err := sessionCommand(context.Background(), c, opts, args, IO{}); err == nil || len(sessions.requests) != 0 {
		t.Fatal("failed status permitted mutation")
	}
	status.err = nil
	status.capabilities = []pb.SystemCapability{pb.SystemCapability_SYSTEM_CAPABILITY_STOPPED_CODEX_ACCOUNT_SWITCH_V1}
	for range 2 {
		if _, err := sessionCommand(context.Background(), c, opts, args, IO{}); err != nil {
			t.Fatal(err)
		}
	}
	if len(sessions.requests) != 2 {
		t.Fatal("explicit receipt retry was lost")
	}
	for _, request := range sessions.requests {
		if request.AccountId != string(account) || request.Mutation.Id != string(id) || request.Mutation.RequestId != string(requestID) || request.Mutation.ExpectedRevision != 9007199254740993 {
			t.Fatalf("request identity or exact revision changed: %+v", request)
		}
	}
}
