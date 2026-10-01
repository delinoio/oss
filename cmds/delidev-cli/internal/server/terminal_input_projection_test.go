// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/terminal"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func TestTerminalInputDispatchNeverEntersPublicResourceProjection(t *testing.T) {
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
	raw, _ := json.Marshal(terminal.Result{State: domain.TerminalRunning, Rows: 24, Columns: 80, Shell: "/fixture/shell", Cwd: manifest.PrimaryPath})
	running, err := worker.ReportTerminal(ctx, ownerRequest(identity, &pb.ReportTerminalRequest{RequestId: string(domain.NewID()), MachineId: claim.MachineId, InstanceId: instance, TerminalId: claim.TerminalId, OperationId: claim.OperationId, ResultJson: raw}))
	if err != nil {
		t.Fatal(err)
	}
	input := []byte("fixture-invisible-input")
	control := &pb.ControlTerminalRequest{Mutation: acctMutation(running.Msg.Terminal, domain.NewID()), Action: pb.TerminalAction_TERMINAL_ACTION_INPUT, Input: input}
	accepted, err := product.ControlTerminal(ctx, ownerRequest(f.identity, control))
	if err != nil {
		t.Fatal(err)
	}
	check := func(label string, r *pb.Resource) {
		t.Helper()
		var public domain.Terminal
		if r == nil || domain.Decode(r.DocumentJson, &public) != nil || public.Pending == nil || public.Pending.Action != domain.TerminalInput || len(public.Pending.Input) != 0 {
			t.Fatal(label, "exposed input or removed pending control metadata")
		}
		if bytes.Contains(r.DocumentJson, input) || bytes.Contains(r.DocumentJson, []byte(base64.StdEncoding.EncodeToString(input))) {
			t.Fatal(label, "exposed accepted input bytes")
		}
	}
	check("control response", accepted.Msg.Terminal)
	check("inspect", currentCatalogResource(t, f, accepted.Msg.Terminal))
	listed, err := f.resources.ListResources(ctx, ownerRequest(f.identity, &pb.ListResourcesRequest{Filter: &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_TERMINAL, SessionId: session.Id, PageSize: 50}}))
	if err != nil || len(listed.Msg.Resources) != 1 {
		t.Fatal("terminal list", err)
	}
	check("list", listed.Msg.Resources[0])
	snapshot, err := f.resources.GetSnapshot(ctx, ownerRequest(f.identity, &pb.GetSnapshotRequest{Filter: &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_TERMINAL, SessionId: session.Id, PageSize: 50}}))
	if err != nil || len(snapshot.Msg.Resources) != 1 {
		t.Fatal("terminal snapshot", err)
	}
	check("snapshot", snapshot.Msg.Resources[0])
	output, err := product.WatchTerminalOutput(ctx, ownerRequest(f.identity, &pb.WatchTerminalOutputRequest{TerminalId: claim.TerminalId}))
	if err != nil || !output.Receive() {
		t.Fatal("output metadata", err)
	}
	check("output metadata", output.Msg().Terminal)
	_ = output.Close()
	replay, err := product.ControlTerminal(ctx, ownerRequest(f.identity, control))
	if err != nil || !replay.Msg.Replayed {
		t.Fatal("control receipt", err)
	}
	check("control receipt", replay.Msg.Terminal)
	assignments, err := worker.WatchTerminals(ctx, ownerRequest(identity, &pb.WatchTerminalsRequest{MachineId: claim.MachineId, InstanceId: instance}))
	if err != nil || !assignments.Receive() {
		t.Fatal("private dispatch", err)
	}
	var assignment terminal.Assignment
	if domain.Decode(assignments.Msg().AssignmentJson, &assignment) != nil || !bytes.Equal(assignment.Operation.Input, input) {
		t.Fatal("public redaction removed original Worker input")
	}
	_ = assignments.Close()
	claim.RequestId, claim.OperationId = string(domain.NewID()), string(assignment.Operation.ID)
	claimed, err := worker.ClaimTerminal(ctx, ownerRequest(identity, claim))
	if err != nil || domain.Decode(claimed.Msg.AssignmentJson, &assignment) != nil || !bytes.Equal(assignment.Operation.Input, input) {
		t.Fatal("private durable claim lost exact input", err)
	}
	check("claimed public resource", currentCatalogResource(t, f, accepted.Msg.Terminal))
}
