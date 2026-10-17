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

// The renewal runtime shares the retry contract of the login runtime.
func TestClaudeUsageRenewalRuntimeReleaseRetriesAFailedLeaseClose(t *testing.T) {
	lease := &flakyUsageLease{failures: 1}
	runtime := &claudeUsageRenewalRuntime{lease: lease}
	if err := runtime.Release(); err == nil {
		t.Fatal("the first failed release was reported as success")
	}
	if err := runtime.Release(); err != nil {
		t.Fatalf("the retried release did not confirm: %v", err)
	}
	if lease.calls != 2 {
		t.Fatalf("the lease was closed %d times", lease.calls)
	}
}

func resetStrandedUsageLeases(t *testing.T) {
	t.Helper()
	clear := func() {
		strandedUsageLeases.mu.Lock()
		strandedUsageLeases.pending = nil
		strandedUsageLeases.mu.Unlock()
	}
	clear()
	t.Cleanup(clear)
}

// A lease whose release failed after its runtime was dropped (an opener error
// path) stays retained and is retried until confirmed, so the profile lock is
// never stranded until a restart.
func TestStrandedUsageLeaseIsRetriedUntilConfirmed(t *testing.T) {
	resetStrandedUsageLeases(t)
	lease := &flakyUsageLease{failures: 2}
	if err := strandedUsageLeases.close(lease); err == nil {
		t.Fatal("a failed release was reported as confirmed")
	}
	// Closing the same lease again does not retain it twice.
	if err := strandedUsageLeases.close(lease); err == nil {
		t.Fatal("the second failed release was reported as confirmed")
	}
	if len(strandedUsageLeases.pending) != 1 {
		t.Fatalf("the failed lease was retained %d times", len(strandedUsageLeases.pending))
	}
	if remaining := strandedUsageLeases.retry(nil); remaining != 0 {
		t.Fatalf("the retry did not confirm the release: %d retained", remaining)
	}
	if lease.calls != 3 {
		t.Fatalf("the lease was closed %d times instead of retried until confirmed", lease.calls)
	}
	if remaining := strandedUsageLeases.retry(nil); remaining != 0 || lease.calls != 3 {
		t.Fatal("a confirmed lease was closed again")
	}
}

func TestStrandedUsageLeaseStaysRetainedWhileTheReleaseKeepsFailing(t *testing.T) {
	resetStrandedUsageLeases(t)
	lease := &flakyUsageLease{failures: 2}
	_ = strandedUsageLeases.close(lease)
	if remaining := strandedUsageLeases.retry(nil); remaining != 1 {
		t.Fatalf("an unconfirmed lease was dropped: %d retained", remaining)
	}
	if remaining := strandedUsageLeases.retry(nil); remaining != 0 {
		t.Fatalf("the lease was not released once the close succeeded: %d retained", remaining)
	}
}

// A runtime release that fails retains its lease for the next acquisition even
// when the caller drops the runtime after logging the failure.
func TestRuntimeReleaseFailureRetainsTheLeaseForRetry(t *testing.T) {
	resetStrandedUsageLeases(t)
	lease := &flakyUsageLease{failures: 1}
	renewal := &claudeUsageRenewalRuntime{lease: lease}
	if err := renewal.Release(); err == nil {
		t.Fatal("a failed release was reported as confirmed")
	}
	if remaining := strandedUsageLeases.retry(nil); remaining != 0 || lease.calls != 2 {
		t.Fatalf("the dropped runtime's lease was not retried to confirmation: retained=%d calls=%d", remaining, lease.calls)
	}
}
