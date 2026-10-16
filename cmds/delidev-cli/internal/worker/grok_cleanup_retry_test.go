// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

// grokFinishRecorder records the Finish requests a lease reports.
type grokFinishRecorder struct {
	delidevv1connect.UnimplementedSubscriptionServiceHandler
	mu       sync.Mutex
	finishes []*pb.FinishSubscriptionRequest
}

func (r *grokFinishRecorder) FinishSubscription(_ context.Context, req *connect.Request[pb.FinishSubscriptionRequest]) (*connect.Response[pb.FinishSubscriptionResponse], error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.finishes = append(r.finishes, req.Msg)
	return connect.NewResponse(&pb.FinishSubscriptionResponse{}), nil
}

func newGrokRetryLease(t *testing.T, action pb.SubscriptionAction) (*managedSubscriptionLease, *grokFinishRecorder, string) {
	t.Helper()
	recorder := &grokFinishRecorder{}
	mux := http.NewServeMux()
	mux.Handle(delidevv1connect.NewSubscriptionServiceHandler(recorder))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	serverID, account, generation := domain.NewID(), domain.NewID(), domain.NewID()
	native, err := subscription.ParseGrokAuthFile(grokWorkerFile(t, "synthetic-principal-1", "one", "2026-10-14T06:00:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	home := grokHome(t)
	lease := grokWorkerLease(t, serverID, account, generation, mustSeal(t, serverID, account, generation, native))
	lease.client = delidevv1connect.NewSubscriptionServiceClient(http.DefaultClient, server.URL)
	lease.credential.Token = "synthetic-worker-token"
	lease.journalPath = filepath.Join(t.TempDir(), "journal.json")
	lease.journal = managedSubscriptionJournal{Version: 1, Account: account, Action: action, Finish: lease.finishID, Generation: generation}
	return lease, recorder, home
}

// Fault injection: the credential file cannot be proven removed, so the first
// Finish reports the cleanup unconfirmed and keeps the bound home. The same
// lease then retries under a fresh request identity once the obstruction is gone,
// and its confirmed Finish releases the home.
func TestGrokUnconfirmedHomeCleanupIsRetriedByTheSameLease(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory replacement semantics differ")
	}
	lease, recorder, home := newGrokRetryLease(t, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT)
	original, err := lease.materializeGrok(home)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, subscription.GrokAuthFileName)
	stuck := filepath.Join(home, security.OwnedTemporaryPrefix(path)+"stuck")
	if err := os.Mkdir(stuck, 0o700); err != nil {
		t.Fatal(err)
	}
	firstID := lease.finishID
	if err := lease.finishGrok(original, true); err != nil {
		t.Fatal(err)
	}
	if len(recorder.finishes) != 1 || recorder.finishes[0].CleanupConfirmed || len(recorder.finishes[0].Bundle) != 0 {
		t.Fatal("an unremoved remnant must be reported as unconfirmed cleanup without a bundle")
	}
	if lease.grokRetry == nil || lease.grokHome.Path() == "" {
		t.Fatal("the lease did not keep its bound home for the retry")
	}
	// The retry before the obstruction is removed stays unconfirmed.
	if err := lease.retryGrokCleanup(); err != nil {
		t.Fatal(err)
	}
	if len(recorder.finishes) != 2 || recorder.finishes[1].CleanupConfirmed || lease.grokRetry == nil {
		t.Fatal("a retry that could not remove the remnant must stay unconfirmed")
	}
	if err := os.Remove(stuck); err != nil {
		t.Fatal(err)
	}
	if err := lease.retryGrokCleanup(); err != nil {
		t.Fatal(err)
	}
	if len(recorder.finishes) != 3 {
		t.Fatalf("finish reports = %d", len(recorder.finishes))
	}
	last := recorder.finishes[2]
	if !last.CleanupConfirmed || !last.Succeeded || last.Mutation.RequestId == recorder.finishes[0].Mutation.RequestId || last.Mutation.RequestId == recorder.finishes[1].Mutation.RequestId || last.Mutation.RequestId == string(firstID) {
		t.Fatal("the confirmed retry must be a fresh, successful, cleanup-confirmed Finish")
	}
	if lease.grokRetry != nil || lease.grokHome.Path() != "" {
		t.Fatal("a confirmed cleanup must release the bound home")
	}
	if err := lease.retryGrokCleanup(); err == nil {
		t.Fatal("a released lease accepted another retry")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("the credential file survived")
	}
}
