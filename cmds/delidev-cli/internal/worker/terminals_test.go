// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/terminal"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type terminalJournalReportFixture struct {
	delidevv1connect.UnimplementedWorkerServiceHandler
	sync.Mutex
	requests []*pb.ReportTerminalRequest
}

func (f *terminalJournalReportFixture) ReportTerminal(_ context.Context, request *connect.Request[pb.ReportTerminalRequest]) (*connect.Response[pb.ReportTerminalResponse], error) {
	var result terminal.Result
	if len(request.Msg.ResultJson) > 16<<10 || domain.Decode(request.Msg.ResultJson, &result) != nil || result.Validate() != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}
	f.Lock()
	defer f.Unlock()
	f.requests = append(f.requests, request.Msg)
	if len(f.requests) == 1 {
		return nil, connect.NewError(connect.CodeUnavailable, nil)
	}
	return connect.NewResponse(&pb.ReportTerminalResponse{}), nil
}

func TestTerminalJournalLargeValidResultRetriesExactFinishedReport(t *testing.T) {
	ctx := context.Background()
	fixture := &terminalJournalReportFixture{}
	_, handler := delidevv1connect.NewWorkerServiceHandler(fixture)
	server := httptest.NewServer(handler)
	defer server.Close()
	instance, machine := domain.NewID(), domain.NewID()
	manager := newTerminalManager(ctx, Config{Root: t.TempDir()}, delidevv1connect.NewWorkerServiceClient(http.DefaultClient, server.URL), Credential{MachineID: machine}, instance)
	if err := security.PrivateDir(filepath.Join(manager.config.Root, "terminal-operations")); err != nil {
		t.Fatal(err)
	}
	assignment := terminal.Assignment{ID: domain.NewID(), SessionID: domain.NewID(), Terminal: domain.Terminal{MachineID: machine}, Operation: domain.TerminalOperation{ID: domain.NewID(), Action: domain.TerminalClose}}
	result := terminal.Result{State: domain.TerminalClosed, CleanupVerified: true, Rows: 24, Columns: 80, Shell: "/" + strings.Repeat("<", 1350), Cwd: "/" + strings.Repeat("&", 1350)}
	journal := terminalOperationJournal{TerminalID: assignment.ID, OperationID: assignment.Operation.ID, InstanceID: instance, Digest: terminalOperationDigest(assignment), ClaimID: domain.NewID(), ReportID: domain.NewID(), Phase: terminalFinished, Result: &result}
	resultJSON, err := json.Marshal(result)
	if err != nil || result.Validate() != nil || len(resultJSON) > 16<<10 {
		t.Fatal("fixture must satisfy the server's terminal report bound", err)
	}
	journalJSON, err := json.Marshal(journal)
	if err != nil || len(journalJSON) <= 16<<10 {
		t.Fatal("fixture must exceed the old journal bound", err)
	}
	if err := manager.saveJournal(journal); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.loadJournal(assignment); err != nil {
		t.Fatal("valid finished result could not be reloaded", err)
	}
	if err := manager.apply(ctx, assignment); err == nil || domain.SafeError(err).Code != domain.ServerUnavailable {
		t.Fatal("expected lost report acknowledgement", err)
	}
	// The auxiliary observation loop must also reload the complete journal
	// after a lost response, without repeating native work or a new claim.
	manager.observeExits(ctx)
	fixture.Lock()
	defer fixture.Unlock()
	if len(fixture.requests) != 2 {
		t.Fatalf("report count = %d, want 2", len(fixture.requests))
	}
	for _, request := range fixture.requests {
		if request.RequestId != string(journal.ReportID) || request.TerminalId != string(journal.TerminalID) || request.OperationId != string(journal.OperationID) || !bytes.Equal(request.ResultJson, resultJSON) {
			t.Fatal("retry changed original report identity or result bytes")
		}
	}
	if len(manager.live) != 0 {
		t.Fatal("finished journal retry created a native shell")
	}
	if _, err := os.Stat(manager.journalPath(journal.OperationID)); !os.IsNotExist(err) {
		t.Fatal("confirmed report did not retire its original journal", err)
	}
}

func TestTerminalJournalOversizedWritePreservesOriginal(t *testing.T) {
	manager := newTerminalManager(context.Background(), Config{Root: t.TempDir()}, nil, Credential{}, domain.NewID())
	if err := security.PrivateDir(filepath.Join(manager.config.Root, "terminal-operations")); err != nil {
		t.Fatal(err)
	}
	journal := terminalOperationJournal{TerminalID: domain.NewID(), OperationID: domain.NewID(), InstanceID: manager.instance, ClaimID: domain.NewID(), ReportID: domain.NewID(), Phase: terminalPrepared}
	if err := manager.saveJournal(journal); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(manager.journalPath(journal.OperationID))
	if err != nil {
		t.Fatal(err)
	}
	journal.Phase = terminalFinished
	journal.Result = &terminal.Result{State: domain.TerminalUncertain, Rows: 24, Columns: 80, Problem: domain.Fail(domain.RecoveryRequired, strings.Repeat("x", 32<<10), "Preserve the original operation.")}
	if err := manager.saveJournal(journal); err == nil || domain.SafeError(err).Code != domain.ResourceExhausted {
		t.Fatal("oversized journal write was accepted", err)
	}
	retained, err := os.ReadFile(manager.journalPath(journal.OperationID))
	if err != nil || !bytes.Equal(original, retained) {
		t.Fatal("rejected oversized write replaced original ownership evidence", err)
	}
}
