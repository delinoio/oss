package opencode

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestOwnedStopDoesNotRequireNativeReadinessOrInventNativeAcknowledgment(t *testing.T) {
	for _, mode := range []string{"before-assistant", "lost-stream", "observer-failed", "session-mismatch", "claim-failed"} {
		t.Run(mode, func(t *testing.T) {
			f := newStopFixture(t, PermissionInteraction)
			r := f.r
			switch mode {
			case "before-assistant":
				r.f.o.progress.UserSeen = false
				r.f.o.progress.InputPartSeen = false
				r.f.o.progress.AssistantID = ""
				r.f.o.progress.Status = NativeStatusUnknown
			case "lost-stream":
				r.api.events.Close()
			case "observer-failed":
				_ = r.f.o.interruption(context.Background())
			case "session-mismatch":
				r.api.problem = sessionProblem()
			case "claim-failed":
				r.claimError = true
			}
			receipt, err := r.api.claimOwnedStop(context.Background(), r.f.o, domain.NewID())
			if (err != nil) != (mode == "claim-failed") || receipt.RequestID.Validate() != nil || receipt.NativeAttempted || receipt.HTTPAccepted || receipt.CleanupVerified || r.posts != 0 || len(r.claims) != 1 || r.claims[0].Kind != StopOwnedRuntimeMutation {
				t.Fatalf("owned Stop invented native authority: %+v %v", receipt, err)
			}
			if _, _, err := r.f.o.prepareInteraction(domain.NewID(), r.id, r.response); err == nil {
				t.Fatal("cleanup intent still allowed an answer")
			}
			if _, err := r.api.claimOwnedStop(context.Background(), r.f.o, domain.NewID()); err == nil {
				t.Fatal("owned cleanup intent was reclaimed")
			}
			receipt, err = r.api.closeStoppedRuntime(context.Background(), r.f.o)
			if err != nil || !receipt.CleanupVerified || !receipt.PendingCleared || receipt.NativeAttempted || receipt.HTTPAccepted || receipt.TerminalObserved || receipt.InterruptedObserved || receipt.IdleVerified || r.posts != 0 || f.closeCalls != 1 {
				t.Fatalf("owned cleanup changed unobserved native facts: %+v %v", receipt, err)
			}
			if mode == "observer-failed" && !r.f.o.snapshot().NeedsRecovery || mode == "session-mismatch" && r.api.problem == nil {
				t.Fatal("process cleanup cleared original native uncertainty")
			}
		})
	}
}

func TestOwnedStopRefusesMissingOrChangedOriginalOwnership(t *testing.T) {
	for _, mode := range []string{"foreign-observer", "missing-closer", "missing-owner", "input-identity", "input-digest", "request-reuse"} {
		t.Run(mode, func(t *testing.T) {
			f := newStopFixture(t, PermissionInteraction)
			r := f.r
			observer, request := r.f.o, domain.NewID()
			switch mode {
			case "foreign-observer":
				observer = newObserverFixture(t).o
			case "missing-closer":
				r.api.closeOwned = nil
			case "missing-owner":
				r.api.owner = ""
			case "input-identity":
				copy := *r.api.input
				copy.receipt.RequestID = domain.NewID()
				r.api.input = &copy
			case "input-digest":
				copy := *r.api.input
				copy.digest[0]++
				r.api.input = &copy
			case "request-reuse":
				request = r.f.o.input.receipt.RequestID
			}
			if _, err := r.api.claimOwnedStop(context.Background(), observer, request); err == nil || len(r.claims) != 0 || r.posts != 0 || f.closeCalls != 0 {
				t.Fatal("unowned cleanup gained native or process authority")
			}
		})
	}
}

func TestNativeAndOwnedStopShareOneOriginalClaim(t *testing.T) {
	f := newStopFixture(t, PermissionInteraction)
	r := f.r
	r.claimError = true
	if _, err := r.api.stopInput(context.Background(), r.f.o, domain.NewID()); err == nil {
		t.Fatal("expected uncertain original claim")
	}
	if _, err := r.api.claimOwnedStop(context.Background(), r.f.o, domain.NewID()); err == nil || len(r.claims) != 1 || r.posts != 0 {
		t.Fatal("owned Stop replaced an uncertain native Stop claim")
	}
	if receipt, err := r.api.closeStoppedRuntime(context.Background(), r.f.o); err != nil || !receipt.CleanupVerified {
		t.Fatal("original uncertain claim lost independent containment authority")
	}
}
