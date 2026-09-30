// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/terminal"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func TestTerminalConfirmedCleanupRetiresOwnershipAfterAcknowledgement(t *testing.T) {
	for _, interrupted := range []bool{false, true} {
		t.Run(map[bool]string{false: "acknowledgement", true: "interrupted-retirement"}[interrupted], func(t *testing.T) {
			ctx := context.Background()
			fixture := &terminalJournalReportFixture{}
			_, handler := delidevv1connect.NewWorkerServiceHandler(fixture)
			server := httptest.NewServer(handler)
			defer server.Close()
			m := newTerminalManager(ctx, Config{Root: t.TempDir(), Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}, delidevv1connect.NewWorkerServiceClient(http.DefaultClient, server.URL), Credential{MachineID: domain.NewID()}, domain.NewID())
			a := terminal.Assignment{ID: domain.NewID(), SessionID: domain.NewID(), Terminal: domain.Terminal{MachineID: m.credential.MachineID}, Operation: domain.TerminalOperation{ID: domain.NewID(), Action: domain.TerminalClose}}
			owner := filepath.Join(m.processRoot(), string(a.ID))
			for _, path := range []string{filepath.Join(m.config.Root, "terminal-operations"), m.processRoot(), owner} {
				if err := security.PrivateDir(path); err != nil {
					t.Fatal(err)
				}
			}
			result := terminal.Result{State: domain.TerminalClosed, CleanupVerified: true, OutputLost: true, Rows: 24, Columns: 80}
			j := terminalOperationJournal{TerminalID: a.ID, OperationID: a.Operation.ID, InstanceID: m.instance, Digest: terminalOperationDigest(a), ClaimID: domain.NewID(), ReportID: domain.NewID(), Phase: terminalFinished, Result: &result}
			if err := m.saveJournal(j); err != nil {
				t.Fatal(err)
			}
			shutdown := result
			shutdown.State = domain.TerminalExited
			if err := m.saveShutdown(a.ID, shutdown); err != nil {
				t.Fatal(err)
			}
			lock, err := security.TryLock(owner + ".recovery.lock")
			if err != nil {
				t.Fatal(err)
			}
			if err := m.apply(ctx, a); err == nil {
				t.Fatal("expected report response loss")
			}
			for _, path := range []string{owner, owner + ".recovery.lock", m.shutdownPath(a.ID), m.journalPath(j.OperationID)} {
				if _, err := os.Stat(path); err != nil {
					t.Fatal("unconfirmed report discarded original ownership", err)
				}
			}
			if !interrupted {
				if err := lock.Close(); err != nil {
					t.Fatal(err)
				}
			}
			err = m.apply(ctx, a)
			if interrupted {
				if err == nil || domain.SafeError(err).Code != domain.Conflict {
					t.Fatal("held maintenance lock did not preserve pending retirement", err)
				}
				retained, err := m.loadJournal(a)
				if err != nil || retained.Phase != terminalReported {
					t.Fatal("confirmed acknowledgement was not synchronized", err)
				}
				if err := lock.Close(); err != nil {
					t.Fatal(err)
				}
				// The server record may already be purged. A different instance
				// finishes only local retirement, without any RPC or native replay.
				replacement := newTerminalManager(ctx, m.config, nil, m.credential, domain.NewID())
				replacement.observeExits(ctx)
			} else if err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{owner, owner + ".recovery.lock", m.shutdownPath(a.ID), m.journalPath(j.OperationID)} {
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Fatal("confirmed cleanup retained terminal ownership metadata", path, err)
				}
			}
			if len(fixture.requests) != 2 {
				t.Fatal("local retirement repeated a confirmed report")
			}
		})
	}
}

func TestTerminalUncertainReportPreservesProcessOwnership(t *testing.T) {
	m := newTerminalManager(context.Background(), Config{Root: t.TempDir()}, nil, Credential{}, domain.NewID())
	id, operation := domain.NewID(), domain.NewID()
	owner := filepath.Join(m.processRoot(), string(id))
	if err := security.PrivateDir(owner); err != nil {
		t.Fatal(err)
	}
	if err := security.PrivateDir(filepath.Join(m.config.Root, "terminal-operations")); err != nil {
		t.Fatal(err)
	}
	result := terminal.Result{State: domain.TerminalUncertain, Rows: 24, Columns: 80, OutputLost: true, Problem: domain.TerminalUnavailable()}
	j := terminalOperationJournal{TerminalID: id, OperationID: operation, Phase: terminalReported, Result: &result}
	if err := m.saveJournal(j); err != nil {
		t.Fatal(err)
	}
	if err := m.saveShutdown(id, result); err != nil {
		t.Fatal(err)
	}
	if err := m.retireReported(j); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{owner, m.shutdownPath(id)} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal("uncertain report retired original proof", err)
		}
	}
}
