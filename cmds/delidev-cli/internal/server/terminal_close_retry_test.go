// SPDX-License-Identifier: Apache-2.0
package server

import (
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
			current := currentCatalogResource(t, f, created.Msg.Terminal)
			closing, err := product.ControlTerminal(ctx, ownerRequest(f.identity, &pb.ControlTerminalRequest{Mutation: acctMutation(current, domain.NewID()), Action: pb.TerminalAction_TERMINAL_ACTION_CLOSE}))
			if err != nil {
				t.Fatal(err)
			}
			if err := domain.Decode(closing.Msg.Terminal.DocumentJson, &value); err != nil {
				t.Fatal(err)
			}
			claim.RequestId, claim.OperationId = string(domain.NewID()), string(value.CloseRequestID)
			if _, err := worker.ClaimTerminal(ctx, ownerRequest(identity, claim)); err != nil {
				t.Fatal(err)
			}
			result.State, result.OutputLost = domain.TerminalUncertain, true
			result.Problem = domain.Fail(domain.RecoveryRequired, "Fixture cleanup remains unconfirmed.", "Continue with the retained observation.")
			raw, _ = json.Marshal(result)
			uncertain := &pb.ReportTerminalRequest{RequestId: string(domain.NewID()), MachineId: claim.MachineId, InstanceId: instance, TerminalId: claim.TerminalId, OperationId: claim.OperationId, ResultJson: raw}
			reported, err := worker.ReportTerminal(ctx, ownerRequest(identity, uncertain))
			if err != nil {
				t.Fatal(err)
			}
			var observed domain.Terminal
			if domain.Decode(reported.Msg.Terminal.DocumentJson, &observed) != nil || observed.CleanupVerified || observed.OwnerInstanceID != originalInstance {
				t.Fatal("uncertainty rewrote native provenance or fabricated cleanup")
			}
			deletion := &pb.DeleteSessionRequest{Mutation: acctMutation(currentCatalogResource(t, f, session), domain.NewID())}
			deleted, err := sessionClient(f).DeleteSession(ctx, ownerRequest(f.identity, deletion))
			if err != nil || deleted.Msg.Job.WorkersPending == 0 {
				t.Fatal("deletion blocked or invented Worker cleanup", err)
			}
			owner := domain.WithPrincipal(ctx, domain.Principal{Type: domain.OwnerDevice})
			f.shutdown()
			db, err := store.Open(ctx, f.root)
			if err != nil {
				t.Fatal(err)
			}

			if _, err := db.PurgeDeletedSession(owner, domain.ID(session.Id)); err != nil {
				t.Fatal("unknown terminal cleanup blocked database purge", err)
			}
			if _, err := db.Get(owner, domain.TerminalKind, domain.ID(created.Msg.Terminal.Id)); domain.SafeError(err).Code != domain.NotFound {
				t.Fatal("terminal projection survived deletion", err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			f.start()
			worker = delidevv1connect.NewWorkerServiceClient(http.DefaultClient, f.endpoint.URL)
			if replacement {
				instance = string(domain.NewID())
				if _, err := worker.AttachWorker(ctx, ownerRequest(identity, &pb.AttachWorkerRequest{RequestId: string(domain.NewID()), MachineId: claim.MachineId, InstanceId: instance, Version: "0.1.0", Capabilities: []pb.WorkerCapability{pb.WorkerCapability_WORKER_CAPABILITY_SESSION_TERMINALS_V1}})); err != nil {
					t.Fatal(err)
				}
			}
			replayed, err := worker.ReportTerminal(ctx, ownerRequest(identity, uncertain))
			if err != nil || replayed.Msg.Terminal != nil {
				t.Fatal("retained uncertain receipt could not be observed after purge", err)
			}
			work, err := worker.ListSessionDeletionWork(ctx, ownerRequest(identity, &pb.ListSessionDeletionWorkRequest{MachineId: claim.MachineId, InstanceId: instance}))
			if err != nil || len(work.Msg.WorkJson) != 1 {
				t.Fatal("unknown cleanup blocked independent deletion work", err)
			}
		})
	}
}
