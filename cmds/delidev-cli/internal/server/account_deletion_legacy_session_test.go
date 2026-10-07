// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestAPIAccountDeletionWithLegacySessionPreparation(t *testing.T) {
	for _, referenced := range []bool{false, true} {
		name := "unrelated"
		if referenced {
			name = "referenced"
		}
		t.Run(name, func(t *testing.T) {
			f := newAccountFixture(t)
			ctx := context.Background()
			initial := f.newAccount(domain.BearerAuth)
			connected, err := connectAccount(f, initial, domain.NewID(), "synthetic-legacy-deletion-key", false)
			if err != nil {
				t.Fatal(err)
			}
			// Seed the old JSON shape directly, before reopening the real loopback
			// server. This fixture never copies user state or opens a native vault.
			f.shutdown()
			db, err := store.Open(ctx, f.root)
			if err != nil {
				t.Fatal(err)
			}
			sessionID, selected := domain.NewID(), domain.NewID()
			if referenced {
				selected = domain.ID(initial.Id)
			}
			legacy := map[string]any{
				"name": "Historical fixture", "agent_id": domain.NewID(),
				"archive": domain.Archived, "dispatch": domain.DispatchPaused,
				"start_preparation": map[string]string{"phase": "checking-installation", "discovery_job_id": string(domain.NewID())},
				"initial_execution": domain.InitialExecution{InitialAccountID: selected},
			}
			_, seedErr := db.Mutate(ctx, domain.NewID(), "fixture.legacy-session", nil, func(tx *store.Tx) (any, error) {
				return tx.Put(domain.SessionKind, sessionID, 0, sessionID, "", legacy)
			})
			closeErr := db.Close()
			if seedErr != nil || closeErr != nil {
				t.Fatal(seedErr, closeErr)
			}
			f.start()
			readSession := func() *pb.Resource {
				t.Helper()
				r, err := f.resources.GetResource(ctx, ownerRequest(f.identity, &pb.GetResourceRequest{Kind: pb.EntityKind_ENTITY_KIND_SESSION, Id: string(sessionID)}))
				if err != nil {
					t.Fatal(err)
				}
				return r.Msg.Resource
			}
			before := readSession()
			disconnected, err := f.accounts.DisconnectAccount(ctx, ownerRequest(f.identity, &pb.DisconnectAccountRequest{Mutation: acctMutation(connected.Msg.Account, domain.NewID())}))
			if err != nil || len(disconnected.Msg.CleanupProblemJson) != 0 {
				t.Fatal("credential cleanup failed", err)
			}
			if _, _, remaining := f.secrets.counts(); remaining != 0 {
				t.Fatal("disconnect retained synthetic credentials")
			}
			status, err := f.accounts.GetAccountStatus(ctx, ownerRequest(f.identity, &pb.GetAccountStatusRequest{Id: initial.Id}))
			if err != nil {
				t.Fatal(err)
			}
			request := &pb.DeleteConfigurationRequest{Kind: pb.EntityKind_ENTITY_KIND_ACCOUNT, Mutation: acctMutation(status.Msg.Account, domain.NewID())}
			deleted, err := f.config.DeleteConfiguration(ctx, ownerRequest(f.identity, request))
			if referenced {
				wantAccountCode(t, err, domain.Conflict)
				if _, err := f.accounts.GetAccountStatus(ctx, ownerRequest(f.identity, &pb.GetAccountStatusRequest{Id: initial.Id})); err != nil {
					t.Fatal("reference rejection removed the account", err)
				}
			} else {
				if err != nil || deleted.Msg.Id != initial.Id {
					t.Fatal("unrelated historical session blocked deletion", err)
				}
				replay, err := f.config.DeleteConfiguration(ctx, ownerRequest(f.identity, request))
				if err != nil || !replay.Msg.Replayed {
					t.Fatal("original deletion did not replay", err)
				}
				_, err = f.accounts.GetAccountStatus(ctx, ownerRequest(f.identity, &pb.GetAccountStatusRequest{Id: initial.Id}))
				wantAccountCode(t, err, domain.NotFound)
			}
			after := readSession()
			if after.Revision != before.Revision || string(after.DocumentJson) != string(before.DocumentJson) {
				t.Fatal("account deletion rewrote historical session ownership")
			}
			var session domain.Session
			if err := domain.Decode(after.DocumentJson, &session); err != nil {
				t.Fatal("historical session is unreadable", err)
			}
			raw, _ := json.Marshal(session)
			var fields map[string]json.RawMessage
			json.Unmarshal(raw, &fields)
			if fields["start_preparation"] == nil {
				t.Fatal("historical observation was lost")
			}
		})
	}
}
