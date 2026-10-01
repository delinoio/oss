// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestBrowserRemovalSurvivesRestoreOfAnOlderActiveProfileBackup(t *testing.T) {
	f := newBrowserFixture(t)
	owner := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	if err := f.s.Store.BindIdentity(owner, f.s.Identity.ServerID); err != nil {
		t.Fatal(err)
	}
	first := f.register(t, f.first, f.session, domain.NewID())
	second := f.register(t, f.second, f.session, domain.NewID())
	revokedID := domain.NewID()
	doctorPut(t, f.s, domain.DeviceKind, revokedID, 0, domain.Device{Type: domain.ClientDevice})
	revokedContext := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.ClientDevice, DeviceID: revokedID})
	revoked := f.register(t, revokedContext, f.session, domain.NewID())
	backup, err := f.s.Store.Backup(owner)
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := f.s.Store.InspectBackup(owner, backup, f.s.Identity.ServerID)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(f.root, "backups", string(backup)+".sqlite")
	original, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	deletion := domain.NewID()
	if _, err = f.s.Store.Mutate(f.first, deletion, "browser.fixture.account-delete", nil, func(tx *store.Tx) (any, error) {
		return nil, tx.Delete(domain.AccountKind, f.account, 1)
	}); err != nil {
		t.Fatal(err)
	}
	pending, err := f.s.GetBrowserProfile(f.first, connect.NewRequest(&pb.GetBrowserProfileRequest{Id: first.Profile.Id}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.ConfirmBrowserProfileRemoval(f.first, connect.NewRequest(&pb.ConfirmBrowserProfileRemovalRequest{
		Mutation: &pb.Mutation{Id: first.Profile.Id, ExpectedRevision: pending.Msg.Profile.Revision, RequestId: string(domain.NewID())}, DeletionRequestId: string(deletion),
	})); err != nil {
		t.Fatal(err)
	}
	deviceRecord, err := f.s.Store.Get(owner, domain.DeviceKind, revokedID)
	if err != nil {
		t.Fatal(err)
	}
	device, err := store.Decode[domain.Device](deviceRecord)
	if err != nil {
		t.Fatal(err)
	}
	device.Revoked = true
	doctorPut(t, f.s, domain.DeviceKind, revokedID, deviceRecord.Revision, device)
	actors := []context.Context{f.first, f.second, revokedContext}
	profiles := []string{first.Profile.Id, second.Profile.Id, revoked.Profile.Id}
	expected := []domain.BrowserProfileRecord{}
	for i, ctx := range actors {
		actor, _ := domain.PrincipalFrom(ctx)
		err = f.s.Store.Read(owner, func(tx *store.Tx) error {
			profile, err := tx.BrowserProfile(actor.DeviceID, domain.ID(profiles[i]))
			expected = append(expected, profile)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	// The second device never reads removal before replacement. The third is
	// revoked and cannot acknowledge it. Restore must retain both obligations
	// and the first device's already completed state from the current safety image.
	for attempt := range 2 {
		if attempt == 0 {
			revision, err := f.s.Store.RestoreRevision(owner)
			if err != nil {
				t.Fatal(err)
			}
			input := store.BackupRestoreInput{Backup: inspection.Backup, SHA256: inspection.SHA256, ServerID: f.s.Identity.ServerID, Actor: domain.Principal{Type: domain.OwnerDevice}, ExpectedRevision: revision}
			if _, _, err = f.s.Store.RestoreBackup(owner, domain.NewID(), input); err != nil {
				t.Fatal(err)
			}
		}
		if err = f.s.Store.Close(); err != nil {
			t.Fatal(err)
		}
		f.s.Store, err = store.Open(owner, f.root)
		if err != nil {
			t.Fatal(err)
		}
		for i, ctx := range actors {
			actor, _ := domain.PrincipalFrom(ctx)
			err = f.s.Store.Read(owner, func(tx *store.Tx) error {
				retained, err := tx.BrowserProfile(actor.DeviceID, domain.ID(profiles[i]))
				if err == nil && !reflect.DeepEqual(retained, expected[i]) {
					t.Fatalf("restore %d changed the original browser obligation: %#v != %#v", attempt, retained, expected[i])
				}
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
		}
		counts, err := f.s.GetAccountBrowserCleanup(owner, connect.NewRequest(&pb.GetAccountBrowserCleanupRequest{AccountId: string(f.account)}))
		if err != nil || counts.Msg.Active != 0 || counts.Msg.Pending != 2 || counts.Msg.Removed != 1 {
			t.Fatal("restore resurrected account browser state", counts, err)
		}
		status, err := f.s.GetBrowserProfile(f.second, connect.NewRequest(&pb.GetBrowserProfileRequest{Id: second.Profile.Id}))
		if err != nil || status.Msg.Profile.State != pb.BrowserProfileState_BROWSER_PROFILE_STATE_REMOVAL_PENDING || status.Msg.Profile.DeletionRequestId != string(deletion) {
			t.Fatal("offline device lost its original removal identity", status, err)
		}
		if _, err = f.s.GetBrowserProfile(revokedContext, connect.NewRequest(&pb.GetBrowserProfileRequest{Id: revoked.Profile.Id})); connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Fatal("restore reauthorized a revoked browser device", err)
		}
		session, err := f.s.Store.Get(owner, domain.SessionKind, f.session)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.s.RegisterBrowserProfile(f.second, connect.NewRequest(&pb.RegisterBrowserProfileRequest{
			Session: &pb.Mutation{Id: string(f.session), ExpectedRevision: session.Revision, RequestId: string(domain.NewID())}, AccountId: string(f.account),
		})); connect.CodeOf(err) != connect.CodeNotFound {
			t.Fatal("restored deleted account authorized reopening", err)
		}
		unchanged, err := os.ReadFile(source)
		if err != nil || !bytes.Equal(original, unchanged) {
			t.Fatal("restore changed the selected historical source", err)
		}
	}
}
