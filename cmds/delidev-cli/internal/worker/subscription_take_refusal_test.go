// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"connectrpc.com/connect"
	"context"
	"encoding/json"
	"errors"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"net/http/httptest"
	"testing"
	"time"
)

type independentTakeRefusal struct {
	delidevv1connect.UnimplementedSubscriptionServiceHandler
	a, b                                              *pb.Resource
	bStarted, releaseB, aRefused, bCanceled, finished chan struct{}
}

func (f *independentTakeRefusal) WatchSubscription(ctx context.Context, _ *connect.Request[pb.WatchSubscriptionRequest], stream *connect.ServerStream[pb.WatchSubscriptionResponse]) error {
	if err := stream.Send(&pb.WatchSubscriptionResponse{Account: f.b}); err != nil {
		return err
	}
	select {
	case <-f.bStarted:
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := stream.Send(&pb.WatchSubscriptionResponse{Account: f.a}); err != nil {
		return err
	}
	<-ctx.Done()
	return ctx.Err()
}
func (f *independentTakeRefusal) TakeSubscription(ctx context.Context, r *connect.Request[pb.TakeSubscriptionRequest]) (*connect.Response[pb.TakeSubscriptionResponse], error) {
	if r.Msg.Mutation.Id == f.a.Id {
		close(f.aRefused)
		p := domain.Fail(domain.Canceled, "Original request retired.", "")
		p.Cause = "subscription_take_not_admitted"
		return nil, rpc.Error(p, "")
	}
	close(f.bStarted)
	select {
	case <-ctx.Done():
		close(f.bCanceled)
		return nil, ctx.Err()
	case <-f.releaseB:
	}
	return connect.NewResponse(&pb.TakeSubscriptionResponse{LeaseId: r.Msg.Mutation.RequestId, LeaseRevision: 3, InstallationJson: []byte(`{}`)}), nil
}
func (f *independentTakeRefusal) FinishSubscription(_ context.Context, r *connect.Request[pb.FinishSubscriptionRequest]) (*connect.Response[pb.FinishSubscriptionResponse], error) {
	if r.Msg.Mutation.Id != f.b.Id {
		return nil, connect.NewError(connect.CodeInternal, nil)
	}
	close(f.finished)
	return connect.NewResponse(&pb.FinishSubscriptionResponse{}), nil
}
func TestManagedLifecycleRefusalPreservesOtherAccountOperation(t *testing.T) {
	machine := domain.NewID()
	account := func() *pb.Resource {
		raw, _ := json.Marshal(domain.Account{Subscription: &domain.SubscriptionState{Pending: &domain.SubscriptionOperation{ID: domain.NewID(), Action: domain.SubscriptionLogin, MachineID: machine, Actor: domain.Principal{Type: domain.OwnerDevice}, Phase: domain.SubscriptionQueued}}})
		return &pb.Resource{Id: string(domain.NewID()), Revision: 2, DocumentJson: raw}
	}
	f := &independentTakeRefusal{a: account(), b: account(), bStarted: make(chan struct{}), releaseB: make(chan struct{}), aRefused: make(chan struct{}), bCanceled: make(chan struct{}), finished: make(chan struct{})}
	_, handler := delidevv1connect.NewSubscriptionServiceHandler(f)
	server := httptest.NewServer(handler)
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- watchSubscriptions(ctx, Config{Root: t.TempDir()}, Credential{Endpoint: server.URL, MachineID: machine}, domain.NewID())
	}()
	select {
	case <-f.aRefused:
	case <-time.After(3 * time.Second):
		t.Fatal("A not refused")
	}
	select {
	case <-f.bCanceled:
		t.Fatal("A refusal canceled original B operation")
	case <-done:
		t.Fatal("A refusal closed lane")
	case <-time.After(100 * time.Millisecond):
	}
	close(f.releaseB)
	select {
	case <-f.finished:
	case <-time.After(3 * time.Second):
		t.Fatal("B did not retain original completion")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("lane did not join")
	}
}

func TestManagedExecutionCannotUseLifecycleTakeRefusal(t *testing.T) {
	root, f, credential := managedLaneFixture(t, nil)
	f.takes = make(chan *pb.TakeSubscriptionRequest, 2)
	problem := domain.Fail(domain.Canceled, "Wrong lane proof.", "")
	problem.Cause = "subscription_take_not_admitted"
	f.firstTakeError = rpc.Error(problem, "")
	client, closeHTTP, err := subscriptionRPC(context.Background(), Config{Root: root}, credential)
	if err != nil {
		t.Fatal(err)
	}
	defer closeHTTP()
	lease, err := takeManagedSubscription(context.Background(), Config{Root: root}, client, credential, domain.NewID(), domain.ID(f.account.Id), domain.NewID(), 2, pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE)
	var refused *managedTakeNotAdmitted
	var uncertain *managedExecutionUncertain
	if lease != nil || errors.As(err, &refused) || !errors.As(err, &uncertain) {
		t.Fatal("Execute adopted lifecycle refusal", err)
	}
	if len(f.takes) != 1 {
		t.Fatal("Execute refusal retried")
	}
	select {
	case <-f.finished:
		t.Fatal("Execute refusal invented Finish")
	default:
	}
}
