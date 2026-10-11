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

func quotaDisplayNameBundle(t *testing.T, source, name string) []byte {
	t.Helper()
	var bundle map[string]any
	if err := json.Unmarshal(shortQuotaBundle(t), &bundle); err != nil {
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
	delete(claims, "name")
	if source == "name" {
		claims["name"] = name
	} else {
		claims["https://api.openai.com/profile"] = map[string]string{"name": name}
	}
	payload, _ = json.Marshal(claims)
	parts[1] = base64.RawURLEncoding.EncodeToString(payload)
	tokens["id_token"] = strings.Join(parts, ".")
	raw, _ := json.Marshal(bundle)
	_, identity, err := subscription.Parse(raw)
	if err != nil || identity.DisplayName != name {
		t.Fatal("synthetic original name failed", err)
	}
	return raw
}

func TestQuotaDisplayNameRejectsShortRawAndAllCompleteEncodings(t *testing.T) {
	for _, source := range []string{"name", "profile"} {
		for _, name := range []string{"J", "Al", "Mia", "Zoë", "࠿", "Seven77"} {
			home := t.TempDir()
			if err := security.WriteAtomic(filepath.Join(home, "auth.json"), quotaDisplayNameBundle(t, source, name)); err != nil {
				t.Fatal(err)
			}
			client := &Client{managedHome: home}
			forms := []string{name}
			for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
				forms = append(forms, encoding.EncodeToString([]byte(name)))
			}
			for _, form := range forms {
				for _, target := range []string{"primary", "secondary", "spend", "credit", "paid"} {
					value := domain.SubscriptionQuotaObservation{}
					switch target {
					case "credit":
						credits := []domain.SubscriptionResetCreditDetail{{ID: form}}
						value.Credits = &domain.SubscriptionResetCredits{Credits: &credits}
					case "paid":
						value.PaidCredits = []domain.SubscriptionPaidCreditBucket{{ID: form}}
					default:
						value.Windows = []domain.SubscriptionQuotaWindow{{ID: form + ":" + target}}
					}
					if err := client.validateQuotaReflection(value); err == nil {
						t.Fatal("protected name accepted", source, target)
					}
				}
			}
			for _, id := range []string{"ordinary-" + name + "-bucket:primary", name + ":primary:secondary", "codex:primary"} {
				if err := client.validateQuotaReflection(domain.SubscriptionQuotaObservation{Windows: []domain.SubscriptionQuotaWindow{{ID: id}}}); err != nil {
					t.Fatal("harmless short substring rejected", err)
				}
			}
		}
	}
}
func TestQuotaDisplayNameOmissionDoesNotInventExternalAuthority(t *testing.T) {
	home := t.TempDir()
	if err := security.WriteAtomic(filepath.Join(home, "auth.json"), quotaDisplayNameBundle(t, "name", "")); err != nil {
		t.Fatal(err)
	}
	value := domain.SubscriptionQuotaObservation{Windows: []domain.SubscriptionQuotaWindow{{ID: "Al:primary"}}}
	if err := (&Client{managedHome: home}).validateQuotaReflection(value); err != nil {
		t.Fatal("absent name widened reflection inputs", err)
	}
	if err := ValidateQuotaSecrets(value, "external-access-token", "external-account"); err != nil {
		t.Fatal("external-token-only lane invented display name", err)
	}
}
