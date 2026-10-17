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
