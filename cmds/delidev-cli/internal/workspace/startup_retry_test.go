// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
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
			if next, err := m.ClaimUnsentRetry(context.Background(), domain.NewID(), domain.NewID(), original, input, manifest); err != nil {
				t.Fatal("active predecessor blocked fresh retry", err)
			} else if err := next.Close(); err != nil {
				t.Fatal(err)
			}
			if err := lease.Close(); err != nil {
				t.Fatal(err)
			}
			foreign := ExecutionPredecessor{JobID: domain.NewID(), ExecutionID: domain.NewID()}
			if next, err := m.ClaimUnsentRetry(context.Background(), domain.NewID(), domain.NewID(), foreign, input, manifest); err != nil {
				t.Fatal("foreign cleanup metadata blocked retry", err)
			} else if err := next.Close(); err != nil {
				t.Fatal(err)
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
	if next, err := m.ClaimUnsentRetry(context.Background(), domain.NewID(), domain.NewID(), failed, input, manifest, foreign); err != nil {
		t.Fatal("foreign retained metadata blocked retry", err)
	} else if err := next.Close(); err != nil {
		t.Fatal(err)
	}
	lease, err = m.ClaimUnsentRetry(context.Background(), domain.NewID(), domain.NewID(), failed, input, manifest, original)
	if err != nil {
		t.Fatal(err)
	}
	if lease.claim.PreviousJobID != failed.JobID || lease.claim.PreviousExecutionID != failed.ExecutionID {
		t.Fatal("changed selected retry metadata")
	}
	var retained executionClaim
	raw, err := security.ReadPrivate(m.executionHistoryPath(input.SessionID, original.ExecutionID), 4096)
	if err != nil || domain.Decode(raw, &retained) != nil || retained.JobID != original.JobID {
		t.Fatal("rewrote historical native predecessor", err)
	}

	if err = lease.Close(); err != nil {
		t.Fatal(err)
	}
}
