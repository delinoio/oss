// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/terminal"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type terminalSupersededFixture struct {
	terminalJournalReportFixture
	close terminal.Assignment
}

func (f *terminalSupersededFixture) ClaimTerminal(_ context.Context, req *connect.Request[pb.ClaimTerminalRequest]) (*connect.Response[pb.ClaimTerminalResponse], error) {
	if req.Msg.OperationId != string(f.close.Operation.ID) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, nil)
	}
	raw, _ := json.Marshal(f.close)
	return connect.NewResponse(&pb.ClaimTerminalResponse{AssignmentJson: raw}), nil
}

func TestTerminalCloseRetiresRejectedPreparedOperationAfterAcknowledgement(t *testing.T) {
	for _, action := range []domain.TerminalAction{domain.TerminalCreate, domain.TerminalInput, domain.TerminalResize} {
		t.Run(string(action), func(t *testing.T) {
			ctx := context.Background()
			f := &terminalSupersededFixture{}
			_, handler := delidevv1connect.NewWorkerServiceHandler(f)
			server := httptest.NewServer(handler)
			defer server.Close()
			m := newTerminalManager(ctx, Config{Root: t.TempDir(), Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}, delidevv1connect.NewWorkerServiceClient(http.DefaultClient, server.URL), Credential{MachineID: domain.NewID()}, domain.NewID())
			a := terminal.Assignment{ID: domain.NewID(), SessionID: domain.NewID(), Terminal: domain.Terminal{MachineID: m.credential.MachineID, InstanceID: m.instance, OwnerInstanceID: m.instance, State: domain.TerminalRunning, Rows: 24, Columns: 80}, Operation: domain.TerminalOperation{ID: domain.NewID(), Action: action, Rows: 24, Columns: 80}}
			if err := m.apply(ctx, a); err == nil {
				t.Fatal("superseded operation unexpectedly claimed native work")
			}
			prepared, err := m.loadJournal(a)
			if err != nil || prepared.Phase != terminalPrepared {
				t.Fatal("failed claim did not preserve original prepared journal", err)
			}
			for _, path := range []string{m.processRoot(), filepath.Join(m.processRoot(), string(a.ID))} {
				if err := security.PrivateDir(path); err != nil {
					t.Fatal(err)
				}
			}
			closed := a
			closed.Terminal.Pending = &a.Operation
			closed.Operation = domain.TerminalOperation{ID: domain.NewID(), Action: domain.TerminalClose}
			f.close = closed
			if err := m.apply(ctx, closed); err == nil {
				t.Fatal("expected lost close acknowledgement")
			}
			for _, path := range []string{m.journalPath(a.Operation.ID), m.supersededPath(a.ID)} {
				if _, err := os.Stat(path); err != nil {
					t.Fatal("unacknowledged close discarded displaced evidence", err)
				}
			}
			if err := m.apply(ctx, closed); err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{m.journalPath(a.Operation.ID), m.supersededPath(a.ID), m.journalPath(closed.Operation.ID)} {
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Fatal("acknowledged cleanup retained prepared metadata", err)
				}
			}
		})
	}
}

func TestTerminalSupersededMetadataSurvivesUncertainCloseAndReplacement(t *testing.T) {
	m := newTerminalManager(context.Background(), Config{Root: t.TempDir(), Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}, nil, Credential{}, domain.NewID())
	if err := security.PrivateDir(filepath.Join(m.config.Root, "terminal-operations")); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []terminalOperationPhase{terminalPrepared, terminalClaimed, terminalStarted, terminalFinished} {
		id, operation := domain.NewID(), domain.NewID()
		pending := terminalOperationJournal{TerminalID: id, OperationID: operation, InstanceID: m.instance, ClaimID: domain.NewID(), ReportID: domain.NewID(), Digest: strings.Repeat("a", 64), Phase: phase}
		if err := m.saveJournal(pending); err != nil {
			t.Fatal(err)
		}
		if err := m.saveSuperseded(id, operation); err != nil {
			t.Fatal(err)
		}
		uncertain := terminalOperationJournal{TerminalID: id, OperationID: domain.NewID(), Phase: terminalReported, Result: &terminal.Result{State: domain.TerminalUncertain, Rows: 24, Columns: 80, Problem: domain.TerminalUnavailable()}}
		if err := m.retireReported(uncertain); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(m.supersededPath(id)); err != nil {
			t.Fatal("uncertain close discarded displaced identity", err)
		}
		replacement := newTerminalManager(context.Background(), m.config, nil, m.credential, domain.NewID())
		confirmed := terminalOperationJournal{TerminalID: id, OperationID: domain.NewID(), Phase: terminalReported, Result: &terminal.Result{State: domain.TerminalClosed, CleanupVerified: true, Rows: 24, Columns: 80}}
		if err := replacement.retireReported(confirmed); err != nil {
			t.Fatal(err)
		}
		_, err := os.Stat(m.journalPath(operation))
		if phase == terminalPrepared && !os.IsNotExist(err) || phase != terminalPrepared && err != nil {
			t.Fatal("cleanup did not distinguish prepared from native ownership evidence", phase, err)
		}
	}
}
