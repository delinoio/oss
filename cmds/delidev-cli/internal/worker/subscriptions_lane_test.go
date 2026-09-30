// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"google.golang.org/protobuf/proto"
)

type lostSubscriptionCompletion struct {
	delidevv1connect.UnimplementedSubscriptionServiceHandler
	account        *pb.Resource
	finished       chan *pb.FinishSubscriptionRequest
	closed         chan struct{}
	finishError    error
	takes          chan *pb.TakeSubscriptionRequest
	firstTake      sync.Once
	firstTakeError error
}

func (f *lostSubscriptionCompletion) WatchSubscription(ctx context.Context, _ *connect.Request[pb.WatchSubscriptionRequest], stream *connect.ServerStream[pb.WatchSubscriptionResponse]) error {
	defer close(f.closed)
	if err := stream.Send(&pb.WatchSubscriptionResponse{Account: f.account}); err != nil {
		return err
	}
	<-ctx.Done()
	return ctx.Err()
}

func (f *lostSubscriptionCompletion) TakeSubscription(_ context.Context, req *connect.Request[pb.TakeSubscriptionRequest]) (*connect.Response[pb.TakeSubscriptionResponse], error) {
	if f.takes != nil {
		f.takes <- proto.Clone(req.Msg).(*pb.TakeSubscriptionRequest)
		var firstError error
		f.firstTake.Do(func() { firstError = f.firstTakeError })
		if firstError != nil {
			return nil, firstError
		}
	}
	// Invalid installation data stops before native work, but still requires
	// the original claim's completion to be reported on the protected lane.
	return connect.NewResponse(&pb.TakeSubscriptionResponse{LeaseId: req.Msg.Mutation.RequestId, LeaseRevision: 3, InstallationJson: []byte(`{}`)}), nil
}

func TestManagedLifecycleBusyTakeWaitsWithOriginalClaimAndKeepsLane(t *testing.T) {
	for _, action := range []domain.SubscriptionAction{domain.SubscriptionLogin, domain.SubscriptionRefresh, domain.SubscriptionLogout} {
		t.Run(string(action), func(t *testing.T) {
			root, f, credential := managedLaneFixture(t, nil)
			var account domain.Account
			if err := domain.Decode(f.account.DocumentJson, &account); err != nil {
				t.Fatal(err)
			}
			account.Subscription.Pending.Action = action
			f.account.DocumentJson, _ = json.Marshal(account)
			f.takes = make(chan *pb.TakeSubscriptionRequest, 4)
			f.firstTakeError = rpc.Error(domain.Fail(domain.ResourceExhausted, "The original account lease is busy.", "Wait for the original owner."), "")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			result := make(chan error, 1)
			go func() { result <- watchSubscriptions(ctx, Config{Root: root}, credential, domain.NewID()) }()
			select {
			case <-f.finished:
			case err := <-result:
				t.Fatal("a definite busy refusal closed the shared ownership lane", err)
			case <-ctx.Done():
				t.Fatal("lifecycle operation did not resume after the busy lease cleared")
			}
			first, second := <-f.takes, <-f.takes
			if !proto.Equal(first, second) {
				t.Fatal("busy retry replaced its original protected claim")
			}
			select {
			case err := <-result:
				t.Fatal("settled lifecycle failure closed unrelated account ownership", err)
			case <-time.After(100 * time.Millisecond):
			}
			cancel()
			select {
			case <-result:
			case <-time.After(time.Second):
				t.Fatal("lane did not join after caller cancellation")
			}
			entries, err := os.ReadDir(filepath.Join(root, "managed-auth"))
			if err != nil || len(entries) != 1 {
				t.Fatal("busy retry created another ownership claim", err)
			}
			select {
			case <-f.takes:
				t.Fatal("lifecycle Take was replayed after delivery")
			default:
			}
		})
	}
}

func TestManagedLifecycleUnknownTakeClosesLaneWithoutRetry(t *testing.T) {
	root, f, credential := managedLaneFixture(t, nil)
	f.takes = make(chan *pb.TakeSubscriptionRequest, 4)
	f.firstTakeError = connect.NewError(connect.CodeUnavailable, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := watchSubscriptions(ctx, Config{Root: root}, credential, domain.NewID()); domain.SafeError(err).Code != domain.ServerUnavailable {
		t.Fatal("unknown Take lost its uncertainty", err)
	}
	if len(f.takes) != 1 {
		t.Fatal("unknown delivery was retried")
	}
	select {
	case <-f.finished:
		t.Fatal("unknown delivery invented protected completion")
	default:
	}
	select {
	case <-f.closed:
	case <-time.After(time.Second):
		t.Fatal("server did not observe lost lane ownership")
	}
}

func (f *lostSubscriptionCompletion) FinishSubscription(_ context.Context, req *connect.Request[pb.FinishSubscriptionRequest]) (*connect.Response[pb.FinishSubscriptionResponse], error) {
	f.finished <- req.Msg
	if f.finishError != nil {
		return nil, f.finishError
	}
	return connect.NewResponse(&pb.FinishSubscriptionResponse{}), nil
}

func managedLaneFixture(t *testing.T, finishError error) (string, *lostSubscriptionCompletion, Credential) {
	t.Helper()
	root := t.TempDir()
	accountID, operationID, machineID := domain.NewID(), domain.NewID(), domain.NewID()
	document, err := json.Marshal(domain.Account{Subscription: &domain.SubscriptionState{Pending: &domain.SubscriptionOperation{ID: operationID, Action: domain.SubscriptionLogin, MachineID: machineID, Actor: domain.Principal{Type: domain.OwnerDevice}, Phase: domain.SubscriptionQueued}}})
	if err != nil {
		t.Fatal(err)
	}
	f := &lostSubscriptionCompletion{account: &pb.Resource{Id: string(accountID), Revision: 2, DocumentJson: document}, finished: make(chan *pb.FinishSubscriptionRequest, 1), closed: make(chan struct{}), finishError: finishError}
	_, handler := delidevv1connect.NewSubscriptionServiceHandler(f)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return root, f, Credential{Endpoint: server.URL, MachineID: machineID}
}

func TestManagedSubscriptionUncertainFinishClosesLane(t *testing.T) {
	root, f, credential := managedLaneFixture(t, connect.NewError(connect.CodeUnavailable, nil))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := watchSubscriptions(ctx, Config{Root: root}, credential, domain.NewID())
	if ctx.Err() != nil {
		t.Fatal("uncertain completion left the subscription lane open until the parent deadline")
	}
	if domain.SafeError(err).Code != domain.ServerUnavailable {
		t.Fatalf("lost completion was not returned: %v", err)
	}
	select {
	case <-f.closed:
	case <-time.After(time.Second):
		t.Fatal("server did not observe subscription ownership loss")
	}
	select {
	case finish := <-f.finished:
		raw, err := security.ReadPrivate(filepath.Join(root, "managed-auth", finish.LeaseId+".json"), 4096)
		var journal managedSubscriptionJournal
		if err != nil || domain.Decode(raw, &journal) != nil || journal.State != managedClosed || journal.Finish != domain.ID(finish.Mutation.RequestId) {
			t.Fatal("uncertain completion lost its original reconciliation journal", err)
		}
	default:
		t.Fatal("no completion was attempted")
	}
}

func TestManagedSubscriptionAcknowledgedFailureKeepsLane(t *testing.T) {
	root, f, credential := managedLaneFixture(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- watchSubscriptions(ctx, Config{Root: root}, credential, domain.NewID()) }()
	var finish *pb.FinishSubscriptionRequest
	select {
	case finish = <-f.finished:
	case <-ctx.Done():
		t.Fatal("no completion was attempted")
	}
	select {
	case err := <-result:
		t.Fatalf("acknowledged failure interrupted unrelated lane ownership: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	select {
	case <-result:
	case <-time.After(time.Second):
		t.Fatal("lane ignored explicit caller cancellation")
	}
	raw, err := security.ReadPrivate(filepath.Join(root, "managed-auth", finish.LeaseId+".json"), 4096)
	var journal managedSubscriptionJournal
	if err != nil || domain.Decode(raw, &journal) != nil || journal.State != managedReported {
		t.Fatal("acknowledged failure lost its confirmed completion", err)
	}
}
