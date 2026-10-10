// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/terminal"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func TestTerminalNotificationsDeliverWithoutSafetyTicks(t *testing.T) {
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	service := &Service{Store: db, Identity: security.Identity{ServerID: domain.NewID(), Token: "fixture-terminal-notifications"}, logger: slog.New(slog.NewJSONHandler(io.Discard, nil)), terminalWatchInterval: time.Hour}
	endpoint := httptest.NewServer(service.Handler(nil, true))
	t.Cleanup(func() { service.executionAuthority.cancel(); endpoint.Close(); service.executionAuthority.close() })
	base := &accountFixture{t: t, endpoint: Endpoint{URL: endpoint.URL, ServerID: service.Identity.ServerID}, identity: service.Identity}
	base.config = delidevv1connect.NewConfigurationServiceClient(http.DefaultClient, endpoint.URL)
	base.accounts = delidevv1connect.NewAccountServiceClient(http.DefaultClient, endpoint.URL)
	base.resources = delidevv1connect.NewResourceServiceClient(http.DefaultClient, endpoint.URL)
	f, session, identity, worker, instance, manifest := terminalFixtureForAccount(t, base)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	product := delidevv1connect.NewTerminalServiceClient(http.DefaultClient, endpoint.URL)
	created, err := product.CreateTerminal(ctx, ownerRequest(f.identity, &pb.CreateTerminalRequest{Mutation: acctMutation(session, domain.NewID()), Rows: 24, Columns: 80}))
	if err != nil {
		t.Fatal(err)
	}
	var value domain.Terminal
	if domain.Decode(created.Msg.Terminal.DocumentJson, &value) != nil {
		t.Fatal("invalid fixture terminal")
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
	assignments, err := worker.WatchTerminals(ctx, ownerRequest(identity, &pb.WatchTerminalsRequest{MachineId: claim.MachineId, InstanceId: instance}))
	if err != nil || !assignments.Receive() {
		t.Fatal("missing initial assignment heartbeat", err)
	}
	defer assignments.Close()
	views := make([]*connect.ServerStreamForClient[pb.WatchTerminalOutputResponse], 2)
	for i := range views {
		views[i], err = product.WatchTerminalOutput(ctx, ownerRequest(f.identity, &pb.WatchTerminalOutputRequest{TerminalId: claim.TerminalId}))
		if err != nil || !views[i].Receive() {
			t.Fatal("missing initial output metadata", err)
		}
		defer views[i].Close()
	}
	received := make(chan terminal.Assignment, 1)
	go func() {
		for assignments.Receive() {
			var next terminal.Assignment
			if len(assignments.Msg().AssignmentJson) > 0 && domain.Decode(assignments.Msg().AssignmentJson, &next) == nil {
				received <- next
				return
			}
		}
	}()
	control := &pb.ControlTerminalRequest{Mutation: acctMutation(running.Msg.Terminal, domain.NewID()), Action: pb.TerminalAction_TERMINAL_ACTION_INPUT, Input: []byte("fixture")}
	accepted, err := product.ControlTerminal(ctx, ownerRequest(f.identity, control))
	if err != nil {
		t.Fatal(err)
	}
	var next terminal.Assignment
	select {
	case next = <-received:
	case <-time.After(2 * time.Second):
		t.Fatal("committed input waited for disabled safety tick")
	}
	if !bytes.Equal(next.Operation.Input, control.Input) {
		t.Fatal("notification changed original assignment bytes")
	}
	claim.RequestId, claim.OperationId = string(domain.NewID()), string(next.Operation.ID)
	if _, err := worker.ClaimTerminal(ctx, ownerRequest(identity, claim)); err != nil {
		t.Fatal(err)
	}
	completed, err := worker.ReportTerminal(ctx, ownerRequest(identity, &pb.ReportTerminalRequest{RequestId: string(domain.NewID()), MachineId: claim.MachineId, InstanceId: instance, TerminalId: claim.TerminalId, OperationId: claim.OperationId, ResultJson: raw}))
	if err != nil {
		t.Fatal(err)
	}
	// Both metadata and bytes must progress while the safety timer is an hour away.
	for _, view := range views {
		for {
			if !view.Receive() {
				t.Fatal("missing committed result metadata", view.Err())
			}
			if view.Msg().Terminal != nil && view.Msg().Terminal.Revision == completed.Msg.Terminal.Revision {
				break
			}
		}
	}
	nativeEpoch := domain.NewID()
	for sequence := uint64(1); sequence <= 3; sequence++ {
		service.terminalOutputMu.Lock()
		before := service.terminalOutputNotification()
		service.terminalOutputMu.Unlock()
		frame := &pb.PublishTerminalOutputRequest{MachineId: claim.MachineId, InstanceId: instance, TerminalId: claim.TerminalId, Epoch: string(nativeEpoch), Sequence: sequence, Data: []byte{byte(sequence)}}
		if _, err := worker.PublishTerminalOutput(ctx, ownerRequest(identity, frame)); err != nil {
			t.Fatal(err)
		}
		select {
		case <-before:
		default:
			t.Fatal("publication did not wake captured snapshot notification")
		}
		service.terminalOutputMu.Lock()
		after := service.terminalOutputNotification()
		service.terminalOutputMu.Unlock()
		if _, err := worker.PublishTerminalOutput(ctx, ownerRequest(identity, frame)); err != nil {
			t.Fatal("exact retry rejected", err)
		}
		changed := *frame
		changed.Data = []byte("changed retry")
		if _, err := worker.PublishTerminalOutput(ctx, ownerRequest(identity, &changed)); connect.CodeOf(err) != connect.CodeAborted {
			t.Fatal("changed retry was accepted", err)
		}
		select {
		case <-after:
			t.Fatal("retry or rejection falsely signaled new output")
		default:
		}
		for _, view := range views {
			if !view.Receive() || view.Msg().Sequence != sequence || !bytes.Equal(view.Msg().Data, frame.Data) {
				t.Fatal("attached observer missed ordered once-only output", view.Err())
			}
		}
	}
	replay, err := product.ControlTerminal(ctx, ownerRequest(f.identity, control))
	if err != nil || !replay.Msg.Replayed || replay.Msg.Terminal.Id != accepted.Msg.Terminal.Id {
		t.Fatal("notification changed control receipt replay", err)
	}
}
