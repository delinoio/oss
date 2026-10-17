// SPDX-License-Identifier: Apache-2.0
package server

import (
	"errors"
	"testing"
)

// flakyUsageLease fails its first closes and then confirms the release.
type flakyUsageLease struct{ calls, failures int }

func (l *flakyUsageLease) Close() error {
	l.calls++
	if l.calls <= l.failures {
		return errors.New("lock release failed")
	}
	return nil
}

// A failed profile lease release must stay reportable and retryable: a later
// call may neither mask the failure with a fresh nil nor skip the retry.
func TestClaudeUsageRuntimeReleaseRetriesAFailedLeaseClose(t *testing.T) {
	lease := &flakyUsageLease{failures: 2}
	runtime := &claudeUsageRuntime{lease: lease}
	for attempt := 1; attempt <= 2; attempt++ {
		if err := runtime.Release(); err == nil {
			t.Fatalf("release attempt %d reported success for a lock that was not released", attempt)
		}
	}
	if err := runtime.Release(); err != nil {
		t.Fatalf("the retried release did not confirm: %v", err)
	}
	if lease.calls != 3 {
		t.Fatalf("the lease was closed %d times instead of retried until confirmed", lease.calls)
	}
}

// The cleanup removal reports success only after the profile lease release was
// confirmed, and an earlier removal failure stays the reported cause.
func TestCloseUsageProfileLeasePropagatesAnUnconfirmedRelease(t *testing.T) {
	if err := closeUsageProfileLease(&flakyUsageLease{}, nil, nil); err != nil {
		t.Fatalf("a confirmed release failed the removal: %v", err)
	}
	if err := closeUsageProfileLease(&flakyUsageLease{failures: 1}, nil, nil); err == nil {
		t.Fatal("a failed release was reported as a completed removal")
	}
	cause := errors.New("storage removal failed")
	if err := closeUsageProfileLease(&flakyUsageLease{failures: 1}, cause, nil); !errors.Is(err, cause) {
		t.Fatalf("an earlier failure was replaced by the release failure: %v", err)
	}
	if err := closeUsageProfileLease(&flakyUsageLease{}, cause, nil); !errors.Is(err, cause) {
		t.Fatalf("an earlier failure was dropped: %v", err)
	}
}
