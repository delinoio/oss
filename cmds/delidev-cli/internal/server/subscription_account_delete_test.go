// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func TestDeleteFailedSubscriptionCleansOriginalLogin(t *testing.T) {
	f, _ := legacyFailedServerLogin(t, false)
	ref := credentials.Ref{Owner: f.input.AccountID, ID: domain.NewID(), Purpose: credentials.AccountLogin}
	if _, err := f.secrets.Put(context.Background(), ref, []byte("synthetic-staged-token")); err != nil {
		t.Fatal(err)
	}
	request := f.failedLoginDeletion()
	if err := f.deleteFailedLogin(request); err != nil {
		t.Fatal(err)
	}
	if _, err := f.secrets.Get(context.Background(), ref); err == nil {
		t.Fatal("protected credentials retained")
	}
	if _, err := f.service.Store.Get(failedLoginContext(), domain.AccountKind, f.input.AccountID); domain.SafeError(err).Code != domain.NotFound {
		t.Fatal("account remains", err)
	}
	if err := f.deleteFailedLogin(request); err != nil {
		t.Fatal("original revision receipt could not replay", err)
	}
	request.Mutation.ExpectedRevision++
	if err := f.deleteFailedLogin(request); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("changed public command replayed", err)
	}
}

func TestDeleteFailedSubscriptionTerminalVaultFailureSurvivesRestart(t *testing.T) {
	f, _ := legacyFailedServerLogin(t, false)
	ref := credentials.Ref{Owner: f.input.AccountID, ID: domain.NewID(), Purpose: credentials.AccountLogin}
	if _, err := f.secrets.Put(context.Background(), ref, []byte("synthetic-staged-token")); err != nil {
		t.Fatal(err)
	}
	f.secrets.deleteError = domain.Fail(domain.Unavailable, "Fixture vault unavailable.", "Retry explicit cleanup.")
	request := f.failedLoginDeletion()
	if err := f.deleteFailedLogin(request); err == nil {
		t.Fatal("vault failure permitted deletion")
	}
	root := f.service.Store.Root()
	if err := f.service.Store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(failedLoginContext(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	f.service.Store = reopened
	f.secrets.deleteError = nil
	if err := f.deleteFailedLogin(request); err == nil {
		t.Fatal("terminal failure retried vault cleanup")
	}
	if _, err := f.secrets.Get(context.Background(), ref); err != nil {
		t.Fatal("original failed request deleted credentials on retry", err)
	}
	if err := f.deleteFailedLogin(f.failedLoginDeletion()); err != nil {
		t.Fatal("fresh explicit deletion failed", err)
	}
}

func TestDeleteFailedSubscriptionRetainsUnconfirmedNative(t *testing.T) {
	f, _ := legacyFailedServerLogin(t, true)
	request := f.failedLoginDeletion()
	if err := f.deleteFailedLogin(request); err == nil {
		t.Fatal("unproven native ownership deleted")
	}
	r, a := f.record()
	if r.Revision != request.Mutation.ExpectedRevision || !a.Subscription.RecoveryRequired {
		t.Fatal("uncertainty lost original ownership")
	}
	if err := f.deleteFailedLogin(request); err == nil {
		t.Fatal("terminal uncertain cleanup retried")
	}
}

func TestDeleteFailedSubscriptionBindsOriginalActorAndConfirmation(t *testing.T) {
	f, _ := legacyFailedServerLogin(t, false)
	ref := credentials.Ref{Owner: f.input.AccountID, ID: domain.NewID(), Purpose: credentials.AccountLogin}
	if _, err := f.secrets.Put(context.Background(), ref, []byte("synthetic-staged-token")); err != nil {
		t.Fatal(err)
	}
	f.secrets.deleteError = domain.Fail(domain.Unavailable, "Fixture vault unavailable.", "Retry explicit cleanup.")
	request := f.failedLoginDeletion()
	if err := f.deleteFailedLogin(request); err == nil {
		t.Fatal("vault failure permitted deletion")
	}
	client := domain.NewID()
	_, err := f.service.Store.Mutate(failedLoginContext(), domain.NewID(), "fixture.cleanup.actor", nil, func(tx *store.Tx) (any, error) {
		return tx.Put(domain.DeviceKind, client, 0, "", "", domain.Device{Name: "Cleanup client", Type: domain.ClientDevice, PairedAt: time.Now().UTC()})
	})
	if err != nil {
		t.Fatal(err)
	}
	f.secrets.deleteError = nil
	other := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.ClientDevice, DeviceID: client})
	if _, err := f.service.DeleteConfiguration(other, connect.NewRequest(request)); err == nil {
		t.Fatal("different actor adopted original deletion")
	}
	_, err = f.service.Store.Mutate(failedLoginContext(), domain.ID(request.Mutation.RequestId), "fixture.different.command", nil, func(tx *store.Tx) (any, error) {
		t.Fatal("reserved public deletion ID reached another command")
		return struct{}{}, nil
	})
	if domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("original request ID was not reserved", err)
	}
	if _, _, err := f.service.Store.Replay(failedLoginContext(), domain.ID(request.Mutation.RequestId), "fixture.different.command", nil); domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("different command ignored pending reservation", err)
	}
	current := f.failedLoginDeletion()
	current.Mutation.RequestId = request.Mutation.RequestId
	if err := f.deleteFailedLogin(current); err == nil {
		t.Fatal("changed confirmed revision adopted original deletion")
	}
	if _, err := f.secrets.Get(context.Background(), ref); err != nil {
		t.Fatal("mismatched request repeated cleanup", err)
	}
	// A stale fresh request cannot gain the new cleanup checkpoint revision.
	stale := &pb.DeleteConfigurationRequest{Kind: request.Kind, Mutation: &pb.Mutation{Id: request.Mutation.Id, ExpectedRevision: request.Mutation.ExpectedRevision, RequestId: string(domain.NewID())}}
	if err := f.deleteFailedLogin(stale); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("stale confirmation admitted", err)
	}
	if err := f.deleteFailedLogin(f.failedLoginDeletion()); err != nil {
		t.Fatal(err)
	}
}

func (f *subscriptionFixture) deleteFailedLoginResponse(request *pb.DeleteConfigurationRequest) (*pb.DeleteConfigurationResponse, error) {
	f.t.Helper()
	client := delidevv1connect.NewConfigurationServiceClient(http.DefaultClient, f.http.URL)
	response, err := client.DeleteConfiguration(context.Background(), subscriptionRequest(f.service.Identity.Token, request))
	if err != nil {
		return nil, err
	}
	return response.Msg, nil
}

func TestSubscriptionAccountDeleteReportsNewCompletionThenReplay(t *testing.T) {
	f, _ := legacyFailedServerLogin(t, false)
	request := f.failedLoginDeletion()
	first, err := f.deleteFailedLoginResponse(request)
	if err != nil {
		t.Fatal(err)
	}
	if first.Replayed || first.Id != request.Mutation.Id || first.RequestId != request.Mutation.RequestId {
		t.Fatal("invocation that completed deletion reported a replay", first)
	}
	again, err := f.deleteFailedLoginResponse(request)
	if err != nil {
		t.Fatal(err)
	}
	if !again.Replayed || again.Id != request.Mutation.Id || again.RequestId != request.Mutation.RequestId {
		t.Fatal("committed receipt was not reported as replayed", again)
	}
}

func TestSubscriptionAccountDeleteReportsRacedReceiptAsReplayed(t *testing.T) {
	f, _ := legacyFailedServerLogin(t, false)
	request := f.failedLoginDeletion()
	var racedErr error
	var hookRuns int
	var raceRef credentials.Ref
	// The original cleanup completes after the retry's initial replay check
	// misses and before the retry reaches the failed-subscription helper.
	f.service.afterAccountDeleteReplay = func(context.Context) {
		hookRuns++
		f.service.afterAccountDeleteReplay = nil
		racedErr = f.deleteFailedLogin(request)
		// Recreate protected state for the account. A second native or vault
		// cleanup by the retry would remove it.
		ref := credentials.Ref{Owner: f.input.AccountID, ID: domain.NewID(), Purpose: credentials.AccountLogin}
		if _, err := f.secrets.Put(context.Background(), ref, []byte("synthetic-post-cleanup-token")); err != nil {
			racedErr = err
		}
		raceRef = ref
	}
	retry, err := f.deleteFailedLoginResponse(request)
	if err != nil {
		t.Fatal(err)
	}
	if hookRuns != 1 || racedErr != nil {
		t.Fatal("original cleanup did not complete inside the race window", hookRuns, racedErr)
	}
	if !retry.Replayed || retry.Id != request.Mutation.Id || retry.RequestId != request.Mutation.RequestId {
		t.Fatal("raced retry did not report the original receipt as replayed", retry)
	}
	if _, err := f.secrets.Get(context.Background(), raceRef); err != nil {
		t.Fatal("raced retry repeated cleanup", err)
	}
}

func TestSubscriptionAccountDeleteKeepsFailedCleanupFences(t *testing.T) {
	f, _ := legacyFailedServerLogin(t, false)
	ref := credentials.Ref{Owner: f.input.AccountID, ID: domain.NewID(), Purpose: credentials.AccountLogin}
	if _, err := f.secrets.Put(context.Background(), ref, []byte("synthetic-staged-token")); err != nil {
		t.Fatal(err)
	}
	f.secrets.deleteError = domain.Fail(domain.Unavailable, "Fixture vault unavailable.", "Retry explicit cleanup.")
	request := f.failedLoginDeletion()
	if response, err := f.deleteFailedLoginResponse(request); err == nil {
		t.Fatal("failed cleanup reported success", response)
	}
	f.secrets.deleteError = nil
	// A retained failed attempt stays an error for its own request, never a replay.
	if response, err := f.deleteFailedLoginResponse(request); err == nil {
		t.Fatal("terminal failed cleanup reported success on retry", response)
	}
	stale := &pb.DeleteConfigurationRequest{Kind: request.Kind, Mutation: &pb.Mutation{Id: request.Mutation.Id, ExpectedRevision: request.Mutation.ExpectedRevision, RequestId: string(domain.NewID())}}
	if response, err := f.deleteFailedLoginResponse(stale); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("stale confirmation admitted", response, err)
	}
	fresh, err := f.deleteFailedLoginResponse(f.failedLoginDeletion())
	if err != nil || fresh.Replayed {
		t.Fatal("fresh confirmation did not newly complete deletion", fresh, err)
	}
}

type failedLoginDeleteResult struct {
	response *pb.DeleteConfigurationResponse
	err      error
}

// pausedDeletionHooks makes each arrival at a hook announce its order and wait
// for an explicit release, so tests choose which invocation proceeds first.
type pausedDeletionHooks struct {
	arrived chan int
	release [3]chan struct{}
	count   atomic.Int32
}

func newPausedDeletionHooks() *pausedDeletionHooks {
	h := &pausedDeletionHooks{arrived: make(chan int, 2)}
	for i := range h.release {
		h.release[i] = make(chan struct{})
	}
	return h
}

func (h *pausedDeletionHooks) pause() {
	n := int(h.count.Add(1))
	h.arrived <- n
	<-h.release[n]
}

func (h *pausedDeletionHooks) waitArrival(t *testing.T, want int) {
	t.Helper()
	select {
	case got := <-h.arrived:
		if got != want {
			t.Fatal("unexpected hook arrival order", got, want)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("invocation never reached the hook", want)
	}
}

func (f *subscriptionFixture) startFailedLoginDeletion(request *pb.DeleteConfigurationRequest) <-chan failedLoginDeleteResult {
	done := make(chan failedLoginDeleteResult, 1)
	go func() {
		response, err := f.deleteFailedLoginResponse(request)
		done <- failedLoginDeleteResult{response, err}
	}()
	return done
}

func awaitFailedLoginDeletion(t *testing.T, done <-chan failedLoginDeleteResult) *pb.DeleteConfigurationResponse {
	t.Helper()
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatal(r.err)
		}
		return r.response
	case <-time.After(10 * time.Second):
		t.Fatal("deletion invocation did not finish")
	}
	return nil
}

func (f *subscriptionFixture) recreateProtectedCredential() credentials.Ref {
	f.t.Helper()
	// Recreate protected state after the deletion committed. A second native or
	// vault cleanup would remove it.
	ref := credentials.Ref{Owner: f.input.AccountID, ID: domain.NewID(), Purpose: credentials.AccountLogin}
	if _, err := f.secrets.Put(context.Background(), ref, []byte("synthetic-post-cleanup-token")); err != nil {
		f.t.Fatal(err)
	}
	return ref
}

func (f *subscriptionFixture) requireDeletedOnce(request *pb.DeleteConfigurationRequest, completed, observed *pb.DeleteConfigurationResponse, ref credentials.Ref) {
	f.t.Helper()
	if completed.Replayed || !observed.Replayed {
		f.t.Fatal("exactly the completing invocation must report Replayed=false", completed, observed)
	}
	for _, r := range []*pb.DeleteConfigurationResponse{completed, observed} {
		if r.Id != request.Mutation.Id || r.RequestId != request.Mutation.RequestId {
			f.t.Fatal("response identity changed", r)
		}
	}
	if _, err := f.secrets.Get(context.Background(), ref); err != nil {
		f.t.Fatal("observing invocation repeated native or vault cleanup", err)
	}
	if _, err := f.service.Store.Get(failedLoginContext(), domain.AccountKind, f.input.AccountID); domain.SafeError(err).Code != domain.NotFound {
		f.t.Fatal("account remains", err)
	}
	if _, replayed, err := f.service.Store.Replay(failedLoginContext(), domain.ID(request.Mutation.RequestId), "configuration.delete", struct {
		ID       string      `json:"id"`
		Revision uint64      `json:"revision"`
		Kind     domain.Kind `json:"kind"`
	}{request.Mutation.Id, request.Mutation.ExpectedRevision, domain.AccountKind}); err != nil || !replayed {
		f.t.Fatal("single deletion receipt missing", replayed, err)
	}
}

// Both invocations observe the same pending child after the account gate is
// released. The later invocation completes first, so the earlier admitting one
// must report a replay.
func TestSubscriptionAccountDeleteReportsOnlyCompletingInvocationAfterSharedPendingObservation(t *testing.T) {
	f, _ := legacyFailedServerLogin(t, false)
	request := f.failedLoginDeletion()
	hooks := newPausedDeletionHooks()
	f.service.afterFailedSubscriptionDeleteObserve = func(context.Context) { hooks.pause() }
	first := f.startFailedLoginDeletion(request)
	hooks.waitArrival(t, 1)
	second := f.startFailedLoginDeletion(request)
	hooks.waitArrival(t, 2)
	close(hooks.release[2])
	completed := awaitFailedLoginDeletion(t, second)
	ref := f.recreateProtectedCredential()
	close(hooks.release[1])
	observed := awaitFailedLoginDeletion(t, first)
	f.requireDeletedOnce(request, completed, observed, ref)
}

// The second invocation already waits at the per-account runner gate when the
// first completes; its locked terminal check must report a replay.
func TestSubscriptionAccountDeleteReportsLockedTerminalCheckAsReplayed(t *testing.T) {
	f, _ := legacyFailedServerLogin(t, false)
	request := f.failedLoginDeletion()
	hooks := newPausedDeletionHooks()
	f.service.beforeFailedCleanupAccountGate = func(context.Context, domain.ID) { hooks.pause() }
	first := f.startFailedLoginDeletion(request)
	hooks.waitArrival(t, 1)
	second := f.startFailedLoginDeletion(request)
	hooks.waitArrival(t, 2)
	close(hooks.release[1])
	completed := awaitFailedLoginDeletion(t, first)
	ref := f.recreateProtectedCredential()
	close(hooks.release[2])
	observed := awaitFailedLoginDeletion(t, second)
	f.requireDeletedOnce(request, completed, observed, ref)
}

// The background controller can finish the admitted child before the admitting
// request runs its own cleanup. No foreground invocation completed the
// deletion, so the admitting request reports a replay without repeating effects.
func TestSubscriptionAccountDeleteReportsBackgroundCompletionAsReplayed(t *testing.T) {
	f, _ := legacyFailedServerLogin(t, false)
	request := f.failedLoginDeletion()
	var ref credentials.Ref
	var backgroundErr error
	f.service.afterFailedSubscriptionDeleteObserve = func(ctx context.Context) {
		f.service.afterFailedSubscriptionDeleteObserve = nil
		parent, err := f.service.Store.Get(failedLoginContext(), domain.JobKind, domain.ID(request.Mutation.RequestId))
		if err != nil {
			backgroundErr = err
			return
		}
		if completion, err := f.service.runFailedSubscriptionCleanup(failedLoginContext(), parent); err != nil || completion != failedSubscriptionCleanupCompletedHere {
			backgroundErr = domain.Fail(domain.Internal, "Background cleanup did not complete the child.", "")
			return
		}
		ref = f.recreateProtectedCredential()
	}
	response, err := f.deleteFailedLoginResponse(request)
	if err != nil || backgroundErr != nil {
		t.Fatal(err, backgroundErr)
	}
	if !response.Replayed || response.Id != request.Mutation.Id || response.RequestId != request.Mutation.RequestId {
		t.Fatal("background completion was reported as a new completion", response)
	}
	if _, err := f.secrets.Get(context.Background(), ref); err != nil {
		t.Fatal("admitting request repeated cleanup", err)
	}
}
