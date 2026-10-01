// SPDX-License-Identifier: Apache-2.0
package terminal

import (
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestResultRejectsUntrustedFailureClassifications(t *testing.T) {
	for _, code := range []domain.Code{domain.InvalidArgument, domain.NotFound, domain.Conflict, domain.PermissionDenied, domain.Unavailable, domain.MissingInput, domain.Unsupported, domain.RecoveryRequired, domain.ResourceExhausted, domain.Canceled, domain.Internal} {
		result := Result{State: domain.TerminalUncertain, Rows: 24, Columns: 80, Problem: domain.Fail(code, "untrusted native diagnostic", "untrusted guidance")}
		if err := result.Validate(); err != nil {
			t.Fatal("supported native failure was rejected", code, err)
		}
	}
	for _, code := range []domain.Code{"", "native_secret_code", domain.ProviderDisabled, domain.BudgetReached, domain.Unauthenticated} {
		result := Result{State: domain.TerminalExited, CleanupVerified: true, Rows: 24, Columns: 80, Problem: domain.Fail(code, "untrusted native diagnostic", "")}
		if result.Validate() == nil {
			t.Fatal("unknown or non-terminal failure was accepted", code)
		}
	}
	if (Result{State: domain.TerminalRunning, Shell: "/bin/sh", Cwd: "/fixture", Rows: 24, Columns: 80, Problem: domain.Fail(domain.Unavailable, "contradictory failure", "")}).Validate() == nil {
		t.Fatal("running terminal carried a failure")
	}
}
