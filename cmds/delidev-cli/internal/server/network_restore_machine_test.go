// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func TestNetworkRestoredWorkerRouteRemainsAdministrativelyClearable(t *testing.T) {
	for _, existed := range []bool{false, true} {
		name := "machine-created-after-backup"
		if existed {
			name = "machine-updated-after-backup"
		}
		t.Run(name, func(t *testing.T) {
			f := newIntegrationFixture(t)
			vault := &accountTestSecrets{values: map[credentials.Ref][]byte{}, removed: map[credentials.Ref]bool{}}
			f.service.accountSecrets = vault
			ctx := networkIntentContext()
			if err := f.service.Store.BindIdentity(ctx, f.service.Identity.ServerID); err != nil {
				t.Fatal(err)
			}
			machineID, deviceID := domain.NewID(), domain.NewID()
			machine := domain.Machine{Name: "Current Worker", OS: "linux", Architecture: "arm64", Version: "fixture-version", LastSeen: time.Now().UTC()}
			if err := machine.Validate(); err != nil {
				t.Fatal(err)
			}
			if existed {
				old := machine
				old.Name, old.LastSeen = "Historical Worker", time.Time{}
				doctorPut(t, f.service, domain.MachineKind, machineID, 0, old)
			}
			backup, err := f.service.Store.Backup(ctx)
			if err != nil {
				t.Fatal(err)
			}
			inspection, err := f.service.Store.InspectBackup(ctx, backup, f.service.Identity.ServerID)
			if err != nil {
				t.Fatal(err)
			}
			// Inject a private paired Worker fixture, including its verifier. The
			// restore must retain route metadata without retaining this authority.
			digest := sha256.Sum256([]byte("private-worker-restore-fixture"))
			_, err = f.service.Store.Mutate(ctx, domain.NewID(), "fixture.current.worker", machineID, func(tx *store.Tx) (any, error) {
				revision := uint64(0)
				if existed {
					revision = 1
				}
				if _, err := tx.Put(domain.MachineKind, machineID, revision, "", "", machine); err != nil {
					return nil, err
				}
				if _, err := tx.Put(domain.DeviceKind, deviceID, 0, "", "", domain.Device{Name: "Worker fixture", Type: domain.WorkerDevice, MachineID: machineID, PairedAt: time.Now().UTC()}); err != nil {
					return nil, err
				}
				return nil, tx.PutCredential(deviceID, digest[:])
			})
			if err != nil {
				t.Fatal(err)
			}
			if principal, err := f.service.Store.Authenticate(context.Background(), digest[:]); err != nil || principal.Type != domain.WorkerDevice {
				t.Fatal("fixture Worker has no original authority", err)
			}
			wantMachine, err := f.service.Store.Get(ctx, domain.MachineKind, machineID)
			if err != nil {
				t.Fatal(err)
			}
			profileID := domain.NewID()
			created, err := f.service.SaveNetworkProfile(ctx, networkIntentRequest(profileID))
			if err != nil {
				t.Fatal(err)
			}
			client := delidevv1connect.NewNetworkServiceClient(f.httpServer.Client(), f.url)
			selected, err := client.SelectNetworkProfile(context.Background(), ownerRequest(f.service.Identity, &pb.SelectNetworkProfileRequest{
				Mutation: &pb.Mutation{RequestId: string(domain.NewID())}, MachineId: string(machineID), ProfileId: string(profileID), ProfileRevision: created.Msg.Resource.Revision,
			}))
			if err != nil {
				t.Fatal(err)
			}
			revision, err := f.service.Store.RestoreRevision(ctx)
			if err != nil {
				t.Fatal(err)
			}
			input := store.BackupRestoreInput{Backup: inspection.Backup, SHA256: inspection.SHA256, ServerID: f.service.Identity.ServerID, Actor: domain.Principal{Type: domain.OwnerDevice}, ExpectedRevision: revision}
			if _, _, err := f.service.Store.RestoreBackup(ctx, domain.NewID(), input); err != nil {
				t.Fatal(err)
			}
			f.restart()
			f.service.accountSecrets = vault
			client = delidevv1connect.NewNetworkServiceClient(f.httpServer.Client(), f.url)
			route, err := client.GetNetworkRoute(context.Background(), ownerRequest(f.service.Identity, &pb.GetNetworkRouteRequest{MachineId: string(machineID)}))
			if err != nil || route.Msg.Route == nil || !bytes.Equal(route.Msg.Route.DocumentJson, selected.Msg.Resource.DocumentJson) || route.Msg.Route.Revision != selected.Msg.Resource.Revision+1 {
				t.Fatal("restored Worker route cannot be read or changed its pinned authority", err)
			}
			gotMachine, err := f.service.Store.Get(ctx, domain.MachineKind, machineID)
			if err != nil || !bytes.Equal(gotMachine.Data, wantMachine.Data) || gotMachine.Revision != wantMachine.Revision+1 {
				t.Fatal("restore lost current route-owned machine metadata", err)
			}
			if _, err := f.service.Store.Authenticate(context.Background(), digest[:]); err == nil {
				t.Fatal("restored machine metadata revived Worker authorization")
			}
			if _, err := client.SelectNetworkProfile(context.Background(), ownerRequest(f.service.Identity, &pb.SelectNetworkProfileRequest{
				Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: route.Msg.Route.Id, ExpectedRevision: route.Msg.Route.Revision}, MachineId: string(machineID),
			})); err != nil {
				t.Fatal("owner cannot clear restored route without pairing the Worker", err)
			}
			profile, err := f.service.Store.Get(ctx, domain.NetworkProfileKind, profileID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.DeleteNetworkProfile(context.Background(), ownerRequest(f.service.Identity, &pb.DeleteNetworkProfileRequest{
				Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(profileID), ExpectedRevision: profile.Revision},
			})); err != nil {
				t.Fatal("cleared restored route still prevents profile deletion", err)
			}
			if _, _, remaining := vault.counts(); remaining != 0 {
				t.Fatal("profile deletion retained its native credential")
			}
		})
	}
}
