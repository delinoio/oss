package opencode

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestStoppedRuntimeRecoveryUsesOriginalOwnerAndSeparateSingleUseClaim(t *testing.T) {
	for _, mode := range []string{"success", "lost-claim", "canceled", "owner-uncertain", "answer-uncertain"} {
		t.Run(mode, func(t *testing.T) {
			f := newStopFixture(t, PermissionInteraction)
			r := f.r
			stopID := domain.NewID()
			if _, err := r.api.claimOwnedStop(context.Background(), r.f.o, stopID); err != nil {
				t.Fatal(err)
			}
			f.closeError = true
			if receipt, err := r.api.closeStoppedRuntime(context.Background(), r.f.o); err == nil || receipt.CleanupVerified {
				t.Fatal("fixture cleanup uncertainty was lost")
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "lost-claim":
				r.claimError = true
			case "canceled":
				r.beforeClaimReturn = cancel
			case "owner-uncertain":
				f.recoveryError = true
			case "answer-uncertain":
				r.f.o.interactions[r.id].attempt = &interactionAttempt{sent: true, receipt: InteractionReceipt{RequestID: domain.NewID()}}
			}
			request := domain.NewID()
			receipt, err := r.api.recoverStoppedRuntime(ctx, r.f.o, request)
			verified := mode == "success" || mode == "answer-uncertain"
			if (err == nil) != (mode == "success") || receipt.CleanupVerified != verified || receipt.RepliesUncertain != (mode == "answer-uncertain") || receipt.NativeAttempted || receipt.HTTPAccepted || r.posts != 0 {
				t.Fatalf("recovery changed original native facts: %+v %v", receipt, err)
			}
			if len(r.claims) != 2 || r.claims[1].RequestID != request || r.claims[1].StopRequestID != stopID || r.claims[1].Kind != RecoverStoppedRuntimeMutation || r.claims[1].InputRequestID != r.f.o.input.receipt.RequestID {
				t.Fatal("cleanup recovery did not synchronize its original Stop/input scope")
			}
			priorCalls := f.recoveryCalls
			_, _ = r.api.recoverStoppedRuntime(context.Background(), r.f.o, request)
			if f.recoveryCalls != priorCalls || len(r.claims) != 2 || f.closeCalls != 1 {
				t.Fatal("recovery automatically repeated an uncertain operation")
			}
			if mode == "success" || mode == "answer-uncertain" {
				return
			}
			r.claimError, r.beforeClaimReturn, f.recoveryError = false, nil, false
			receipt, err = r.api.recoverStoppedRuntime(context.Background(), r.f.o, domain.NewID())
			if err != nil || !receipt.CleanupVerified || f.recoveryCalls != priorCalls+1 || f.closeCalls != 1 || r.posts != 0 {
				t.Fatal("explicit new original-owner recovery did not retain retryable cleanup")
			}
		})
	}
}

func TestStoppedRuntimeRecoveryRejectsUnownedScopeAndBoundsClaims(t *testing.T) {
	for _, mode := range []string{"not-stopped", "not-attempted", "foreign-observer", "missing-reconciler", "reused-stop-id", "bound"} {
		t.Run(mode, func(t *testing.T) {
			f := newStopFixture(t, QuestionInteraction)
			r := f.r
			observer, request := r.f.o, domain.NewID()
			if mode != "not-stopped" {
				stopID := domain.NewID()
				if _, err := r.api.claimOwnedStop(context.Background(), observer, stopID); err != nil {
					t.Fatal(err)
				}
				if mode == "reused-stop-id" {
					request = stopID
				}
				if mode != "not-attempted" {
					f.closeError = true
					_, _ = r.api.closeStoppedRuntime(context.Background(), observer)
				}
			}
			switch mode {
			case "foreign-observer":
				observer = newObserverFixture(t).o
			case "missing-reconciler":
				r.api.reconcileOwned = nil
			case "bound":
				observer.stop.recoveryAttempts = maxStopRecoveryAttempts
			}
			claims := len(r.claims)
			if _, err := r.api.recoverStoppedRuntime(context.Background(), observer, request); err == nil || f.recoveryCalls != 0 || len(r.claims) != claims || r.posts != 0 {
				t.Fatal("invalid cleanup recovery consumed owner authority")
			}
		})
	}
}
