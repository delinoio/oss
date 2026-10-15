// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

const grokTimeoutSuffix = "The native operation timed out."

// Error identity through normalization: which inputs keep or lose the timeout
// marker, and what a Grok diagnostic reports for each. The table covers raw
// context errors, wrapped and joined errors, SafeError (which loses identity)
// and SafeTimeoutError (which keeps it), plus failures that merely look similar.
func TestSafeTimeoutErrorPreservesTimeoutIdentity(t *testing.T) {
	deadline := fmt.Errorf("probe: %w", context.DeadlineExceeded)
	for _, test := range []struct {
		name        string
		normalized  *Error
		wantCode    Code
		wantCause   string
		wantTimeout bool
	}{
		{"deadline", SafeTimeoutError(context.DeadlineExceeded), Unavailable, TimeoutCause, true},
		{"wrapped deadline", SafeTimeoutError(deadline), Unavailable, TimeoutCause, true},
		{"joined deadline", SafeTimeoutError(errors.Join(errors.New("raw"), context.DeadlineExceeded)), Unavailable, TimeoutCause, true},
		{"already normalized timeout", SafeTimeoutError(SafeTimeoutError(context.DeadlineExceeded)), Unavailable, TimeoutCause, true},
		{"plain SafeError loses identity", SafeError(context.DeadlineExceeded), Unavailable, "", false},
		{"cancellation", SafeTimeoutError(context.Canceled), Canceled, "", false},
		{"unavailable without a deadline", SafeTimeoutError(Fail(Unavailable, "x", "y")), Unavailable, "", false},
		{"unavailable with its own cause", SafeTimeoutError(&Error{Code: Unavailable, Message: "x", Cause: "clone_timeout"}), Unavailable, "clone_timeout", false},
		{"unknown error", SafeTimeoutError(errors.New("raw")), Internal, "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			e := test.normalized
			if e.Code != test.wantCode || e.Cause != test.wantCause {
				t.Fatalf("normalized to code=%q cause=%q, want code=%q cause=%q", e.Code, e.Cause, test.wantCode, test.wantCause)
			}
			if got := errors.Is(e, context.DeadlineExceeded); got != test.wantTimeout {
				t.Fatalf("errors.Is(DeadlineExceeded) = %v, want %v", got, test.wantTimeout)
			}
			d := GrokErrorDiagnostic(WithGrokDiagnostic("1.0.52", GrokRuntime, e))
			if d == nil || d.Validate() != nil {
				t.Fatalf("diagnostic = %#v", d)
			}
			if got := strings.HasSuffix(d.Message, grokTimeoutSuffix); got != (test.wantTimeout && test.wantCode == Unavailable) {
				t.Fatalf("timeout reason = %v for %q", got, d.Message)
			}
			// Wrapping the normalized error again keeps the same diagnostic.
			again := GrokErrorDiagnostic(WithGrokDiagnostic("1.0.52", GrokRuntime, fmt.Errorf("outer: %w", e)))
			if again == nil || *again != *d {
				t.Fatalf("wrapping changed the diagnostic: %#v vs %#v", again, d)
			}
		})
	}
}

// The timeout marker is presentation metadata of the safe error only; the
// diagnostic's transport error does not repeat it, and the reason is stable for
// every phase.
func TestGrokTimeoutReasonIsPresentInEveryPhase(t *testing.T) {
	for _, phase := range GrokPhases {
		d := GrokErrorDiagnostic(WithGrokDiagnostic("1.0.52", phase, SafeTimeoutError(context.DeadlineExceeded)))
		if d == nil || d.Code != Unavailable || d.Validate() != nil || !strings.HasSuffix(d.Message, grokTimeoutSuffix) && phase != GrokVersion {
			t.Errorf("phase %q: %#v", phase, d)
		}
	}
	failure := WithGrokDiagnostic("1.0.52", GrokRuntime, SafeTimeoutError(context.DeadlineExceeded))
	if got := SafeError(failure); got.Cause != "" || got.Code != Unavailable {
		t.Fatalf("transport error repeats the marker: %#v", got)
	}
}
