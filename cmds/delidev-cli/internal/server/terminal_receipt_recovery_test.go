// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/terminal"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"google.golang.org/protobuf/proto"
)

func TestTerminalReplacementReadsOnlyCommittedOriginalReportReceipt(t *testing.T) {
	for _, purged := range []bool{false, true} {
		name := "retained-record"
		if purged {
			name = "purged-record"
		}
		t.Run(name, func(t *testing.T) {
			f, session, identity, worker, instance, _ := terminalFixture(t)
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
			claim := &pb.ClaimTerminalRequest{RequestId: string(domain.NewID()), MachineId: string(value.MachineID), InstanceId: instance, TerminalId: created.Msg.Terminal.Id, OperationId: string(value.Pending.ID)}
			if _, err := worker.ClaimTerminal(ctx, ownerRequest(identity, claim)); err != nil {
				t.Fatal(err)
			}
			// A positive original pre-native failure is a completed cleanup result.
			// This fixture verifies server receipt authority, not native shell cleanup.
			result := terminal.Result{State: domain.TerminalExited, CleanupVerified: true, Rows: 24, Columns: 80, Problem: domain.TerminalUnavailable()}
			raw, _ := json.Marshal(result)
			report := &pb.ReportTerminalRequest{RequestId: string(domain.NewID()), MachineId: claim.MachineId, InstanceId: instance, TerminalId: claim.TerminalId, OperationId: claim.OperationId, ResultJson: raw}
			committed, err := worker.ReportTerminal(ctx, ownerRequest(identity, report))
			if err != nil {
				t.Fatal(err)
			}
			original := committed.Msg.Terminal
			var authority domain.Terminal
			if err := domain.Decode(original.DocumentJson, &authority); err != nil {
				t.Fatal(err)
			}
			f.shutdown()
			db, err := store.Open(ctx, f.root)
			if err != nil {
				t.Fatal(err)
			}
			_, err = db.Mutate(ctx, domain.NewID(), "fixture.replace-terminal-worker", nil, func(tx *store.Tx) (any, error) {
				if err := tx.SetWorkerInstance(value.MachineID, domain.ID(instance), time.Now().Add(-time.Minute)); err != nil {
					return nil, err
				}
				if purged {
					return nil, tx.Delete(domain.TerminalKind, domain.ID(original.Id), original.Revision)
				}
				return nil, nil
			})
			closeErr := db.Close()
			if err != nil || closeErr != nil {
				t.Fatal(err, closeErr)
			}
			f.start()
			worker = delidevv1connect.NewWorkerServiceClient(http.DefaultClient, f.endpoint.URL)
			if _, err := worker.ReportTerminal(ctx, ownerRequest(identity, report)); err == nil {
				t.Fatal("expired Worker authority acknowledged receipt")
			}
			replacement := string(domain.NewID())
			if _, err := worker.AttachWorker(ctx, ownerRequest(identity, &pb.AttachWorkerRequest{RequestId: string(domain.NewID()), MachineId: claim.MachineId, InstanceId: replacement, Version: "0.1.0", Capabilities: []pb.WorkerCapability{pb.WorkerCapability_WORKER_CAPABILITY_SESSION_TERMINALS_V1}})); err != nil {
				t.Fatal(err)
			}
			if !purged {
				original = currentCatalogResource(t, f, original)
			}
			for attempt := 0; attempt < 2; attempt++ {
				ack, err := worker.ReportTerminal(ctx, ownerRequest(identity, report))
				if err != nil || ack.Msg.Terminal != nil {
					t.Fatal("exact committed receipt did not return content-free acknowledgement", err)
				}
			}
			changed := proto.Clone(report).(*pb.ReportTerminalRequest)
			changed.ResultJson = append(bytes.Clone(raw), ' ')
			if _, err := worker.ReportTerminal(ctx, ownerRequest(identity, changed)); connect.CodeOf(err) != connect.CodeAborted {
				t.Fatal("changed report bytes acknowledged", err)
			}
			changed = proto.Clone(report).(*pb.ReportTerminalRequest)
			changed.RequestId = string(domain.NewID())
			if _, err := worker.ReportTerminal(ctx, ownerRequest(identity, changed)); err == nil {
				t.Fatal("absent receipt granted old-instance reporting authority")
			}
			changed = proto.Clone(report).(*pb.ReportTerminalRequest)
			changed.MachineId = string(domain.NewID())
			if _, err := worker.ReportTerminal(ctx, ownerRequest(identity, changed)); connect.CodeOf(err) != connect.CodeNotFound {
				t.Fatal("missing selected machine was not rejected", err)
			}
			if !purged {
				current := currentCatalogResource(t, f, original)
				if current.Revision != original.Revision || !bytes.Equal(current.DocumentJson, original.DocumentJson) {
					t.Fatal("receipt reads or rejected requests changed terminal")
				}
			}
			// Fresh instance identities cannot recreate a completed original report.
			changed = proto.Clone(report).(*pb.ReportTerminalRequest)
			changed.InstanceId, changed.RequestId = replacement, string(domain.NewID())
			if _, err := worker.ReportTerminal(ctx, ownerRequest(identity, changed)); err == nil {
				t.Fatal("receipt acknowledgement revived native reporting authority")
			}
			device := currentCatalogResource(t, f, &pb.Resource{Kind: pb.EntityKind_ENTITY_KIND_DEVICE, Id: string(authority.DeviceID)})
			devices := delidevv1connect.NewDeviceServiceClient(http.DefaultClient, f.endpoint.URL)
			if _, err := devices.RevokeDevice(ctx, ownerRequest(f.identity, &pb.RevokeDeviceRequest{Mutation: acctMutation(device, domain.NewID())})); err != nil {
				t.Fatal(err)
			}
			if _, err := worker.ReportTerminal(ctx, ownerRequest(identity, report)); connect.CodeOf(err) != connect.CodeUnauthenticated {
				t.Fatal("revoked device acknowledged the original receipt", err)
			}
		})
	}
}
