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
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
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
	if len(request.Msg.ResultJson) > terminal.MaxResultBytes || domain.Decode(request.Msg.ResultJson, &result) != nil || result.Validate() != nil {
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
	t.Run("escaped-paths", func(t *testing.T) { terminalJournalReportRetry(t, false) })
	t.Run("maximum-report", func(t *testing.T) { terminalJournalReportRetry(t, true) })
}

func TestTerminalManagerRejectsAssignmentForDifferentMachine(t *testing.T) {
	manager := newTerminalManager(context.Background(), Config{Root: t.TempDir()}, nil, Credential{MachineID: domain.NewID()}, domain.NewID())
	assignment := terminal.Assignment{ID: domain.NewID(), SessionID: domain.NewID(), Terminal: domain.Terminal{MachineID: domain.NewID()}, Operation: domain.TerminalOperation{ID: domain.NewID(), Action: domain.TerminalCreate}}
	if err := manager.apply(context.Background(), assignment); err == nil {
		t.Fatal("Worker accepted a terminal assignment for another machine")
	}
	if len(manager.live) != 0 {
		t.Fatal("mismatched assignment started a native terminal")
	}
}

func TestTerminalManagerRejectsPreparationForDifferentMachine(t *testing.T) {
	machine, session := domain.NewID(), domain.NewID()
	manager := newTerminalManager(context.Background(), Config{Root: t.TempDir()}, nil, Credential{MachineID: machine}, domain.NewID())
	assignment := terminal.Assignment{
		ID:          domain.NewID(),
		SessionID:   session,
		Terminal:    domain.Terminal{MachineID: machine, State: domain.TerminalStarting, Rows: 24, Columns: 80},
		Operation:   domain.TerminalOperation{ID: domain.NewID(), Action: domain.TerminalCreate},
		Preparation: &workspace.PrepareRequest{SessionID: session, MachineID: domain.NewID(), Type: domain.GeneralChat},
		Manifest:    &workspace.Manifest{SessionID: session, MachineID: machine, Type: domain.GeneralChat, State: workspace.Ready},
	}
	result := manager.execute(assignment)
	if result.State != domain.TerminalUncertain || domain.SafeError(result.Problem).Code != domain.RecoveryRequired || len(manager.live) != 0 {
		t.Fatal("Worker accepted mismatched preparation machine authority", result)
	}
}

func terminalJournalReportRetry(t *testing.T, maximum bool) {
	t.Helper()
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
	result := terminal.Result{State: domain.TerminalClosed, CleanupVerified: true, Rows: 24, Columns: 80, Shell: "/" + strings.Repeat("<", 4095), Cwd: "/" + strings.Repeat("&", 4095)}
	if maximum {
		result.Problem = domain.Fail(domain.Internal, "", "Inspect the original terminal.")
		base, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		result.Problem.Message = strings.Repeat("x", terminal.MaxResultBytes-len(base))
	}
	journal := terminalOperationJournal{TerminalID: assignment.ID, OperationID: assignment.Operation.ID, InstanceID: instance, Digest: terminalOperationDigest(assignment), ClaimID: domain.NewID(), ReportID: domain.NewID(), Phase: terminalFinished, Result: &result}
	resultJSON, err := json.Marshal(result)
	if err != nil || result.Validate() != nil || len(resultJSON) <= 16<<10 || len(resultJSON) > terminal.MaxResultBytes {
		t.Fatal("fixture must exceed the old report limit with valid escaped paths", err)
	}
	if maximum && len(resultJSON) != terminal.MaxResultBytes {
		t.Fatal("fixture must fill the report envelope")
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
	journal.Result = &terminal.Result{State: domain.TerminalUncertain, Rows: 24, Columns: 80, Problem: domain.Fail(domain.RecoveryRequired, strings.Repeat("x", terminalOperationJournalMaxBytes), "Preserve the original operation.")}
	if err := manager.saveJournal(journal); err == nil || domain.SafeError(err).Code != domain.ResourceExhausted {
		t.Fatal("oversized journal write was accepted", err)
	}
	retained, err := os.ReadFile(manager.journalPath(journal.OperationID))
	if err != nil || !bytes.Equal(original, retained) {
		t.Fatal("rejected oversized write replaced original ownership evidence", err)
	}
}

func TestTerminalStartedCreationRetainsPreNativeCleanupIndex(t *testing.T) {
	ctx := context.Background()
	instance, machine, id, operation := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
	manager := newTerminalManager(ctx, Config{Root: t.TempDir()}, nil, Credential{MachineID: machine}, instance)
	assignment := terminal.Assignment{ID: id, SessionID: domain.NewID(), Terminal: domain.Terminal{MachineID: machine, ShellOverride: "/fixture/explicit-shell", Rows: 24, Columns: 80}, Operation: domain.TerminalOperation{ID: operation, Action: domain.TerminalCreate}}
	journal, err := manager.loadJournal(assignment)
	if err != nil {
		t.Fatal(err)
	}
	journal.Phase = terminalClaimed
	if err := manager.saveJournal(journal); err != nil {
		t.Fatal(err)
	}
	if err := manager.markStarted(&journal, assignment.Operation.Action); err != nil {
		t.Fatal(err)
	}
	retained, err := manager.loadJournal(assignment)
	if err != nil || retained.Phase != terminalStarted {
		t.Fatal("original start intent was not retained", err)
	}
	owner := filepath.Join(manager.processRoot(), string(id))
	if err := security.CheckPrivateDir(owner); err != nil {
		t.Fatal("started creation did not retain private owner index", err)
	}
	entries, err := os.ReadDir(owner)
	if err != nil || len(entries) != 0 {
		t.Fatal("pre-native creation unexpectedly launched a process", err)
	}
	// Simulate restart immediately at the synchronized start-intent boundary,
	// before execute or shell discovery. Replacement ownership may close only.
	restarted := newTerminalManager(ctx, Config{Root: manager.config.Root}, nil, Credential{MachineID: machine}, domain.NewID())
	assignment.Terminal.OwnerInstanceID = instance
	assignment.Terminal.Pending = &domain.TerminalOperation{ID: operation, Action: domain.TerminalCreate, Claimed: true}
	assignment.Operation = domain.TerminalOperation{ID: domain.NewID(), Action: domain.TerminalClose}
	if err := os.Rename(owner, owner+"-moved"); err != nil {
		t.Fatal(err)
	}
	missing := restarted.execute(assignment)
	if missing.State != domain.TerminalUncertain || missing.CleanupVerified || missing.Problem == nil || missing.Problem.Code != domain.RecoveryRequired {
		t.Fatal("missing original index manufactured cleanup", missing)
	}
	if err := os.Rename(owner+"-moved", owner); err != nil {
		t.Fatal(err)
	}
	result := restarted.execute(assignment)
	if result.State != domain.TerminalClosed || !result.CleanupVerified || result.Problem != nil || len(restarted.live) != 0 {
		t.Fatal("retained original pre-native ownership did not permit close reconciliation", result)
	}
}

func TestTerminalCreationOwnerFailurePreservesClaimedJournal(t *testing.T) {
	manager := newTerminalManager(context.Background(), Config{Root: t.TempDir()}, nil, Credential{}, domain.NewID())
	assignment := terminal.Assignment{ID: domain.NewID(), SessionID: domain.NewID(), Operation: domain.TerminalOperation{ID: domain.NewID(), Action: domain.TerminalCreate}}
	journal, err := manager.loadJournal(assignment)
	if err != nil {
		t.Fatal(err)
	}
	journal.Phase = terminalClaimed
	if err := manager.saveJournal(journal); err != nil {
		t.Fatal(err)
	}
	if err := security.PrivateDir(manager.processRoot()); err != nil {
		t.Fatal(err)
	}
	owner := filepath.Join(manager.processRoot(), string(assignment.ID))
	if err := security.WriteAtomic(owner, []byte("foreign owner-index fixture")); err != nil {
		t.Fatal(err)
	}
	if err := manager.markStarted(&journal, assignment.Operation.Action); err == nil {
		t.Fatal("creation accepted invalid original owner index")
	}
	retained, err := manager.loadJournal(assignment)
	if err != nil || journal.Phase != terminalClaimed || retained.Phase != terminalClaimed {
		t.Fatal("failed index preparation consumed the original native start intent", err)
	}
	raw, err := os.ReadFile(owner)
	if err != nil || string(raw) != "foreign owner-index fixture" {
		t.Fatal("failed preparation replaced foreign ownership", err)
	}
}
