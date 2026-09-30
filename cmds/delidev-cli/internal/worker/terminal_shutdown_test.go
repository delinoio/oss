// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/terminal"
)

func TestTerminalShutdownLossDoesNotSubstituteForOriginalProcessEvidence(t *testing.T) {
	m := newTerminalManager(context.Background(), Config{Root: t.TempDir()}, nil, Credential{}, domain.NewID())
	a := terminal.Assignment{ID: domain.NewID(), Terminal: domain.Terminal{OwnerInstanceID: m.instance, Rows: 24, Columns: 80}, Operation: domain.TerminalOperation{ID: domain.NewID(), Action: domain.TerminalClose}}
	result := terminal.Result{State: domain.TerminalUncertain, OutputLost: true, Rows: 24, Columns: 80, Problem: domain.TerminalUnavailable()}
	if err := m.saveShutdown(a.ID, result); err != nil {
		t.Fatal(err)
	}
	got := m.execute(a)
	if got.State != domain.TerminalUncertain || got.CleanupVerified || !got.OutputLost {
		t.Fatal("shutdown loss manufactured cleanup or lost its gap", got)
	}
	a.Terminal.OwnerInstanceID = domain.NewID()
	got = m.execute(a)
	if got.State != domain.TerminalUncertain || got.CleanupVerified || got.OutputLost {
		t.Fatal("foreign shutdown record granted evidence", got)
	}
}
