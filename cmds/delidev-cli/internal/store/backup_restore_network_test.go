// SPDX-License-Identifier: Apache-2.0
package store

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func TestBackupRestoreRetainsCurrentNetworkAuthority(t *testing.T) {
	s, root, ctx, input, _ := restoreFixture(t)
	profileID, routeID := domain.NewID(), domain.NewID()
	old := domain.NetworkProfile{ProxyDefinition: domain.ProxyDefinition{Name: "Original", Mode: domain.ProxyHTTP, Host: "127.0.0.1", Port: 3128}, CredentialGeneration: domain.NewID()}
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.original.network", nil, func(tx *Tx) (any, error) {
		if _, err := tx.Put(domain.NetworkProfileKind, profileID, 0, "", "", old); err != nil {
			return nil, err
		}
		return tx.Put(domain.NetworkRouteKind, routeID, 0, "", "", domain.NetworkRoute{ProfileID: profileID, ProfileRevision: 1, Profile: old})
	})
	if err != nil {
		t.Fatal(err)
	}
	backup, err := s.Backup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := s.InspectBackup(ctx, backup, input.ServerID)
	if err != nil {
		t.Fatal(err)
	}
	current := old
	current.Name, current.CredentialGeneration = "Current explicit selection", domain.NewID()
	newID := domain.NewID()
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.current.network", nil, func(tx *Tx) (any, error) {
		if _, err := tx.Put(domain.NetworkProfileKind, newID, 0, "", "", current); err != nil {
			return nil, err
		}
		if _, err := tx.Put(domain.NetworkRouteKind, routeID, 1, "", "", domain.NetworkRoute{ProfileID: newID, ProfileRevision: 1, Profile: current}); err != nil {
			return nil, err
		}
		return nil, tx.Delete(domain.NetworkProfileKind, profileID, 1)
	})
	if err != nil {
		t.Fatal(err)
	}
	wantProfile, err := s.Get(ctx, domain.NetworkProfileKind, newID)
	if err != nil {
		t.Fatal(err)
	}
	wantRoute, err := s.Get(ctx, domain.NetworkRouteKind, routeID)
	if err != nil {
		t.Fatal(err)
	}
	input.Backup, input.SHA256 = inspection.Backup, inspection.SHA256
	input.ExpectedRevision, err = s.RestoreRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	restored, _, err := s.RestoreBackup(ctx, domain.NewID(), input)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	// Restore freshens resource revisions to invalidate pre-restore mutations;
	// preserve the exact current body, including its pinned generation/route.
	wantProfile.Revision++
	wantRoute.Revision++
	profile, err := reopened.Get(ctx, domain.NetworkProfileKind, newID)
	wantProfile.UpdatedAt = restored.CreatedAt.Truncate(time.Millisecond)
	if err != nil || !reflect.DeepEqual(profile, wantProfile) {
		t.Fatalf("restore replaced current profile/generation: %v\ngot %+v\nwant %+v", err, profile, wantProfile)
	}
	route, err := reopened.Get(ctx, domain.NetworkRouteKind, routeID)
	wantRoute.UpdatedAt = restored.CreatedAt.Truncate(time.Millisecond)
	if err != nil || !reflect.DeepEqual(route, wantRoute) {
		t.Fatalf("restore reactivated historical routing or lost fresh revision: %v\ngot %+v\nwant %+v", err, route, wantRoute)
	}
	if _, err := reopened.Get(ctx, domain.NetworkProfileKind, profileID); domain.SafeError(err).Code != domain.NotFound {
		t.Fatal("restore resurrected deleted network authority", err)
	}
}

func TestBackupRestorePreservesPendingPrivateNetworkIntents(t *testing.T) {
	for _, name := range []string{"network-save-intent.json", "network-delete-intent.json"} {
		t.Run(name, func(t *testing.T) {
			s, root, ctx, input, _ := restoreFixture(t)
			path := filepath.Join(root, name)
			raw := []byte(`{"fixture":"unsettled private ownership"}`)
			if err := security.WriteAtomic(path, raw); err != nil {
				t.Fatal(err)
			}
			if _, _, err := s.RestoreBackup(ctx, domain.NewID(), input); err != nil {
				t.Fatal("pending credential receipts blocked restore", err)
			}
			if !s.RestoreFrozen() {
				t.Fatal("accepted restore did not freeze its original database")
			}
			if retained, err := os.ReadFile(path); err != nil || string(retained) != string(raw) {
				t.Fatal("restore replaced original cleanup evidence", err)
			}
		})
	}
}
