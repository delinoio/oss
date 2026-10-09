// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"connectrpc.com/connect"
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"google.golang.org/protobuf/proto"
	"testing"
	"time"
)

func TestAutomaticCreditObserverRemovalJoinsOriginalOwner(t *testing.T) {
	registry := &managedObservationRegistry{}
	account := domain.NewID()
	remove := registry.register(account, nil, nil)
	owner := registry.owners[account]
	owner.mu.Lock()
	done := make(chan struct{})
	go func() { remove(); close(done) }()
	deadline := time.After(time.Second)
	for {
		registry.mu.Lock()
		_, present := registry.owners[account]
		registry.mu.Unlock()
		if !present {
			break
		}
		select {
		case <-deadline:
			t.Fatal("owner not fenced")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	select {
	case <-done:
		t.Fatal("cleanup overtook retained observation")
	default:
	}
	if handled, _ := registry.run(context.Background(), account, domain.SubscriptionObservationOperation{}); handled {
		t.Fatal("fenced owner acquired new observation")
	}
	owner.mu.Unlock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("original observer not joined")
	}
	remove()
}
func TestAutomaticCreditLateRemovalPreservesReplacementOwner(t *testing.T) {
	registry := &managedObservationRegistry{}
	account := domain.NewID()
	old := registry.register(account, nil, nil)
	registry.register(account, nil, nil)
	replacement := registry.owners[account]
	old()
	if registry.owners[account] != replacement || replacement.closed {
		t.Fatal("old cleanup removed replacement owner")
	}
}

type automaticMarkerReceiptRPC struct {
	delidevv1connect.SubscriptionServiceClient
	t             *testing.T
	first         *pb.PublishSubscriptionObservationRequest
	account       *pb.Resource
	calls, claims int
	failure       connect.Code
	persistent    bool
	operation     domain.ID
}

func (f *automaticMarkerReceiptRPC) PublishSubscriptionObservation(_ context.Context, r *connect.Request[pb.PublishSubscriptionObservationRequest]) (*connect.Response[pb.PublishSubscriptionObservationResponse], error) {
	f.calls++
	if f.first == nil {
		f.first = proto.Clone(r.Msg).(*pb.PublishSubscriptionObservationRequest)
	} else if !proto.Equal(f.first, r.Msg) {
		f.t.Fatal("receipt replay replaced original marker mutation")
	}
	if f.calls == 1 || f.persistent {
		return nil, connect.NewError(f.failure, nil)
	}
	return connect.NewResponse(&pb.PublishSubscriptionObservationResponse{Account: f.account, Replayed: true}), nil
}
func (f *automaticMarkerReceiptRPC) ClaimSubscriptionObservation(_ context.Context, r *connect.Request[pb.ClaimSubscriptionObservationRequest]) (*connect.Response[pb.ClaimSubscriptionObservationResponse], error) {
	f.claims++
	if domain.ID(r.Msg.OperationId) != f.operation || r.Msg.LeaseId != f.first.LeaseId || r.Msg.GenerationId != f.first.GenerationId {
		f.t.Fatal("replayed receipt used foreign native authority")
	}
	return nil, connect.NewError(connect.CodeFailedPrecondition, nil)
}
func TestAutomaticCreditMarkerReceiptReplayBeforeOwnerRelease(t *testing.T) {
	for _, scenario := range []string{"lost-once", "persistent-loss", "definitive"} {
		t.Run(scenario, func(t *testing.T) {
			account, lease, generation, operation := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
			block := domain.SubscriptionQuotaBlock{SessionID: domain.NewID(), ExecutionID: domain.NewID(), NativeThreadID: domain.NativeIdentity(domain.NewID()), NativeTurnID: domain.NativeIdentity(domain.NewID()), Reason: domain.CodexUsageLimitExceeded}
			raw, _ := json.Marshal(domain.Account{Subscription: &domain.SubscriptionState{Observation: &domain.SubscriptionObservationOperation{ID: operation, Action: domain.SubscriptionResetCredit, Phase: domain.SubscriptionObservationQueued, AutomaticLeaseID: lease, AutomaticBlock: &block}}})
			f := &automaticMarkerReceiptRPC{t: t, failure: connect.CodeUnavailable, persistent: scenario == "persistent-loss", operation: operation, account: &pb.Resource{DocumentJson: raw}}
			if scenario == "definitive" {
				f.failure = connect.CodePermissionDenied
			}
			owner := &managedSubscriptionLease{client: f, account: account, instance: domain.NewID(), credential: Credential{MachineID: domain.NewID()}, response: &pb.TakeSubscriptionResponse{LeaseId: string(lease), LeaseRevision: 7, GenerationId: string(generation)}}
			registry := &managedObservationRegistry{}
			remove := registry.register(account, nil, owner)
			owner.publishQuotaBlock(context.Background(), registry, block)
			remove()
			wantCalls, wantClaims := 2, 0
			if scenario == "lost-once" {
				wantClaims = 1
			}
			if scenario == "definitive" {
				wantCalls = 1
			}
			if f.calls != wantCalls || f.claims != wantClaims {
				t.Fatalf("%s calls=%d claims=%d", scenario, f.calls, f.claims)
			}
		})
	}
}
