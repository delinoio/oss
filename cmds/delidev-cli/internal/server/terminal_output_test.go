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

func TestTerminalOutputLossPreservesAcknowledgedCursor(t *testing.T) {
	for _, reconnect := range []bool{false, true} {
		name := "attached"
		if reconnect {
			name = "reattached"
		}
		t.Run(name, func(t *testing.T) {
			f, session, worker, client, instance, manifest := terminalFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			product := delidevv1connect.NewTerminalServiceClient(http.DefaultClient, f.endpoint.URL)
			accepted, err := product.CreateTerminal(ctx, ownerRequest(f.identity, &pb.CreateTerminalRequest{Mutation: acctMutation(session, domain.NewID()), Rows: 24, Columns: 80}))
			if err != nil {
				t.Fatal(err)
			}
			var value domain.Terminal
			if domain.Decode(accepted.Msg.Terminal.DocumentJson, &value) != nil {
				t.Fatal("invalid terminal")
			}
			claim := &pb.ClaimTerminalRequest{RequestId: string(domain.NewID()), MachineId: string(value.MachineID), InstanceId: instance, TerminalId: accepted.Msg.Terminal.Id, OperationId: string(value.Pending.ID)}
			if _, err := client.ClaimTerminal(ctx, ownerRequest(worker, claim)); err != nil {
				t.Fatal(err)
			}
			result := terminal.Result{State: domain.TerminalRunning, Shell: "/fixture/shell", Cwd: manifest.PrimaryPath, Rows: 24, Columns: 80}
			raw, _ := json.Marshal(result)
			if _, err := client.ReportTerminal(ctx, ownerRequest(worker, &pb.ReportTerminalRequest{RequestId: string(domain.NewID()), MachineId: claim.MachineId, InstanceId: instance, TerminalId: claim.TerminalId, OperationId: claim.OperationId, ResultJson: raw})); err != nil {
				t.Fatal(err)
			}
			nativeEpoch := domain.NewID()
			for sequence := uint64(1); sequence <= 2; sequence++ {
				if _, err := client.PublishTerminalOutput(ctx, ownerRequest(worker, &pb.PublishTerminalOutputRequest{MachineId: claim.MachineId, InstanceId: instance, TerminalId: claim.TerminalId, Epoch: string(nativeEpoch), Sequence: sequence, Data: []byte("already displayed")})); err != nil {
					t.Fatal(err)
				}
			}
			view, err := product.WatchTerminalOutput(ctx, ownerRequest(f.identity, &pb.WatchTerminalOutputRequest{TerminalId: claim.TerminalId}))
			if err != nil {
				t.Fatal(err)
			}
			defer view.Close()
			epoch := ""
			for {
				if !view.Receive() {
					t.Fatal("missing retained bytes", view.Err())
				}
				epoch = view.Msg().Epoch
				if len(view.Msg().Data) != 0 && view.Msg().Sequence == 2 {
					break
				}
			}
			if reconnect {
				view.Close()
			}
			result.State, result.CleanupVerified, result.OutputLost = domain.TerminalExited, true, true
			raw, _ = json.Marshal(result)
			if _, err := client.ReportTerminal(ctx, ownerRequest(worker, &pb.ReportTerminalRequest{RequestId: string(domain.NewID()), MachineId: claim.MachineId, InstanceId: instance, TerminalId: claim.TerminalId, ResultJson: raw})); err != nil {
				t.Fatal(err)
			}
			if reconnect {
				view, err = product.WatchTerminalOutput(ctx, ownerRequest(f.identity, &pb.WatchTerminalOutputRequest{TerminalId: claim.TerminalId, Epoch: epoch, AfterSequence: 2}))
				if err != nil {
					t.Fatal(err)
				}
				defer view.Close()
			}
			sawGap := false
			for view.Receive() {
				frame := view.Msg()
				if len(frame.Data) != 0 {
					t.Fatal("output-loss notification replayed acknowledged bytes")
				}
				if frame.Epoch != epoch || frame.Sequence != 2 {
					t.Fatal("output-loss notification changed the acknowledged cursor")
				}
				sawGap = sawGap || frame.Gap
			}
			if view.Err() != nil || !sawGap {
				t.Fatal("missing final output-loss gap", view.Err())
			}
		})
	}
}
