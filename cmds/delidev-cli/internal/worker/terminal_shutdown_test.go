// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
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

func TestTerminalMissingShutdownConservativelyPreservesOutputLoss(t *testing.T) {
	for _, test := range []struct {
		name    string
		phase   terminalOperationPhase
		claimed bool
		pending bool
		lost    bool
	}{
		{name: "running", lost: true},
		{name: "unclaimed-create", pending: true},
		{name: "prepared-create", pending: true, claimed: true, phase: terminalPrepared},
		{name: "claimed-create", pending: true, claimed: true, phase: terminalClaimed},
		{name: "started-create", pending: true, claimed: true, phase: terminalStarted, lost: true},
		{name: "joined-create", pending: true, claimed: true, phase: terminalFinished},
		{name: "joined-create-with-loss", pending: true, claimed: true, phase: terminalFinished, lost: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := newTerminalManager(context.Background(), Config{Root: t.TempDir()}, nil, Credential{}, domain.NewID())
			a := terminal.Assignment{ID: domain.NewID(), Terminal: domain.Terminal{OwnerInstanceID: domain.NewID(), State: domain.TerminalRunning, Rows: 24, Columns: 80}, Operation: domain.TerminalOperation{ID: domain.NewID(), Action: domain.TerminalClose}}
			// Retained empty ownership permits cleanup, independently of missing
			// shutdown metadata. Neither can establish complete output delivery.
			// PrivateDir owns only its final component. Explicitly create both
			// levels so Windows never inherits a shared DACL for the process root.
			for _, path := range []string{m.processRoot(), filepath.Join(m.processRoot(), string(a.ID))} {
				if err := security.PrivateDir(path); err != nil {
					t.Fatal(err)
				}
			}
			if test.pending {
				a.Terminal.Pending = &domain.TerminalOperation{ID: domain.NewID(), Action: domain.TerminalCreate, Claimed: test.claimed}
				if test.phase != "" {
					if err := security.PrivateDir(filepath.Join(m.config.Root, "terminal-operations")); err != nil {
						t.Fatal(err)
					}
					j := terminalOperationJournal{TerminalID: a.ID, OperationID: a.Terminal.Pending.ID, InstanceID: a.Terminal.OwnerInstanceID, Phase: test.phase}
					if test.phase == terminalFinished {
						j.Result = &terminal.Result{State: domain.TerminalExited, CleanupVerified: true, OutputLost: test.lost, Rows: 24, Columns: 80}
					}
					if err := m.saveJournal(j); err != nil {
						t.Fatal(err)
					}
				}
			}
			got := m.execute(a)
			if got.State != domain.TerminalClosed || !got.CleanupVerified || got.OutputLost != test.lost {
				t.Fatal("missing shutdown changed independent cleanup or loss proof", got)
			}
		})
	}
	m := newTerminalManager(context.Background(), Config{Root: t.TempDir()}, nil, Credential{}, domain.NewID())
	a := terminal.Assignment{ID: domain.NewID(), Terminal: domain.Terminal{OwnerInstanceID: domain.NewID(), Rows: 24, Columns: 80}, Operation: domain.TerminalOperation{ID: domain.NewID(), Action: domain.TerminalClose}}
	got := m.execute(a)
	if got.State != domain.TerminalUncertain || got.CleanupVerified || !got.OutputLost {
		t.Fatal("missing process and shutdown evidence did not preserve uncertainty and loss", got)
	}
}
