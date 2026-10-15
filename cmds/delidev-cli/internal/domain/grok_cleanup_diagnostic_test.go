// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// The complete result table of one native operation joined with its owned
// process cleanup: every operation outcome against a confirmed and an
// unconfirmed cleanup. The first failure keeps its diagnostic; an unconfirmed
// cleanup only forces recovery_required on the transport error.
func TestGrokCleanupFailureKeepsTheFirstFailure(t *testing.T) {
	cleanup := Fail(RecoveryRequired, "Grok Build probe cleanup could not be confirmed.", "Retain the private runtime and reconcile its owned process before retrying.")
	for _, test := range []struct {
		name          string
		phase         GrokPhase
		operation     error
		cleanup       *Error
		wantNil       bool
		wantPhase     GrokPhase
		wantCode      Code
		wantTransport Code
		wantTimeout   bool
		wantDeadline  bool
	}{
		{name: "success and confirmed cleanup", wantNil: true},
		{name: "success and unconfirmed cleanup", cleanup: cleanup, wantPhase: GrokCleanup, wantCode: RecoveryRequired, wantTransport: RecoveryRequired},
		{name: "profile failure and confirmed cleanup", phase: GrokProfile, operation: Fail(Unsupported, "x", "y"), wantPhase: GrokProfile, wantCode: Unsupported, wantTransport: Unsupported},
		{name: "profile failure and unconfirmed cleanup", phase: GrokProfile, operation: Fail(Unsupported, "x", "y"), cleanup: cleanup, wantPhase: GrokProfile, wantCode: Unsupported, wantTransport: RecoveryRequired},
		{name: "runtime failure and confirmed cleanup", phase: GrokRuntime, operation: Fail(Unavailable, "x", "y"), wantPhase: GrokRuntime, wantCode: Unavailable, wantTransport: Unavailable},
		{name: "runtime failure and unconfirmed cleanup", phase: GrokRuntime, operation: Fail(Unavailable, "x", "y"), cleanup: cleanup, wantPhase: GrokRuntime, wantCode: Unavailable, wantTransport: RecoveryRequired},
		{name: "version failure and unconfirmed cleanup", phase: GrokVersion, operation: Fail(Unsupported, "x", "y"), cleanup: cleanup, wantPhase: GrokVersion, wantCode: Unsupported, wantTransport: RecoveryRequired},
		{name: "timeout and confirmed cleanup", phase: GrokRuntime, operation: context.DeadlineExceeded, wantPhase: GrokRuntime, wantCode: Unavailable, wantTransport: Unavailable, wantTimeout: true, wantDeadline: true},
		{name: "timeout and unconfirmed cleanup", phase: GrokRuntime, operation: context.DeadlineExceeded, cleanup: cleanup, wantPhase: GrokRuntime, wantCode: Unavailable, wantTransport: RecoveryRequired, wantTimeout: true, wantDeadline: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := WithGrokCleanupFailure("1.0.52", test.phase, test.operation, test.cleanup)
			if test.wantNil {
				if err != nil {
					t.Fatalf("a clean probe returned %v", err)
				}
				return
			}
			d := GrokErrorDiagnostic(err)
			if d == nil || d.Validate() != nil || d.Phase != test.wantPhase || d.Code != test.wantCode || d.DetectedVersion != "1.0.52" || d.SupportedVersion != GrokSubscriptionBaselineVersion {
				t.Fatalf("diagnostic = %#v", d)
			}
			if got := SafeError(err).Code; got != test.wantTransport {
				t.Fatalf("transport code = %q, want %q", got, test.wantTransport)
			}
			if timeout := strings.HasSuffix(d.Message, "The native operation timed out."); timeout != test.wantTimeout {
				t.Fatalf("timeout reason = %v, want %v: %q", timeout, test.wantTimeout, d.Message)
			}
			if got := errors.Is(err, context.DeadlineExceeded); got != test.wantDeadline {
				t.Fatalf("deadline identity = %v, want %v", got, test.wantDeadline)
			}
			// The earliest failure is never wrapped twice: re-wrapping is a no-op.
			if again := GrokErrorDiagnostic(WithGrokDiagnostic("1.0.99", GrokCleanup, err)); *again != *d {
				t.Fatalf("a wrapped failure was replaced: %#v", again)
			}
		})
	}
}
