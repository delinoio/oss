// SPDX-License-Identifier: Apache-2.0
package store

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"testing"
	"time"
)

func TestAutomaticCreditRestoreClearsConsentKeepsCleanupReferences(t *testing.T) {
	s, root, ctx, in, _ := restoreFixture(t)
	account, generation, connection := domain.NewID(), domain.NewID(), domain.NewID()
	episode := domain.NewID()
	now := time.Now().UTC()
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.automatic", nil, func(tx *Tx) (any, error) {
		_, e := tx.Put(domain.AccountKind, account, 0, "", "", domain.Account{Alias: "Fixture", Type: domain.SubscriptionAccount, SubscriptionService: domain.SubscriptionChatGPT, Enabled: true, Health: domain.AccountReady, Connection: &domain.AccountConnection{ID: connection, Authentication: domain.SubscriptionAuth, ConnectedAt: now}, Subscription: &domain.SubscriptionState{Generation: generation, OwnerMachineID: domain.NewID(), AutomaticCreditConsent: &domain.AutomaticResetCreditConsent{Actor: in.Actor, ConnectionID: connection, Generation: generation, ConfirmedAt: now}, AutomaticCreditEpisode: &domain.AutomaticResetCreditEpisode{ID: episode, Generation: generation, ExecutionID: domain.NewID(), NativeThreadID: domain.NativeIdentity(domain.NewID()), NativeTurnID: domain.NativeIdentity(domain.NewID()), Block: domain.CodexUsageLimitExceeded, ObservedAt: now}}})
		return nil, e
	})
	if err != nil {
		t.Fatal(err)
	}
	backup, err := s.Backup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := s.InspectBackup(ctx, backup, in.ServerID)
	if err != nil {
		t.Fatal(err)
	}
	in.Backup, in.SHA256 = inspection.Backup, inspection.SHA256
	in.ExpectedRevision, err = s.RestoreRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.RestoreBackup(ctx, domain.NewID(), in); err != nil {
		t.Fatal(err)
	}
	s.Close()
	restored, err := Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	r, err := restored.Get(ctx, domain.AccountKind, account)
	if err != nil {
		t.Fatal(err)
	}
	a, err := Decode[domain.Account](r)
	if err != nil {
		t.Fatal(err)
	}
	if a.Subscription == nil || a.Subscription.AutomaticCreditConsent != nil || a.Subscription.AutomaticCreditEpisode == nil || a.Subscription.AutomaticCreditEpisode.ID != episode || a.Subscription.Generation != generation || !a.Subscription.RecoveryRequired || a.Connection != nil {
		t.Fatal("restore retained consent or discarded cleanup ownership")
	}
}
