// SPDX-License-Identifier: Apache-2.0
package store

import (
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestBackupRestoreRequiresSettledServerCreditOwnership(t *testing.T) {
	for _, phase := range []domain.SubscriptionObservationPhase{domain.SubscriptionObservationQueued, domain.SubscriptionObservationSending, domain.SubscriptionObservationUncertain} {
		t.Run(string(phase), func(t *testing.T) {
			s, _, ctx, in, _ := restoreFixture(t)
			op := domain.ServerCreditOperation{ID: domain.NewID(), Epoch: domain.NewID(), AttemptID: domain.NewID(), AttemptEpoch: domain.NewID(), FinishID: domain.NewID(), ConnectionID: domain.NewID(), Generation: domain.NewID(), Actor: domain.Principal{Type: domain.OwnerDevice}, Phase: phase, RequestedAt: time.Now().UTC(), NextCredit: true, CreditsObservationID: domain.NewID()}
			account := domain.Account{Alias: "Credit restore fixture", Type: domain.SubscriptionAccount, SubscriptionService: domain.SubscriptionChatGPT, Health: domain.AccountDisconnected, Subscription: &domain.SubscriptionState{ServerCredit: &op}}
			_, err := s.Mutate(ctx, domain.NewID(), "fixture.restore.credit", nil, func(tx *Tx) (any, error) { return tx.Put(domain.AccountKind, domain.NewID(), 0, "", "", account) })
			if err != nil {
				t.Fatal(err)
			}
			in.ExpectedRevision, err = s.RestoreRevision(ctx)
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = s.RestoreBackup(ctx, domain.NewID(), in)
			if domain.SafeError(err).Code != domain.RecoveryRequired || s.RestoreFrozen() {
				t.Fatal("restore replaced unsettled original credit", err)
			}
		})
	}
}
