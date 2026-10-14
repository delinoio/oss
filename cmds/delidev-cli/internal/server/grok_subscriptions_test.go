// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
)

func grokServerBundle(t *testing.T, principal string) subscription.GrokBundle {
	t.Helper()
	raw, err := json.Marshal(map[string]any{subscription.GrokScopeKey: map[string]any{
		"key": "synthetic-key", "auth_mode": "oidc", "create_time": "2026-10-14T00:00:00Z", "expires_at": "2026-10-14T06:00:00Z",
		"user_id": "synthetic-user", "principal_type": "user", "principal_id": principal, "refresh_token": "synthetic-refresh",
		"oidc_issuer": subscription.GrokIssuer, "oidc_client_id": subscription.GrokClientID,
	}})
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := subscription.ParseGrokAuthFile(raw)
	if err != nil {
		t.Fatal(err)
	}
	return bundle
}

func TestGrokIdentityCommitmentRejectsDuplicateOwnershipPerPrincipal(t *testing.T) {
	f := newSubscriptionFixture(t)
	first, same, other := grokServerBundle(t, "synthetic-principal-1"), grokServerBundle(t, "synthetic-principal-1"), grokServerBundle(t, "synthetic-principal-2")
	commitment := f.service.grokIdentityCommitment(first)
	if !domain.NativeIdentityCommitmentValid(commitment) || commitment == first.IdentityKey() {
		t.Fatal("commitment must be a keyed 32-byte hex value distinct from the bundle key")
	}
	if f.service.grokIdentityCommitment(same) != commitment || f.service.grokIdentityCommitment(other) == commitment || f.service.grokIdentityCommitment(subscription.GrokBundle{}) != "" {
		t.Fatal("commitment must follow the identity key")
	}
	owner := domain.NewID()
	_, account := f.record()
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.grok.owner", nil, func(tx *store.Tx) (any, error) {
		account.SubscriptionService = domain.SubscriptionGrok
		account.Subscription = &domain.SubscriptionState{IdentityCommitment: commitment}
		return tx.Put(domain.AccountKind, owner, 0, "", "", account)
	})
	if err != nil {
		t.Fatal(err)
	}
	err = f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		candidate := domain.NewID()
		if err := uniqueSubscriptionIdentity(tx, candidate, f.service.grokIdentityCommitment(same)); err == nil || domain.SafeError(err).Code != domain.Conflict {
			t.Error("second account with the same Grok identity was not rejected")
		} else if strings.Contains(err.Error(), "synthetic") {
			t.Error("duplicate rejection echoed a bundle value")
		}
		if err := uniqueSubscriptionIdentity(tx, candidate, f.service.grokIdentityCommitment(other)); err != nil {
			t.Error("a different principal for the same user was rejected")
		}
		if err := uniqueSubscriptionIdentity(tx, owner, commitment); err != nil {
			t.Error("the owner was rejected by its own identity")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCodexSubscriptionPathNeverServesGrokAccount(t *testing.T) {
	f := newSubscriptionFixture(t)
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.grok.service", nil, func(tx *store.Tx) (any, error) {
		r, a, err := accountFromTx(tx, f.input.AccountID, 0)
		if err != nil {
			return nil, err
		}
		a.SubscriptionService = domain.SubscriptionGrok
		return tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
	})
	if err != nil {
		t.Fatal(err)
	}
	err = f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		if _, _, err := subscriptionAccount(tx, f.input.AccountID, 0); err == nil || domain.SafeError(err).Code != domain.Unsupported {
			t.Error("the Codex lifecycle accepted a Grok account")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
