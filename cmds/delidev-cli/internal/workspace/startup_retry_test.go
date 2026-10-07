// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"testing"
)

func TestUnsentRetryKeepsOriginalWorkspaceClaim(t *testing.T) {
	for _, claimed := range []bool{false, true} {
		m, input, manifest := chatExecutionFixture(t)
		original := ExecutionPredecessor{JobID: domain.NewID(), ExecutionID: domain.NewID()}
		if claimed {
			lease, err := m.ClaimFirstExecution(context.Background(), original.JobID, original.ExecutionID, input, manifest)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := m.ClaimUnsentRetry(context.Background(), domain.NewID(), domain.NewID(), original, input, manifest); err == nil {
				t.Fatal("active predecessor was replaced")
			}
			if err := lease.Close(); err != nil {
				t.Fatal(err)
			}
			foreign := ExecutionPredecessor{JobID: domain.NewID(), ExecutionID: domain.NewID()}
			if _, err := m.ClaimUnsentRetry(context.Background(), domain.NewID(), domain.NewID(), foreign, input, manifest); err == nil {
				t.Fatal("foreign cleanup proof was adopted")
			}
		}
		lease, err := m.ClaimUnsentRetry(context.Background(), domain.NewID(), domain.NewID(), original, input, manifest)
		if err != nil {
			t.Fatal(err)
		}
		if err := lease.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestUnsentRetryBeforeContinuationClaimKeepsExactNativePredecessor(t *testing.T) {
	m, input, manifest := chatExecutionFixture(t)
	original := ExecutionPredecessor{JobID: domain.NewID(), ExecutionID: domain.NewID()}
	failed := ExecutionPredecessor{JobID: domain.NewID(), ExecutionID: domain.NewID()}
	lease, err := m.ClaimFirstExecution(context.Background(), original.JobID, original.ExecutionID, input, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err = lease.Close(); err != nil {
		t.Fatal(err)
	}
	foreign := ExecutionPredecessor{JobID: domain.NewID(), ExecutionID: domain.NewID()}
	if _, err = m.ClaimUnsentRetry(context.Background(), domain.NewID(), domain.NewID(), failed, input, manifest, foreign); err == nil {
		t.Fatal("adopted unrelated retained history")
	}
	lease, err = m.ClaimUnsentRetry(context.Background(), domain.NewID(), domain.NewID(), failed, input, manifest, original)
	if err != nil {
		t.Fatal(err)
	}
	if lease.claim.PreviousJobID != original.JobID || lease.claim.PreviousExecutionID != original.ExecutionID {
		t.Fatal("rewrote the original native predecessor")
	}
	if err = lease.Close(); err != nil {
		t.Fatal(err)
	}
}
