// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func serverQuotaDisplayNameBundle(t *testing.T, source, name string) []byte {
	t.Helper()
	var bundle map[string]any
	if err := json.Unmarshal(subscriptionTestBundle("quota-name-account", "first", time.Now().UTC()), &bundle); err != nil {
		t.Fatal(err)
	}
	tokens := bundle["tokens"].(map[string]any)
	parts := strings.Split(tokens["id_token"].(string), ".")
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims map[string]any
	if err = json.Unmarshal(payload, &claims); err != nil {
		t.Fatal(err)
	}
	if source == "name" {
		claims["name"] = name
	} else {
		claims["https://api.openai.com/profile"] = map[string]string{"name": name}
	}
	payload, _ = json.Marshal(claims)
	parts[1] = base64.RawURLEncoding.EncodeToString(payload)
	tokens["id_token"] = strings.Join(parts, ".")
	raw, _ := json.Marshal(bundle)
	return raw
}

func TestServerQuotaDisplayNameRejectsOriginalAndAllEncodedNames(t *testing.T) {
	for _, source := range []string{"name", "profile"} {
		for _, name := range []string{"Al", "࠿"} {
			f, n := newServerQuotaFixtureAccount(t, "quota-name-account", serverQuotaDisplayNameBundle(t, source, name))
			ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
			// First retain a known good observation; subsequent rejected values must not
			// refresh timestamps, quota or credential/cleanup authority.
			if _, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, requestServerQuota(f))); err != nil {
				t.Fatal(err)
			}
			f.service.runServerQuota(ctx, f.input.AccountID)
			_, original := f.record()
			forms := []string{name}
			for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
				forms = append(forms, encoding.EncodeToString([]byte(name)))
			}
			for _, form := range forms {
				for _, target := range []string{"window", "credit"} {
					n.observed = domain.SubscriptionQuotaObservation{ObservedAt: time.Now().UTC()}
					if target == "window" {
						remaining := 1.0
						n.observed.Windows = []domain.SubscriptionQuotaWindow{{ID: form + ":primary", Remaining: &remaining}}
					} else {
						credits := []domain.SubscriptionResetCreditDetail{{ID: form}}
						n.observed.Credits = &domain.SubscriptionResetCredits{Credits: &credits}
					}
					var logs bytes.Buffer
					f.service.logger = slog.New(slog.NewJSONHandler(&logs, nil))
					if _, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, requestServerQuota(f))); err != nil {
						t.Fatal(err)
					}
					f.service.runServerQuota(ctx, f.input.AccountID)
					_, after := f.record()
					if after.Subscription.ServerQuota.Phase != domain.SubscriptionObservationFailed || !after.Subscription.ServerQuota.CleanupConfirmed || after.Subscription.RecoveryRequired || after.Health != original.Health || !reflect.DeepEqual(original.Quota, after.Quota) || !reflect.DeepEqual(original.Subscription.QuotaObservedAt, after.Subscription.QuotaObservedAt) || after.ConfirmedExhausted != original.ConfirmedExhausted {
						t.Fatal("protected display name changed original publication or authority", source, target)
					}
					if strings.Contains(logs.String(), form+":primary") {
						t.Fatal("quota identifier leaked to logs")
					}
					reads := n.reads.Load()
					f.service.runServerQuota(ctx, f.input.AccountID)
					if n.reads.Load() != reads {
						t.Fatal("terminal reflection resubmitted native read")
					}
				}
			}
			remaining := 0.5
			n.observed = domain.SubscriptionQuotaObservation{ObservedAt: time.Now().UTC(), Windows: []domain.SubscriptionQuotaWindow{{ID: "ordinary-" + base64.RawURLEncoding.EncodeToString([]byte(name)) + "-quota:primary", Remaining: &remaining}}}
			if _, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, requestServerQuota(f))); err != nil {
				t.Fatal(err)
			}
			f.service.runServerQuota(ctx, f.input.AccountID)
			_, allowed := f.record()
			if allowed.Subscription.ServerQuota.Phase != domain.SubscriptionObservationSucceeded {
				t.Fatal("unrelated short name substring rejected")
			}
		}
	}
}

func TestServerQuotaDisplayNameAbsentRetainsExistingScope(t *testing.T) {
	f, n := newServerQuotaFixtureAccount(t, "quota-name-account", serverQuotaDisplayNameBundle(t, "name", ""))
	n.observed.Windows[0].ID = "Al:primary"
	if _, err := f.client.RequestSubscriptionObservation(context.Background(), subscriptionRequest(f.service.Identity.Token, requestServerQuota(f))); err != nil {
		t.Fatal(err)
	}
	f.service.runServerQuota(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), f.input.AccountID)
	_, after := f.record()
	if after.Subscription.ServerQuota.Phase != domain.SubscriptionObservationSucceeded || !after.Subscription.ServerQuota.CleanupConfirmed {
		t.Fatal("absent name altered quota authority")
	}
}
