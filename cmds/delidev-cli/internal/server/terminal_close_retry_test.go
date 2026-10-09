// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/terminal"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func TestSessionDeletionReissuesUncertainTerminalClose(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		name := "same-instance"
		if replacement {
			name = "replacement-instance"
		}
		t.Run(name, func(t *testing.T) {
			f, session, identity, worker, instance, manifest := terminalFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
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
			originalInstance := value.OwnerInstanceID
			claim := &pb.ClaimTerminalRequest{RequestId: string(domain.NewID()), MachineId: string(value.MachineID), InstanceId: instance, TerminalId: created.Msg.Terminal.Id, OperationId: string(value.Pending.ID)}
			if _, err := worker.ClaimTerminal(ctx, ownerRequest(identity, claim)); err != nil {
				t.Fatal(err)
			}
			result := terminal.Result{State: domain.TerminalRunning, Rows: 24, Columns: 80, Shell: "/fixture/shell", Cwd: manifest.PrimaryPath}
			raw, _ := json.Marshal(result)
			if _, err := worker.ReportTerminal(ctx, ownerRequest(identity, &pb.ReportTerminalRequest{RequestId: string(domain.NewID()), MachineId: claim.MachineId, InstanceId: instance, TerminalId: claim.TerminalId, OperationId: claim.OperationId, ResultJson: raw})); err != nil {
				t.Fatal(err)
			}
			deletion := &pb.DeleteSessionRequest{Mutation: acctMutation(currentCatalogResource(t, f, session), domain.NewID())}
			if _, err := sessionClient(f).DeleteSession(ctx, ownerRequest(f.identity, deletion)); err != nil {
				t.Fatal(err)
			}
			current := currentCatalogResource(t, f, created.Msg.Terminal)
			value = domain.Terminal{}
			if err := domain.Decode(current.DocumentJson, &value); err != nil || value.CloseRequestID == "" {
				t.Fatal("deletion did not request terminal cleanup", err)
			}
			claim.RequestId, claim.OperationId = string(domain.NewID()), string(value.CloseRequestID)
			if _, err := worker.ClaimTerminal(ctx, ownerRequest(identity, claim)); err != nil {
				t.Fatal(err)
			}
			result.State, result.OutputLost = domain.TerminalUncertain, true
			result.Problem = domain.Fail(domain.RecoveryRequired, "Fixture cleanup remains unconfirmed.", "Reconcile original process ownership.")
			raw, _ = json.Marshal(result)
			uncertain := &pb.ReportTerminalRequest{RequestId: string(domain.NewID()), MachineId: claim.MachineId, InstanceId: instance, TerminalId: claim.TerminalId, OperationId: claim.OperationId, ResultJson: raw}
			reported, err := worker.ReportTerminal(ctx, ownerRequest(identity, uncertain))
			if err != nil {
				t.Fatal(err)
			}
			current = reported.Msg.Terminal
			value = domain.Terminal{}
			if err := domain.Decode(current.DocumentJson, &value); err != nil || value.CloseRequestID.Validate() != nil || string(value.CloseRequestID) == uncertain.OperationId || value.CleanupVerified || value.Pending != nil || !value.OutputLost || value.OwnerInstanceID != originalInstance {
				t.Logf("terminal_close_state state=%s close_id=%s old_close_id=%s cleanup_verified=%t pending=%t output_lost=%t owner_instance=%s original_instance=%s", value.State, value.CloseRequestID, uncertain.OperationId, value.CleanupVerified, value.Pending != nil, value.OutputLost, value.OwnerInstanceID, originalInstance)
				t.Fatal("uncertain cleanup lost or reused the completed close intent", err)
			}
			retryID := value.CloseRequestID
			for attempt := 0; attempt < 2; attempt++ {
				replay, err := worker.ReportTerminal(ctx, ownerRequest(identity, uncertain))
				if err != nil || replay.Msg.Terminal.Revision != current.Revision || !bytes.Equal(replay.Msg.Terminal.DocumentJson, current.DocumentJson) {
					t.Fatal("lost report acknowledgement replaced the next close or altered original facts", err)
				}
			}
			if _, err := sessionClient(f).DeleteSession(ctx, ownerRequest(f.identity, deletion)); err != nil {
				t.Fatal("deletion replay failed", err)
			}
			current = currentCatalogResource(t, f, current)
			value = domain.Terminal{}
			if err := domain.Decode(current.DocumentJson, &value); err != nil || value.CloseRequestID != retryID {
				t.Fatal("deletion replay changed the pending reconciliation", err)
			}

			if replacement {
				// Expire only this controlled fixture's original connection lease.
				// The paired device and process-owner instance remain unchanged.
				f.shutdown()
				db, err := store.Open(ctx, f.root)
				if err != nil {
					t.Fatal(err)
				}
				_, err = db.Mutate(ctx, domain.NewID(), "fixture.expire-terminal-worker", nil, func(tx *store.Tx) (any, error) {
					return nil, tx.SetWorkerInstance(domain.ID(claim.MachineId), domain.ID(instance), time.Now().Add(-time.Minute))
				})
				if closeErr := db.Close(); err != nil || closeErr != nil {
					t.Fatal("fixture lease expiration failed", err, closeErr)
				}
				f.start()
				worker = delidevv1connect.NewWorkerServiceClient(http.DefaultClient, f.endpoint.URL)
				instance = string(domain.NewID())
				if _, err := worker.AttachWorker(ctx, ownerRequest(identity, &pb.AttachWorkerRequest{ProtocolVersion: 2, RequestId: string(domain.NewID()), MachineId: claim.MachineId, InstanceId: instance, Version: "0.1.0", Capabilities: []pb.WorkerCapability{pb.WorkerCapability_WORKER_CAPABILITY_SESSION_TERMINALS_V1}})); err != nil {
					t.Fatal(err)
				}
				current = currentCatalogResource(t, f, current)
				ack, err := worker.ReportTerminal(ctx, ownerRequest(identity, uncertain))
				if err != nil || ack.Msg.Terminal != nil {
					t.Fatal("replacement could not read exact receipt without terminal projection", err)
				}
				unchanged := currentCatalogResource(t, f, current)
				if unchanged.Revision != current.Revision || !bytes.Equal(unchanged.DocumentJson, current.DocumentJson) {
					t.Fatal("receipt-only acknowledgement changed the next cleanup intent")
				}
			}
			// Keep a primary stream for the complete retry observation, independent
			// of terminalFixture's shorter workspace-preparation stream.
			primary, err := worker.WatchWork(ctx, ownerRequest(identity, &pb.WatchWorkRequest{MachineId: claim.MachineId, InstanceId: instance}))
			if err != nil || !primary.Receive() || !primary.Msg().Heartbeat {
				t.Fatal("missing primary Worker stream", err)
			}
			defer primary.Close()
			list := &pb.ListSessionDeletionWorkRequest{MachineId: claim.MachineId, InstanceId: instance}
			work, err := worker.ListSessionDeletionWork(ctx, ownerRequest(identity, list))
			if err != nil || len(work.Msg.WorkJson) != 0 {
				t.Fatal("uncertain terminal released workspace removal", err)
			}
			view, err := worker.WatchTerminals(ctx, ownerRequest(identity, &pb.WatchTerminalsRequest{MachineId: claim.MachineId, InstanceId: instance}))
			if err != nil {
				t.Fatal(err)
			}
			defer view.Close()
			var assignment terminal.Assignment
			for {
				if !view.Receive() {
					t.Fatal("uncertain close was never reassigned", view.Err())
				}
				if len(view.Msg().AssignmentJson) != 0 {
					if err := domain.Decode(view.Msg().AssignmentJson, &assignment); err != nil {
						t.Fatal(err)
					}
					break
				}
			}
			updated, err := time.Parse(time.RFC3339Nano, currentCatalogResource(t, f, current).UpdatedAt)
			if err != nil || time.Since(updated) < 10*time.Second {
				t.Fatal("uncertain cleanup was dispatched without its bounded retry delay", err)
			}
			if assignment.ID != domain.ID(claim.TerminalId) || assignment.Operation.ID != retryID || assignment.Operation.Action != domain.TerminalClose || assignment.Terminal.OwnerInstanceID != originalInstance || assignment.Preparation != nil || assignment.Manifest != nil {
				t.Fatal("retry gained shell or workspace-creation authority")
			}
			claim.RequestId, claim.OperationId, claim.InstanceId = string(domain.NewID()), string(retryID), instance
			if _, err := worker.ClaimTerminal(ctx, ownerRequest(identity, claim)); err != nil {
				t.Fatal("current original-device instance cannot claim reissued close", err)
			}
			// This is an authenticated cleanup-report fixture. Native process
			// reconciliation and finished-result reuse have separate Worker tests.
			result.State, result.CleanupVerified, result.Problem, result.OutputLost = domain.TerminalClosed, true, nil, false
			raw, _ = json.Marshal(result)
			closed, err := worker.ReportTerminal(ctx, ownerRequest(identity, &pb.ReportTerminalRequest{RequestId: string(domain.NewID()), MachineId: claim.MachineId, InstanceId: instance, TerminalId: claim.TerminalId, OperationId: claim.OperationId, ResultJson: raw}))
			if err != nil {
				t.Fatal(err)
			}
			value = domain.Terminal{}
			if err := domain.Decode(closed.Msg.Terminal.DocumentJson, &value); err != nil || !value.CleanupVerified || value.CloseRequestID != "" || !value.OutputLost {
				t.Fatal("confirmed cleanup lost original loss or retained executable work", err)
			}
			if !replacement {
				replay, err := worker.ReportTerminal(ctx, ownerRequest(identity, uncertain))
				if err != nil || replay.Msg.Terminal.Revision != closed.Msg.Terminal.Revision || !bytes.Equal(replay.Msg.Terminal.DocumentJson, closed.Msg.Terminal.DocumentJson) {
					t.Fatal("old uncertain receipt regressed confirmed cleanup", err)
				}
			}
			work, err = worker.ListSessionDeletionWork(ctx, ownerRequest(identity, list))
			if err != nil || len(work.Msg.WorkJson) != 1 {
				t.Fatal("confirmed terminal cleanup did not release workspace removal", err)
			}
		})
	}
}
