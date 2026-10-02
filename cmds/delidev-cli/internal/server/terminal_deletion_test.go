// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/terminal"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func TestSessionDeletionJoinsTerminalBeforeWorkspaceRemoval(t *testing.T) {
	f, session, identity, worker, instance, manifest := terminalFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	product := delidevv1connect.NewTerminalServiceClient(http.DefaultClient, f.endpoint.URL)
	created, err := product.CreateTerminal(ctx, ownerRequest(f.identity, &pb.CreateTerminalRequest{Mutation: acctMutation(session, domain.NewID()), Rows: 24, Columns: 80}))
	if err != nil {
		t.Fatal(err)
	}
	var value domain.Terminal
	if err := domain.Decode(created.Msg.Terminal.DocumentJson, &value); err != nil {
		t.Fatal(err)
	}
	claim := &pb.ClaimTerminalRequest{RequestId: string(domain.NewID()), MachineId: string(value.MachineID), InstanceId: instance, TerminalId: created.Msg.Terminal.Id, OperationId: string(value.Pending.ID)}
	if _, err := worker.ClaimTerminal(ctx, ownerRequest(identity, claim)); err != nil {
		t.Fatal(err)
	}
	result := terminal.Result{State: domain.TerminalRunning, Rows: 24, Columns: 80, Shell: "/fixture/shell", Cwd: manifest.PrimaryPath}
	raw, _ := json.Marshal(result)
	if _, err := worker.ReportTerminal(ctx, ownerRequest(identity, &pb.ReportTerminalRequest{RequestId: string(domain.NewID()), MachineId: claim.MachineId, InstanceId: instance, TerminalId: claim.TerminalId, OperationId: claim.OperationId, ResultJson: raw})); err != nil {
		t.Fatal(err)
	}
	current := currentCatalogResource(t, f, session)
	request := &pb.DeleteSessionRequest{Mutation: acctMutation(current, domain.NewID())}
	deleted, err := sessionClient(f).DeleteSession(ctx, ownerRequest(f.identity, request))
	if err != nil || deleted.Msg.Job.WorkersPending != 1 {
		t.Fatal("deletion did not retain original workspace ownership", deleted, err)
	}
	terminalNow := currentCatalogResource(t, f, created.Msg.Terminal)
	if err := domain.Decode(terminalNow.DocumentJson, &value); err != nil || value.CloseRequestID == "" || value.CleanupVerified {
		t.Fatal("deletion did not request original terminal cleanup", value, err)
	}
	closeID := value.CloseRequestID
	if _, err := sessionClient(f).DeleteSession(ctx, ownerRequest(f.identity, request)); err != nil {
		t.Fatal("deletion retry failed", err)
	}
	terminalNow = currentCatalogResource(t, f, terminalNow)
	if err := domain.Decode(terminalNow.DocumentJson, &value); err != nil || value.CloseRequestID != closeID {
		t.Fatal("deletion retry changed the close operation", value, err)
	}
	list := &pb.ListSessionDeletionWorkRequest{MachineId: claim.MachineId, InstanceId: instance}
	work, err := worker.ListSessionDeletionWork(ctx, ownerRequest(identity, list))
	if err != nil || len(work.Msg.WorkJson) != 0 {
		t.Fatal("workspace removal raced a live terminal", work, err)
	}
	current = currentCatalogResource(t, f, session)
	if _, err := product.CreateTerminal(ctx, ownerRequest(f.identity, &pb.CreateTerminalRequest{Mutation: acctMutation(current, domain.NewID()), Rows: 24, Columns: 80})); err == nil {
		t.Fatal("deleting session created another shell")
	}
	if _, err := product.ControlTerminal(ctx, ownerRequest(f.identity, &pb.ControlTerminalRequest{Mutation: acctMutation(terminalNow, domain.NewID()), Action: pb.TerminalAction_TERMINAL_ACTION_INPUT, Input: []byte("must not run\n")})); err == nil {
		t.Fatal("deleting session accepted input after close")
	}
	claim.RequestId, claim.OperationId = string(domain.NewID()), string(closeID)
	if _, err := worker.ClaimTerminal(ctx, ownerRequest(identity, claim)); err != nil {
		t.Fatal("deletion blocked original close authority", err)
	}
	// This is an authenticated ownership-report fixture, not native process
	// evidence. PTY descendant joining is covered by the process/Worker fixtures.
	result.State, result.CleanupVerified = domain.TerminalClosed, true
	raw, _ = json.Marshal(result)
	report := &pb.ReportTerminalRequest{RequestId: string(domain.NewID()), MachineId: claim.MachineId, InstanceId: instance, TerminalId: claim.TerminalId, OperationId: claim.OperationId, ResultJson: raw}
	if _, err := worker.ReportTerminal(ctx, ownerRequest(identity, report)); err != nil {
		t.Fatal("deletion blocked independently confirmed terminal cleanup", err)
	}
	if _, err := worker.ReportTerminal(ctx, ownerRequest(identity, report)); err != nil {
		t.Fatal("cleanup receipt retry failed during deletion", err)
	}
	work, err = worker.ListSessionDeletionWork(ctx, ownerRequest(identity, list))
	if err != nil || len(work.Msg.WorkJson) != 1 {
		t.Fatal("joined terminal did not release original workspace deletion", work, err)
	}
}
