// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
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

func TestTerminalOutputDiscardedRingExposesFreshAttachmentGap(t *testing.T) {
	for _, discard := range []string{"retained", "evicted", "restarted"} {
		t.Run(discard, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			db, err := store.Open(ctx, filepath.Join(t.TempDir(), "state"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			id := domain.NewID()
			_, err = db.Mutate(ctx, domain.NewID(), "fixture.closed-terminal", id, func(tx *store.Tx) (any, error) {
				return tx.Put(domain.TerminalKind, id, 0, domain.NewID(), "", domain.Terminal{State: domain.TerminalClosed, CleanupVerified: true, Rows: 24, Columns: 80})
			})
			if err != nil {
				t.Fatal(err)
			}
			identity := security.Identity{ServerID: domain.NewID(), Token: "fixture-output-owner"}
			logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
			service := &Service{Store: db, Identity: identity, logger: logger}
			ring := service.terminalRing(id)
			ring.sequence, ring.bytes = 1, len("retained")
			ring.chunks = []terminalOutputChunk{{sequence: 1, data: []byte("retained")}}
			switch discard {
			case "evicted":
				for i := 0; i < 128; i++ {
					service.terminalRing(domain.NewID())
				}
				if service.terminalOutputs[id] != nil || len(service.terminalOutputs) != 128 {
					t.Fatal("fixture did not evict the oldest bounded ring")
				}
			case "restarted":
				// A new service retains the database, but no ephemeral rings.
				service = &Service{Store: db, Identity: identity, logger: logger}
			}
			endpoint := httptest.NewServer(service.Handler(nil, true))
			defer func() { service.executionAuthority.cancel(); endpoint.Close(); service.executionAuthority.close() }()
			client := delidevv1connect.NewTerminalServiceClient(http.DefaultClient, endpoint.URL)
			view, err := client.WatchTerminalOutput(ctx, ownerRequest(identity, &pb.WatchTerminalOutputRequest{TerminalId: string(id)}))
			if err != nil {
				t.Fatal(err)
			}
			defer view.Close()
			if !view.Receive() {
				t.Fatal("missing initial observation", view.Err())
			}
			if view.Msg().Gap != (discard != "retained") {
				t.Fatal("fresh attachment misrepresented retained output completeness")
			}
			var data []byte
			for view.Receive() {
				data = append(data, view.Msg().Data...)
			}
			if view.Err() != nil {
				t.Fatal(view.Err())
			}
			if discard == "retained" && string(data) != "retained" {
				t.Fatal("complete retained output was lost")
			}
			if discard != "retained" && len(data) != 0 {
				t.Fatal("discarded output was reconstructed")
			}
		})
	}
}

func TestTerminalOutputEvictionPreservesExactAccessOrder(t *testing.T) {
	service := &Service{}
	first, second := domain.NewID(), domain.NewID()
	retained := service.terminalRing(first)
	service.terminalRing(second)
	for i := 0; i < 126; i++ {
		service.terminalRing(domain.NewID())
	}
	if service.terminalRing(first) != retained {
		t.Fatal("touch replaced original output ownership")
	}
	service.terminalRing(domain.NewID())
	if service.terminalOutputs[first] != retained || service.terminalOutputs[second] != nil || len(service.terminalOutputs) != 128 || service.terminalOutputOrder.Len() != 128 {
		t.Fatal("bounded eviction did not preserve the most recently observed ring")
	}
}
