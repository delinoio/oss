package cli

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type subscriptionCLIClient struct {
	delidevv1connect.SubscriptionServiceClient
	request  *pb.RequestSubscriptionRequest
	cancel   *pb.CancelSubscriptionRequest
	progress *pb.GetSubscriptionProgressRequest
}

func (f *subscriptionCLIClient) RequestSubscription(_ context.Context, req *connect.Request[pb.RequestSubscriptionRequest]) (*connect.Response[pb.RequestSubscriptionResponse], error) {
	f.request = req.Msg
	return connect.NewResponse(&pb.RequestSubscriptionResponse{OperationId: req.Msg.Mutation.RequestId}), nil
}
func (f *subscriptionCLIClient) CancelSubscription(_ context.Context, req *connect.Request[pb.CancelSubscriptionRequest]) (*connect.Response[pb.CancelSubscriptionResponse], error) {
	f.cancel = req.Msg
	return connect.NewResponse(&pb.CancelSubscriptionResponse{}), nil
}
func (f *subscriptionCLIClient) GetSubscriptionProgress(_ context.Context, req *connect.Request[pb.GetSubscriptionProgressRequest]) (*connect.Response[pb.GetSubscriptionProgressResponse], error) {
	f.progress = req.Msg
	return connect.NewResponse(&pb.GetSubscriptionProgressResponse{Url: "https://auth.openai.com/codex/device", UserCode: "TEST-1234"}), nil
}

func TestCLISubscriptionExplicitWorkerAndOriginalOperation(t *testing.T) {
	id, machine, requestID := domain.NewID(), domain.NewID(), domain.NewID()
	for _, operation := range []string{"login", "refresh", "logout"} {
		f := &subscriptionCLIClient{}
		c := client{subscriptions: f}
		args := []string{operation, "--id", string(id), "--revision", "7", "--machine-id", string(machine)}
		if operation == "login" {
			args = append(args, "--device-code")
		}
		if _, err := accountCommand(context.Background(), c, options{requestID: requestID}, args, IO{}); err != nil {
			t.Fatal(err)
		}
		if f.request == nil || f.request.Mutation.RequestId != string(requestID) || f.request.Mutation.ExpectedRevision != 7 || f.request.MachineId != string(machine) || f.request.DeviceCode != (operation == "login") {
			t.Fatal("CLI lost explicit ownership or original receipt")
		}
		if _, err := accountCommand(context.Background(), c, options{}, args[:5], IO{}); err == nil {
			t.Fatal("missing Worker accepted")
		}
	}
	f := &subscriptionCLIClient{}
	c := client{subscriptions: f}
	if _, err := accountCommand(context.Background(), c, options{requestID: requestID}, []string{"cancel-login", "--id", string(id), "--revision", "7"}, IO{}); err != nil {
		t.Fatal(err)
	}
	if f.cancel == nil || f.cancel.Mutation.RequestId != string(requestID) {
		t.Fatal("cancellation did not retain the mutation receipt")
	}
	value, err := accountCommand(context.Background(), c, options{}, []string{"login-progress", "--id", string(id), "--operation-id", string(requestID)}, IO{})
	if err != nil || f.progress == nil || f.progress.OperationId != string(requestID) || value.(map[string]any)["user_code"] != "TEST-1234" {
		t.Fatal("progress did not use the original operation", err)
	}
}
