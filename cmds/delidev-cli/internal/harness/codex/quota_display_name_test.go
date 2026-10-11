// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
)

func quotaDisplayNameBundle(t *testing.T, name string, profile bool) []byte {
	t.Helper()
	var bundle subscription.Bundle
	if err := json.Unmarshal(shortQuotaBundle(t), &bundle); err != nil {
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
	// Only the ID token owns the parsed display name; an access-token name is
	// not required for the original protected identity to reject reflection.
	parts[1] = base64.RawURLEncoding.EncodeToString(payload)
	bundle.Tokens.ID = strings.Join(parts, ".")
	raw, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	_, identity, err := subscription.Parse(raw)
	if err != nil || identity.DisplayName != name {
		t.Fatal("invalid named quota fixture", err)
	}
	return raw
}

func TestQuotaDisplayNameRejectsProtectedIdentifiers(t *testing.T) {
	for _, profile := range []bool{false, true} {
		for _, name := range []string{"Q", "Example", "+/名", "available", "unlimited", "codexRateLimits"} {
			home := t.TempDir()
			if err := security.WriteAtomic(filepath.Join(home, "auth.json"), quotaDisplayNameBundle(t, name, profile)); err != nil {
				t.Fatal(err)
			}
			client := &Client{managedHome: home}
			forms := []string{name}
			for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
				forms = append(forms, encoding.EncodeToString([]byte(name)))
			}
			for _, form := range forms {
				for _, suffix := range []string{"", ":primary", ":secondary", ":spend"} {
					if err := client.validateQuotaReflection(domain.SubscriptionQuotaObservation{Windows: []domain.SubscriptionQuotaWindow{{ID: form + suffix}}}); err == nil {
						t.Fatal("protected display name accepted in quota window")
					}
				}
				credits := []domain.SubscriptionResetCreditDetail{{ID: form}}
				for _, value := range []domain.SubscriptionQuotaObservation{
					{Credits: &domain.SubscriptionResetCredits{Credits: &credits}},
					{PaidCredits: []domain.SubscriptionPaidCreditBucket{{ID: form}}},
				} {
					if err := client.validateQuotaReflection(value); err == nil {
						t.Fatal("protected display name accepted in credit identifier")
					}
				}
				if err := client.validateQuotaReflection(domain.SubscriptionQuotaObservation{Windows: []domain.SubscriptionQuotaWindow{{ID: "prefix-" + form + "-suffix:primary"}}}); err != nil {
					t.Fatal("unrelated short-name substring rejected", err)
				}
			}
		}
	}
}

func TestQuotaDisplayNameAllowsFixedQuotaMetadata(t *testing.T) {
	for _, name := range []string{"available", "unlimited", "codexRateLimits"} {
		home := t.TempDir()
		if err := security.WriteAtomic(filepath.Join(home, "auth.json"), quotaDisplayNameBundle(t, name, false)); err != nil {
			t.Fatal(err)
		}
		client := &Client{managedHome: home}
		unlimited := true
		credits := []domain.SubscriptionResetCreditDetail{{ID: "credit_1", ResetType: domain.CodexRateLimitsReset, Status: domain.SubscriptionCreditAvailable}}
		value := domain.SubscriptionQuotaObservation{
			Credits:     &domain.SubscriptionResetCredits{Credits: &credits},
			PaidCredits: []domain.SubscriptionPaidCreditBucket{{ID: "codex", Unlimited: &unlimited}},
		}
		if err := client.validateQuotaReflection(value); err != nil {
			t.Fatalf("fixed quota metadata %q was mistaken for a reflected display name: %v", name, err)
		}
	}
}

func TestQuotaDisplayNameChecksOriginalIdentifiersOnly(t *testing.T) {
	t.Run("projected window suffix", func(t *testing.T) {
		home := t.TempDir()
		if err := security.WriteAtomic(filepath.Join(home, "auth.json"), quotaDisplayNameBundle(t, "codex:primary", false)); err != nil {
			t.Fatal(err)
		}
		client := &Client{managedHome: home}
		value := domain.SubscriptionQuotaObservation{Windows: []domain.SubscriptionQuotaWindow{{ID: "codex:primary"}}}
		if err := client.validateQuotaReflection(value); err != nil {
			t.Fatalf("generated window suffix was mistaken for an original bucket ID: %v", err)
		}
	})

	t.Run("paid balance", func(t *testing.T) {
		home := t.TempDir()
		if err := security.WriteAtomic(filepath.Join(home, "auth.json"), quotaDisplayNameBundle(t, "0", false)); err != nil {
			t.Fatal(err)
		}
		client := &Client{managedHome: home}
		balance := "0"
		value := domain.SubscriptionQuotaObservation{PaidCredits: []domain.SubscriptionPaidCreditBucket{{ID: "paid", Balance: &balance}}}
		if err := client.validateQuotaReflection(value); err != nil {
			t.Fatalf("paid balance was mistaken for an identifier: %v", err)
		}
	})

	t.Run("credential balance remains protected", func(t *testing.T) {
		balance := "token-value"
		value := domain.SubscriptionQuotaObservation{PaidCredits: []domain.SubscriptionPaidCreditBucket{{ID: "paid", Balance: &balance}}}
		if err := ValidateQuotaSecrets(value, "token-value"); err == nil {
			t.Fatal("credential reflection in a paid balance was accepted")
		}
	})
}

func TestQuotaDisplayNameAbsentKeepsUnrelatedIdentifiers(t *testing.T) {
	home := t.TempDir()
	if err := security.WriteAtomic(filepath.Join(home, "auth.json"), quotaDisplayNameBundle(t, "", false)); err != nil {
		t.Fatal(err)
	}
	client := &Client{managedHome: home}
	if err := client.validateQuotaReflection(domain.SubscriptionQuotaObservation{Windows: []domain.SubscriptionQuotaWindow{{ID: "Example:primary"}}}); err != nil {
		t.Fatal("missing display name rejected unrelated quota", err)
	}
}
