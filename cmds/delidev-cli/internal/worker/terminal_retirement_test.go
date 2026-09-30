// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"fmt"
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

func TestTerminalReplacementRetiresOnlyAcknowledgedOriginalReport(t *testing.T) {
	for _, natural := range []bool{false, true} {
		t.Run(map[bool]string{false: "claimed-close", true: "spontaneous-exit"}[natural], func(t *testing.T) {
			ctx := context.Background()
			fixture := &terminalJournalReportFixture{}
			_, handler := delidevv1connect.NewWorkerServiceHandler(fixture)
			server := httptest.NewServer(handler)
			defer server.Close()
			m := newTerminalManager(ctx, Config{Root: t.TempDir(), Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}, delidevv1connect.NewWorkerServiceClient(http.DefaultClient, server.URL), Credential{MachineID: domain.NewID()}, domain.NewID())
			id, operation := domain.NewID(), domain.NewID()
			owner := filepath.Join(m.processRoot(), string(id))
			for _, path := range []string{filepath.Join(m.config.Root, "terminal-operations"), m.processRoot(), owner} {
				if err := security.PrivateDir(path); err != nil {
					t.Fatal(err)
				}
			}
			result := terminal.Result{State: domain.TerminalClosed, CleanupVerified: true, OutputLost: true, Rows: 24, Columns: 80}
			j := terminalOperationJournal{TerminalID: id, OperationID: operation, InstanceID: m.instance, Digest: "original-close", ClaimID: domain.NewID(), ReportID: domain.NewID(), Phase: terminalFinished, Result: &result}
			if natural {
				j.Digest = ""
				j.ClaimID = ""
				j.ReportID = operation
				j.Result.State = domain.TerminalExited
			}
			if err := m.saveJournal(j); err != nil {
				t.Fatal(err)
			}
			if err := m.saveShutdown(id, terminal.Result{State: domain.TerminalExited, CleanupVerified: true, OutputLost: true, Rows: 24, Columns: 80}); err != nil {
				t.Fatal(err)
			}
			lock, err := security.TryLock(owner + ".recovery.lock")
			if err != nil {
				t.Fatal(err)
			}
			m.observeExits(ctx) // Server commits, but the acknowledgement is lost.
			for _, path := range []string{owner, owner + ".recovery.lock", m.shutdownPath(id), m.journalPath(operation)} {
				if _, err := os.Stat(path); err != nil {
					t.Fatal("unacknowledged result discarded ownership", err)
				}
			}
			replacement := newTerminalManager(ctx, m.config, m.client, m.credential, domain.NewID())
			replacement.observeExits(ctx) // Exact receipt read, no close assignment exists.
			raw, err := security.ReadPrivate(m.journalPath(operation), terminalOperationJournalMaxBytes)
			var retained terminalOperationJournal
			if err != nil || domain.Decode(raw, &retained) != nil || retained.Phase != terminalReported || retained.InstanceID != j.InstanceID || retained.ReportID != j.ReportID {
				t.Fatal("receipt acknowledgement was not synchronized before interrupted retirement", err)
			}
			if err := lock.Close(); err != nil {
				t.Fatal(err)
			}
			retry := newTerminalManager(ctx, m.config, nil, m.credential, domain.NewID())
			retry.observeExits(ctx) // Acknowledged local retirement requires no server record.
			for _, path := range []string{owner, owner + ".recovery.lock", m.shutdownPath(id), m.journalPath(operation)} {
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Fatal("replacement retained acknowledged ownership metadata", path, err)
				}
			}
			fixture.Lock()
			defer fixture.Unlock()
			if len(fixture.requests) != 2 {
				t.Fatal("retirement replayed a report or native work")
			}
			first, second := fixture.requests[0], fixture.requests[1]
			if first.InstanceId != string(j.InstanceID) || second.InstanceId != first.InstanceId || first.RequestId != second.RequestId || first.OperationId != second.OperationId || string(first.ResultJson) != string(second.ResultJson) {
				t.Fatal("replacement receipt read changed immutable request")
			}
			if natural && second.OperationId != "" {
				t.Fatal("spontaneous receipt manufactured an operation claim")
			}
		})
	}
}

func TestTerminalRetirementScanAdvancesThroughOversizedBacklog(t *testing.T) {
	m := newTerminalManager(context.Background(), Config{Root: t.TempDir(), Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}, nil, Credential{}, domain.NewID())
	defer m.close()
	directory := filepath.Join(m.config.Root, "terminal-operations")
	if err := security.PrivateDir(directory); err != nil {
		t.Fatal(err)
	}
	// Malformed retained entries have no receipt/native authority and must stay.
	// More than one scan batch must not permanently hide valid retirement work.
	for index := 0; index < 4097; index++ {
		if err := os.WriteFile(filepath.Join(directory, fmt.Sprintf("invalid-%04d.json", index)), []byte("{}"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	j := terminalOperationJournal{TerminalID: domain.NewID(), OperationID: domain.NewID(), InstanceID: m.instance, ReportID: domain.NewID(), Phase: terminalReported, Result: &terminal.Result{State: domain.TerminalUncertain, Rows: 24, Columns: 80, Problem: domain.TerminalUnavailable()}}
	if err := m.saveJournal(j); err != nil {
		t.Fatal(err)
	}
	for scan := 0; scan < 3; scan++ {
		m.observeExits(context.Background())
	}
	if _, err := os.Stat(m.journalPath(j.OperationID)); !os.IsNotExist(err) {
		t.Fatal("bounded scan permanently suppressed acknowledged retirement", err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 4097 {
		t.Fatal("scan discarded malformed evidence", err)
	}
}
