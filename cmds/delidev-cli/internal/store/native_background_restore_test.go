// SPDX-License-Identifier: Apache-2.0
package store

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"testing"
)

func TestNativeBackgroundRestoreQuarantinesOriginalPendingReceipts(t *testing.T) {
	for _, state := range []domain.NativeBackgroundState{domain.NativeBackgroundQueued, domain.NativeBackgroundClaimed} {
		t.Run(string(state), func(t *testing.T) {
			s, root, ctx, input, _ := restoreFixture(t)
			session, job, execution, thread, action := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
			a := domain.NativeBackgroundAction{Version: 1, ActionType: domain.NativeBackgroundActionType, ActionID: action, SessionID: session, JobID: job, ExecutionID: execution, NativeThreadID: domain.NativeIdentity(thread), InventorySequence: 1, Operation: domain.NativeBackgroundList, Targets: []domain.NativeBackgroundTerminal{}, Actor: domain.Principal{Type: domain.OwnerDevice}, State: state}
			if state == domain.NativeBackgroundClaimed {
				a.Claim = &domain.NativeShellClaim{RequestID: domain.NewID(), MachineID: domain.NewID(), InstanceID: domain.NewID(), DeviceID: domain.NewID()}
			}
			if a.Validate() != nil {
				t.Fatal("invalid original fixture")
			}
			original := domain.Session{NativeBackground: &domain.NativeBackgroundInventory{Version: 1, JobID: job, ExecutionID: execution, NativeThreadID: domain.NativeIdentity(thread), Sequence: 1, Terminals: []domain.NativeBackgroundTerminal{}, PendingActionID: action, NativeEmpty: true}}
			_, err := s.Mutate(ctx, domain.NewID(), "fixture.background-restore", nil, func(tx *Tx) (any, error) {
				if _, err := tx.Put(domain.SessionKind, session, 0, session, "", original); err != nil {
					return nil, err
				}
				return tx.Put(domain.NativeActionKind, action, 0, session, "", a)
			})
			if err != nil {
				t.Fatal(err)
			}
			name, err := s.Backup(ctx)
			if err != nil {
				t.Fatal(err)
			}
			observed, err := s.InspectBackup(ctx, name, input.ServerID)
			if err != nil {
				t.Fatal(err)
			}
			input.Backup, input.SHA256 = observed.Backup, observed.SHA256
			input.ExpectedRevision, err = s.RestoreRevision(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err = s.RestoreBackup(ctx, domain.NewID(), input); err != nil {
				t.Fatal(err)
			}
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err = Open(ctx, root)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			row, err := s.Get(ctx, domain.NativeActionKind, action)
			if err != nil {
				t.Fatal(err)
			}
			retained, err := Decode[domain.NativeBackgroundAction](row)
			if err != nil || retained.Validate() != nil {
				t.Fatal("invalid retained receipt", err)
			}
			if state == domain.NativeBackgroundQueued && retained.State != domain.NativeBackgroundCanceled || state == domain.NativeBackgroundClaimed && (retained.State != domain.NativeBackgroundUncertain || retained.Observation.Acknowledged || retained.Observation.Delivery != domain.NativeBackgroundSendUnknown) {
				t.Fatal("restore reopened original send authority")
			}
			row, err = s.Get(ctx, domain.SessionKind, session)
			if err != nil {
				t.Fatal(err)
			}
			restored, err := Decode[domain.Session](row)
			if err != nil || !restored.NativeBackground.OwnerLost {
				t.Fatal("restore adopted original inventory", err)
			}
		})
	}
}
