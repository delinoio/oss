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
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/terminal"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type terminalCloseRecoveryFixture struct {
	delidevv1connect.UnimplementedWorkerServiceHandler
	sync.Mutex
	assignment terminal.Assignment
	original   terminalOperationJournal
	claims     []*pb.ClaimTerminalRequest
	reports    []*pb.ReportTerminalRequest
	loseClaim  bool
	loseReport bool
}

func (f *terminalCloseRecoveryFixture) ClaimTerminal(_ context.Context, request *connect.Request[pb.ClaimTerminalRequest]) (*connect.Response[pb.ClaimTerminalResponse], error) {
	f.Lock()
	defer f.Unlock()
	if request.Msg.RequestId == string(f.original.ClaimID) || request.Msg.TerminalId != string(f.assignment.ID) || request.Msg.OperationId != string(f.assignment.Operation.ID) {
		return nil, connect.NewError(connect.CodeAborted, nil)
	}
	f.claims = append(f.claims, request.Msg)
	if f.loseClaim {
		f.loseClaim = false
		return nil, connect.NewError(connect.CodeUnavailable, nil)
	}
	a := f.assignment
	a.Terminal.InstanceID = domain.ID(request.Msg.InstanceId)
	raw, _ := json.Marshal(a)
	return connect.NewResponse(&pb.ClaimTerminalResponse{AssignmentJson: raw}), nil
}

func (f *terminalCloseRecoveryFixture) ReportTerminal(_ context.Context, request *connect.Request[pb.ReportTerminalRequest]) (*connect.Response[pb.ReportTerminalResponse], error) {
	f.Lock()
	defer f.Unlock()
	if len(f.claims) == 0 || request.Msg.InstanceId != f.claims[len(f.claims)-1].InstanceId || request.Msg.RequestId == string(f.original.ReportID) || request.Msg.OperationId != string(f.assignment.Operation.ID) {
		return nil, connect.NewError(connect.CodeAborted, nil)
	}
	f.reports = append(f.reports, request.Msg)
	if f.loseReport {
		f.loseReport = false
		return nil, connect.NewError(connect.CodeUnavailable, nil)
	}
	return connect.NewResponse(&pb.ReportTerminalResponse{}), nil
}

func TestTerminalReplacementResumesOriginalCloseJournal(t *testing.T) {
	for _, phase := range []terminalOperationPhase{terminalPrepared, terminalClaimed, terminalStarted, terminalFinished} {
		t.Run(string(phase), func(t *testing.T) {
			ctx := context.Background()
			machine, originalInstance, replacement := domain.NewID(), domain.NewID(), domain.NewID()
			a := terminal.Assignment{ID: domain.NewID(), SessionID: domain.NewID(), Terminal: domain.Terminal{MachineID: machine, OwnerInstanceID: originalInstance, InstanceID: originalInstance, State: domain.TerminalRunning, Rows: 24, Columns: 80}, Operation: domain.TerminalOperation{ID: domain.NewID(), Action: domain.TerminalClose}}
			original := terminalOperationJournal{TerminalID: a.ID, OperationID: a.Operation.ID, InstanceID: originalInstance, Digest: terminalOperationDigest(a), ClaimID: domain.NewID(), ReportID: domain.NewID(), Phase: phase}
			if phase == terminalFinished {
				original.Result = &terminal.Result{State: domain.TerminalClosed, CleanupVerified: true, OutputLost: true, Rows: 24, Columns: 80}
			}
			fixture := &terminalCloseRecoveryFixture{assignment: a, original: original, loseClaim: true, loseReport: true}
			_, handler := delidevv1connect.NewWorkerServiceHandler(fixture)
			server := httptest.NewServer(handler)
			defer server.Close()
			manager := newTerminalManager(ctx, Config{Root: t.TempDir()}, delidevv1connect.NewWorkerServiceClient(http.DefaultClient, server.URL), Credential{MachineID: machine}, replacement)
			for _, directory := range []string{filepath.Join(manager.config.Root, "terminal-operations"), manager.processRoot(), filepath.Join(manager.processRoot(), string(a.ID))} {
				if err := security.PrivateDir(directory); err != nil {
					t.Fatal(err)
				}
			}
			if err := manager.saveJournal(original); err != nil {
				t.Fatal(err)
			}
			manager.observeExits(ctx)
			if len(fixture.reports) != 0 {
				t.Fatal("old journal reported without replacement claim")
			}
			if err := manager.apply(ctx, a); err == nil || domain.SafeError(err).Code != domain.ServerUnavailable {
				t.Fatal("expected lost replacement claim acknowledgement", err)
			}
			manager.observeExits(ctx)
			if len(fixture.reports) != 0 {
				t.Fatal("unconfirmed replacement claim granted report authority")
			}
			if err := manager.apply(ctx, a); err == nil || domain.SafeError(err).Code != domain.ServerUnavailable {
				t.Fatal("expected lost replacement report acknowledgement", err)
			}
			retained, err := manager.loadJournal(a)
			if err != nil || retained.InstanceID != original.InstanceID || retained.ClaimID != original.ClaimID || retained.ReportID != original.ReportID || retained.Digest != original.Digest {
				t.Fatal("replacement changed original journal identity", err)
			}
			var result terminal.Result
			if err := domain.Decode(fixture.reports[0].ResultJson, &result); err != nil || result.State != domain.TerminalClosed || !result.CleanupVerified {
				t.Fatal("original close did not reconcile cleanup", err, result)
			}
			if phase == terminalFinished {
				raw, _ := json.Marshal(original.Result)
				if !bytes.Equal(raw, fixture.reports[0].ResultJson) {
					t.Fatal("replacement changed retained native result")
				}
			}
			manager.observeExits(ctx)
			if len(fixture.claims) != 2 || fixture.claims[0].RequestId != fixture.claims[1].RequestId || len(fixture.reports) != 2 || fixture.reports[0].RequestId != fixture.reports[1].RequestId || !bytes.Equal(fixture.reports[0].ResultJson, fixture.reports[1].ResultJson) {
				t.Fatal("response loss changed replacement receipt identity or result")
			}
			if len(manager.live) != 0 {
				t.Fatal("close recovery created a native shell")
			}
			if _, err := os.Stat(manager.journalPath(a.Operation.ID)); !os.IsNotExist(err) {
				t.Fatal("confirmed close did not retire original journal", err)
			}
		})
	}
}

func TestTerminalReplacementRejectsNonCloseAndChangedJournals(t *testing.T) {
	for _, action := range []domain.TerminalAction{domain.TerminalCreate, domain.TerminalInput, domain.TerminalResize, domain.TerminalClose} {
		t.Run(string(action), func(t *testing.T) {
			manager := newTerminalManager(context.Background(), Config{Root: t.TempDir()}, nil, Credential{MachineID: domain.NewID()}, domain.NewID())
			a := terminal.Assignment{ID: domain.NewID(), SessionID: domain.NewID(), Terminal: domain.Terminal{MachineID: manager.credential.MachineID}, Operation: domain.TerminalOperation{ID: domain.NewID(), Action: action}}
			if err := security.PrivateDir(filepath.Join(manager.config.Root, "terminal-operations")); err != nil {
				t.Fatal(err)
			}
			journal := terminalOperationJournal{TerminalID: a.ID, OperationID: a.Operation.ID, InstanceID: domain.NewID(), Digest: terminalOperationDigest(a), ClaimID: domain.NewID(), ReportID: domain.NewID(), Phase: terminalStarted}
			if action == domain.TerminalClose {
				journal.Digest = "changed-original-operation"
			}
			if err := manager.saveJournal(journal); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(manager.journalPath(a.Operation.ID))
			if err != nil {
				t.Fatal(err)
			}
			if err := manager.apply(context.Background(), a); err == nil || domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatal("replacement accepted old native operation or changed close", err)
			}
			after, err := os.ReadFile(manager.journalPath(a.Operation.ID))
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("rejected replacement altered original evidence", err)
			}
		})
	}
}

func TestTerminalCloseRecoveryReplacementAcknowledgesExactCommittedReceipt(t *testing.T) {
	ctx := context.Background()
	a := terminal.Assignment{ID: domain.NewID(), SessionID: domain.NewID(), Terminal: domain.Terminal{MachineID: domain.NewID(), Rows: 24, Columns: 80}, Operation: domain.TerminalOperation{ID: domain.NewID(), Action: domain.TerminalClose}}
	original := terminalOperationJournal{TerminalID: a.ID, OperationID: a.Operation.ID, InstanceID: domain.NewID(), Digest: terminalOperationDigest(a), ClaimID: domain.NewID(), ReportID: domain.NewID(), Phase: terminalFinished, Result: &terminal.Result{State: domain.TerminalClosed, CleanupVerified: true, OutputLost: true, Rows: 24, Columns: 80}}
	fixture := &terminalCloseRecoveryFixture{assignment: a, original: original, loseReport: true}
	_, handler := delidevv1connect.NewWorkerServiceHandler(fixture)
	server := httptest.NewServer(handler)
	defer server.Close()
	root := t.TempDir()
	if err := security.PrivateDir(filepath.Join(root, "terminal-operations")); err != nil {
		t.Fatal(err)
	}
	client := delidevv1connect.NewWorkerServiceClient(http.DefaultClient, server.URL)
	first := newTerminalManager(ctx, Config{Root: root}, client, Credential{MachineID: a.Terminal.MachineID}, domain.NewID())
	if err := first.saveJournal(original); err != nil {
		t.Fatal(err)
	}
	if err := first.apply(ctx, a); err == nil || domain.SafeError(err).Code != domain.ServerUnavailable {
		t.Fatal("expected first report response loss", err)
	}
	second := newTerminalManager(ctx, Config{Root: root}, client, first.credential, domain.NewID())
	second.observeExits(ctx)
	raw, _ := json.Marshal(original.Result)
	if len(fixture.claims) != 1 || len(fixture.reports) != 2 || fixture.reports[0].RequestId != fixture.reports[1].RequestId || fixture.reports[0].InstanceId != fixture.reports[1].InstanceId {
		t.Fatal("receipt acknowledgement changed original identity or claimed native authority")
	}
	for _, report := range fixture.reports {
		if !bytes.Equal(raw, report.ResultJson) {
			t.Fatal("replacement changed original finished result bytes")
		}
	}
	if _, err := os.Stat(second.journalPath(original.OperationID)); !os.IsNotExist(err) {
		t.Fatal("committed original receipt did not retire finished journal", err)
	}
}

func TestTerminalCloseRecoveryMissingOwnerRemainsUncertain(t *testing.T) {
	ctx := context.Background()
	a := terminal.Assignment{ID: domain.NewID(), SessionID: domain.NewID(), Terminal: domain.Terminal{MachineID: domain.NewID(), Rows: 24, Columns: 80}, Operation: domain.TerminalOperation{ID: domain.NewID(), Action: domain.TerminalClose}}
	original := terminalOperationJournal{TerminalID: a.ID, OperationID: a.Operation.ID, InstanceID: domain.NewID(), Digest: terminalOperationDigest(a), ClaimID: domain.NewID(), ReportID: domain.NewID(), Phase: terminalStarted}
	fixture := &terminalCloseRecoveryFixture{assignment: a, original: original}
	_, handler := delidevv1connect.NewWorkerServiceHandler(fixture)
	server := httptest.NewServer(handler)
	defer server.Close()
	manager := newTerminalManager(ctx, Config{Root: t.TempDir()}, delidevv1connect.NewWorkerServiceClient(http.DefaultClient, server.URL), Credential{MachineID: a.Terminal.MachineID}, domain.NewID())
	if err := security.PrivateDir(filepath.Join(manager.config.Root, "terminal-operations")); err != nil {
		t.Fatal(err)
	}
	if err := manager.saveJournal(original); err != nil {
		t.Fatal(err)
	}
	if err := manager.apply(ctx, a); err != nil {
		t.Fatal(err)
	}
	var result terminal.Result
	if len(fixture.reports) != 1 || domain.Decode(fixture.reports[0].ResultJson, &result) != nil || result.State != domain.TerminalUncertain || result.CleanupVerified || result.Problem == nil || result.Problem.Code != domain.RecoveryRequired {
		t.Fatal("missing ownership manufactured close cleanup", result)
	}
}
