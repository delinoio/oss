// SPDX-License-Identifier: Apache-2.0
package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestBackupRestoreReceiptRequiresOriginalActor(t *testing.T) {
	for _, actorType := range []domain.DeviceType{domain.OwnerDevice, domain.ClientDevice} {
		for _, state := range []BackupRestoreState{RestoreCompleted, RestoreRolledBack} {
			t.Run(string(actorType)+"/"+string(state), func(t *testing.T) {
				s, root, owner, input, _ := restoreFixture(t)
				clients := []domain.Principal{
					{Type: domain.ClientDevice, DeviceID: domain.NewID()},
					{Type: domain.ClientDevice, DeviceID: domain.NewID()},
				}
				_, err := s.Mutate(owner, domain.NewID(), "fixture.restore-clients", nil, func(tx *Tx) (any, error) {
					for _, client := range clients {
						if _, err := tx.Put(domain.DeviceKind, client.DeviceID, 0, "", "", domain.Device{Type: domain.ClientDevice, Name: "restore fixture"}); err != nil {
							return nil, err
						}
						digest := sha256.Sum256([]byte(client.DeviceID))
						if err := tx.PutCredential(client.DeviceID, digest[:]); err != nil {
							return nil, err
						}
					}
					return nil, nil
				})
				if err != nil {
					t.Fatal(err)
				}
				if actorType == domain.ClientDevice {
					input.Actor = clients[0]
				}
				actor := domain.WithPrincipal(context.Background(), input.Actor)
				input.ExpectedRevision, err = s.RestoreRevision(actor)
				if err != nil {
					t.Fatal(err)
				}
				request := domain.NewID()
				interrupted := errors.New("fixture stops before restore publication")
				_, _, err = s.restoreBackup(actor, request, input, func(boundary string) error {
					if state == RestoreRolledBack && boundary == "prepared" {
						return interrupted
					}
					return nil
				})
				if state == RestoreRolledBack && !errors.Is(err, interrupted) || state == RestoreCompleted && err != nil {
					t.Fatal(err)
				}
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
				for restart := 0; restart < 2; restart++ {
					reopened, err := Open(owner, root)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { reopened.Close() })
					receipt, err := reopened.GetBackupRestore(actor, request)
					if err != nil || receipt.State != state || receipt.Input.Actor != input.Actor {
						t.Fatal("original actor lost its retained receipt", receipt, err)
					}
					journal, err := os.ReadFile(restoreJournal(root, request))
					if err != nil {
						t.Fatal(err)
					}
					principals := append([]domain.Principal{{Type: domain.OwnerDevice}}, clients...)
					for _, other := range principals {
						if other == input.Actor {
							continue
						}
						ctx := domain.WithPrincipal(context.Background(), other)
						if err := reopened.Read(ctx, func(tx *Tx) error { return tx.Authorize() }); err != nil {
							t.Fatal("fixture caller is not currently authorized", err)
						}
						denied, err := reopened.GetBackupRestore(ctx, request)
						if err != nil || denied.Input.Actor != input.Actor || denied.State != state {
							t.Fatal("authenticated receipt read lost original attribution", denied, err)
						}
					}
					if denied, err := reopened.GetBackupRestore(context.Background(), request); domain.SafeError(err).Code != domain.PermissionDenied || denied != (BackupRestore{}) {
						t.Fatal("missing principal read the restore receipt", denied, err)
					}
					after, err := os.ReadFile(restoreJournal(root, request))
					if err != nil || !bytes.Equal(journal, after) || reopened.RestoreFrozen() {
						t.Fatal("receipt observation changed retained restore authority", err)
					}
					if restart == 1 && actorType == domain.ClientDevice {
						_, err := reopened.Mutate(owner, domain.NewID(), "fixture.revoke-restore-client", nil, func(tx *Tx) (any, error) {
							record, err := tx.Get(domain.DeviceKind, input.Actor.DeviceID)
							if err != nil {
								return nil, err
							}
							device, err := Decode[domain.Device](record)
							if err != nil {
								return nil, err
							}
							device.Revoked = true
							return tx.Put(record.Kind, record.ID, record.Revision, "", "", device)
						})
						if err != nil {
							t.Fatal(err)
						}
						if denied, err := reopened.GetBackupRestore(actor, request); domain.SafeError(err).Code != domain.Unauthenticated || denied != (BackupRestore{}) {
							t.Fatal("revoked original actor read the restore receipt", denied, err)
						}
					}
					if err := reopened.Close(); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}
