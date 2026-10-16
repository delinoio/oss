// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func TestWatchSlotsBoundEachClassIndependently(t *testing.T) {
	slots := newWatchSlots()
	ordinary := make([]domain.ID, watchOrdinaryLaneBound)
	for i := range ordinary {
		ordinary[i] = domain.NewID()
		if !slots.acquire(ordinary[i], watchSlotOrdinary) {
			t.Fatal("an ordinary slot within the bound was refused", i)
		}
	}
	if slots.acquire(domain.NewID(), watchSlotOrdinary) {
		t.Fatal("an ordinary operation exceeded its bound")
	}
	recoveries := make([]domain.ID, codexRecoveryLaneBound)
	for i := range recoveries {
		recoveries[i] = domain.NewID()
		if !slots.acquire(recoveries[i], watchSlotRecovery) {
			t.Fatal("a full ordinary class consumed the reserved recovery capacity", i)
		}
	}
	if slots.acquire(domain.NewID(), watchSlotRecovery) {
		t.Fatal("a recovery operation exceeded its bound")
	}
	if slots.acquire(recoveries[0], watchSlotRecovery) || slots.acquire(ordinary[0], watchSlotOrdinary) {
		t.Fatal("a key already in flight was admitted twice")
	}
	slots.release(recoveries[0])
	slots.release(recoveries[0])
	if !slots.acquire(domain.NewID(), watchSlotRecovery) {
		t.Fatal("a released recovery slot was not reusable")
	}
	if slots.acquire(domain.NewID(), watchSlotRecovery) {
		t.Fatal("a repeated release freed more than one slot")
	}
	slots.release(ordinary[1])
	if !slots.acquire(domain.NewID(), watchSlotOrdinary) {
		t.Fatal("a released ordinary slot was not reusable")
	}
}

type starvedRecoveryServer struct {
	delidevv1connect.UnimplementedSubscriptionServiceHandler
	accounts  []*pb.Resource
	mu        sync.Mutex
	takes     int
	recovered map[string]int
}

func (s *starvedRecoveryServer) WatchSubscription(ctx context.Context, _ *connect.Request[pb.WatchSubscriptionRequest], stream *connect.ServerStream[pb.WatchSubscriptionResponse]) error {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		for _, r := range s.accounts {
			if err := stream.Send(&pb.WatchSubscriptionResponse{Account: r}); err != nil {
				return err
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// Take holds its ordinary slot until the caller cancels the lane.
func (s *starvedRecoveryServer) TakeSubscription(ctx context.Context, _ *connect.Request[pb.TakeSubscriptionRequest]) (*connect.Response[pb.TakeSubscriptionResponse], error) {
	s.mu.Lock()
	s.takes++
	s.mu.Unlock()
	<-ctx.Done()
	return nil, ctx.Err()
}

// No retained journal exists for the fenced account, so its recovery attempt
// ends with one blocked report that identifies the account.
func (s *starvedRecoveryServer) RecoverSubscription(_ context.Context, req *connect.Request[pb.RecoverSubscriptionRequest]) (*connect.Response[pb.RecoverSubscriptionResponse], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recovered[req.Msg.Mutation.Id]++
	return connect.NewResponse(&pb.RecoverSubscriptionResponse{Outcome: pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_BLOCKED, BlockedReason: req.Msg.BlockedReason}), nil
}

// A continuous backlog of ordinary rows that fills the ordinary slots must not
// prevent a fenced account from being reconciled.
func TestWatchLaneReservesRecoveryCapacityAgainstOrdinaryBacklog(t *testing.T) {
	credential := Credential{MachineID: domain.NewID(), ServerID: domain.NewID(), DeviceID: domain.NewID(), Token: "synthetic-token"}
	server := &starvedRecoveryServer{recovered: map[string]int{}}
	for i := 0; i < watchOrdinaryLaneBound+2; i++ {
		raw, err := json.Marshal(domain.Account{Subscription: &domain.SubscriptionState{Pending: &domain.SubscriptionOperation{ID: domain.NewID(), Action: domain.SubscriptionLogin, MachineID: credential.MachineID, Actor: domain.Principal{Type: domain.OwnerDevice}, Phase: domain.SubscriptionQueued}}})
		if err != nil {
			t.Fatal(err)
		}
		server.accounts = append(server.accounts, &pb.Resource{Id: string(domain.NewID()), Revision: 2, DocumentJson: raw})
	}
	lease := domain.SubscriptionLease{ID: domain.NewID(), OperationID: domain.NewID(), Revision: 3, Action: domain.SubscriptionExecute, MachineID: credential.MachineID, InstanceID: domain.NewID(), DeviceID: credential.DeviceID, Epoch: domain.NewID(), Generation: domain.NewID(), StartedAt: time.Now().UTC()}
	raw, err := json.Marshal(domain.Account{Type: domain.SubscriptionAccount, SubscriptionService: domain.SubscriptionChatGPT, Subscription: &domain.SubscriptionState{RecoveryRequired: true, Lease: &lease, Generation: lease.Generation}})
	if err != nil {
		t.Fatal(err)
	}
	recoveryAccount := string(domain.NewID())
	server.accounts = append(server.accounts, &pb.Resource{Id: recoveryAccount, Revision: 4, Kind: pb.EntityKind_ENTITY_KIND_ACCOUNT, DocumentJson: raw})
	_, handler := delidevv1connect.NewSubscriptionServiceHandler(server)
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()
	credential.Endpoint = httpServer.URL
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- watchSubscriptions(ctx, Config{Root: t.TempDir(), codexRecovery: true}, credential, domain.NewID())
	}()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		server.mu.Lock()
		takes, recovered := server.takes, server.recovered[recoveryAccount]
		server.mu.Unlock()
		if takes >= watchOrdinaryLaneBound && recovered > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	server.mu.Lock()
	takes, recovered := server.takes, server.recovered[recoveryAccount]
	server.mu.Unlock()
	if takes != watchOrdinaryLaneBound {
		t.Fatal("the ordinary rows did not hold exactly their own slots", takes)
	}
	if recovered == 0 {
		t.Fatal("a saturated ordinary backlog starved the fenced account's recovery")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the watch lane did not join on shutdown")
	}
}
