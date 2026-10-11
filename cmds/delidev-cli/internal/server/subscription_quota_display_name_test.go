// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
)

func serverQuotaDisplayNameBundle(t *testing.T, name string, profile bool) []byte {
	t.Helper()
	var bundle subscription.Bundle
	if err := json.Unmarshal(subscriptionTestBundle("quota-server-account", "named", time.Now().UTC()), &bundle); err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(bundle.Tokens.ID, ".")
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims map[string]any
	if err = json.Unmarshal(payload, &claims); err != nil {
		t.Fatal(err)
	}
	if profile {
		claims["https://api.openai.com/profile"] = map[string]string{"name": name}
	} else {
		claims["name"] = name
	}
	payload, err = json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	parts[1] = base64.RawURLEncoding.EncodeToString(payload)
	bundle.Tokens.ID = strings.Join(parts, ".")
	raw, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	_, identity, err := subscription.Parse(raw)
	if err != nil || identity.DisplayName != name {
		t.Fatal("invalid server quota name fixture", err)
	}
	return raw
}

func TestServerQuotaDisplayNameRejectedBeforePublication(t *testing.T) {
	for _, profile := range []bool{false, true} {
		f, native := newServerQuotaFixtureBundle(t, serverQuotaDisplayNameBundle(t, "Q?", profile))
		ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
		forms := []string{"Q?"}
		for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
			forms = append(forms, encoding.EncodeToString([]byte("Q?")))
		}
		for _, form := range forms {
			for _, credit := range []bool{false, true} {
				// Keep one accepted projection to distinguish reflection rejection
				// from accidentally publishing an empty replacement on failure.
				remaining := 0.4
				native.observed = domain.SubscriptionQuotaObservation{ObservedAt: time.Now().UTC(), Windows: []domain.SubscriptionQuotaWindow{{ID: "baseline:primary", Remaining: &remaining}}}
				if _, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, requestServerQuota(f))); err != nil {
					t.Fatal(err)
				}
				f.service.runServerQuota(ctx, f.input.AccountID)
				_, before := f.record()
				if before.Subscription.ServerQuota.Phase != domain.SubscriptionObservationSucceeded || len(before.Quota) != 1 {
					t.Fatal("baseline quota was not accepted")
				}
				native.observed = domain.SubscriptionQuotaObservation{ObservedAt: time.Now().UTC(), Windows: []domain.SubscriptionQuotaWindow{{ID: form + ":primary", Remaining: &remaining}}}
				if credit {
					details := []domain.SubscriptionResetCreditDetail{{ID: form, ResetType: domain.CodexRateLimitsReset, Status: domain.SubscriptionCreditAvailable, GrantedAt: time.Now().UTC()}}
					native.observed.Windows = nil
					native.observed.Credits = &domain.SubscriptionResetCredits{ObservationID: domain.NewID(), ObservedAt: time.Now().UTC(), AvailableCount: 1, Credits: &details}
				}
				request := requestServerQuota(f)
				if _, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, request)); err != nil {
					t.Fatal(err)
				}
				f.service.runServerQuota(ctx, f.input.AccountID)
				_, after := f.record()
				// Some encoded forms also violate the public identifier grammar.
				// Require the protected guard's code, not that later rejection.
				if after.Subscription.ServerQuota.ErrorCode != domain.RecoveryRequired || after.Subscription.ServerQuota.Phase != domain.SubscriptionObservationFailed || !after.Subscription.ServerQuota.CleanupConfirmed || after.Subscription.RecoveryRequired || after.Health != before.Health {
					t.Fatal("reflection changed cleanup or account authority")
				}
				if !reflect.DeepEqual(after.Quota, before.Quota) || !reflect.DeepEqual(after.Subscription.QuotaObservedAt, before.Subscription.QuotaObservedAt) || !reflect.DeepEqual(after.Subscription.ResetCredits, before.Subscription.ResetCredits) || after.ConfirmedExhausted != before.ConfirmedExhausted {
					t.Fatal("display-name reflection replaced accepted quota data")
				}
				reads := native.reads.Load()
				if _, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, request)); err != nil {
					t.Fatal(err)
				}
				f.service.runServerQuota(ctx, f.input.AccountID)
				if native.reads.Load() != reads {
					t.Fatal("terminal rejected observation was sent again")
				}
			}
		}
	}
}

func TestServerQuotaDisplayNameAllowsUnrelatedSubstringAndAbsence(t *testing.T) {
	for _, name := range []string{"Ada", ""} {
		f, native := newServerQuotaFixtureBundle(t, serverQuotaDisplayNameBundle(t, name, false))
		remaining := 0.5
		native.observed = domain.SubscriptionQuotaObservation{ObservedAt: time.Now().UTC(), Windows: []domain.SubscriptionQuotaWindow{{ID: "prefix-Ada-suffix:primary", Remaining: &remaining}}}
		if _, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, requestServerQuota(f))); err != nil {
			t.Fatal(err)
		}
		f.service.runServerQuota(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), f.input.AccountID)
		_, account := f.record()
		if account.Subscription.ServerQuota.Phase != domain.SubscriptionObservationSucceeded || len(account.Quota) != 1 {
			t.Fatal("unrelated short substring or absent name was rejected")
		}
	}
}
