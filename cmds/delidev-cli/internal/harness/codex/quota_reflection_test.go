// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
)

func shortQuotaBundle(t *testing.T) []byte {
	t.Helper()
	claims, _ := json.Marshal(map[string]any{"email": "fixture@example.invalid", "https://api.openai.com/auth": map[string]string{"chatgpt_account_id": "acct", "chatgpt_user_id": "user", "chatgpt_plan_type": "plus"}})
	token := "header." + base64.RawURLEncoding.EncodeToString(claims) + ".synthetic"
	raw, _ := json.Marshal(map[string]any{"auth_mode": "chatgpt", "OPENAI_API_KEY": nil, "tokens": map[string]string{"id_token": token, "access_token": token, "refresh_token": "tok", "account_id": "acct"}, "last_refresh": time.Now().UTC()})
	if _, _, err := subscription.Parse(raw); err != nil {
		t.Fatal("valid short identity fixture rejected", err)
	}
	return raw
}

func TestQuotaReflectionRejectsExactShortOriginalAndEncodedIDs(t *testing.T) {
	home := t.TempDir()
	if err := security.WriteAtomic(filepath.Join(home, "auth.json"), shortQuotaBundle(t)); err != nil {
		t.Fatal(err)
	}
	client := &Client{managedHome: home}
	for _, secret := range []string{"acct", "user", "tok"} {
		forms := []string{secret}
		for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
			forms = append(forms, encoding.EncodeToString([]byte(secret)))
		}
		for _, form := range forms {
			for _, suffix := range []string{":primary", ":secondary", ":spend", ""} {
				remaining := 0.5
				value := domain.SubscriptionQuotaObservation{ObservedAt: time.Now().UTC(), Windows: []domain.SubscriptionQuotaWindow{{ID: form + suffix, Remaining: &remaining}}}
				if suffix == "" {
					credits := []domain.SubscriptionResetCreditDetail{{ID: form}}
					value.Windows = nil
					value.Credits = &domain.SubscriptionResetCredits{Credits: &credits}
				}
				if err := client.validateQuotaReflection(value); err == nil {
					t.Fatal("protected exact quota or credit ID accepted")
				}
			}
		}
	}
	for _, id := range []string{"codex:primary", "user-facing:secondary", "token-allowance:spend", "acct-extra:primary", "acct:primary:secondary"} {
		value := domain.SubscriptionQuotaObservation{Windows: []domain.SubscriptionQuotaWindow{{ID: id}}}
		if err := client.validateQuotaReflection(value); err != nil {
			t.Fatal("incidental short word or non-original suffix rejected", err)
		}
	}
	for _, form := range []string{"protected-long-value", base64.RawURLEncoding.EncodeToString([]byte("protected-long-value"))} {
		if err := ValidateQuotaSecrets(domain.SubscriptionQuotaObservation{Windows: []domain.SubscriptionQuotaWindow{{ID: "prefix-" + form + ":primary"}}}, "protected-long-value"); err == nil {
			t.Fatal("existing long substring reflection accepted")
		}
	}
}

func TestNativeQuotaShortReflectionRejectsReadsAndRollingUpdates(t *testing.T) {
	for _, key := range []string{"acct", "YWNjdA", "user", "dXNlcg", "tok", "dG9r"} {
		t.Run(key, func(t *testing.T) {
			config := fixtureConfig(t, "managed-ready")
			config.Mode, config.ManagedAuthentication = SubscriptionProtocol, true
			response := strings.Replace(quotaResponseBase, `"primary":`, `"limitId":"`+key+`","primary":`, 1)
			config.Process.Env = append(config.Process.Env, "DELIDEV_CODEX_QUOTA_READ="+response, "DELIDEV_CODEX_QUOTA_UPDATES="+key)
			var logs bytes.Buffer
			config.Process.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
			last := domain.SubscriptionQuotaObservation{ObservedAt: time.Now().UTC().Add(-time.Minute), Windows: []domain.SubscriptionQuotaWindow{{ID: "last-good"}}}
			original := last
			publications := 0
			config.QuotaObserver = func(_ context.Context, value domain.SubscriptionQuotaObservation) { last = value; publications++ }
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			native, err := Open(ctx, config)
			if err != nil {
				t.Fatal(err)
			}
			defer native.Close()
			progress, err := native.StartManagedLogin(ctx, true)
			if err != nil {
				t.Fatal(err)
			}
			if err = native.WaitManagedLogin(ctx, progress.LoginID); err != nil {
				t.Fatal(err)
			}
			if err = security.WriteAtomic(filepath.Join(config.Home, "auth.json"), shortQuotaBundle(t)); err != nil {
				t.Fatal(err)
			}
			observed, err := native.ReadManagedQuota(ctx, domain.NewID())
			if err == nil || len(observed.Windows) != 0 {
				t.Fatal("explicit read published reflected quota", err)
			}
			// This private lifecycle client has no execution state. Drain only its
			// original login completion, then require the fixture end marker after
			// the rolling quota notification was consumed by NextEvent.
			for {
				event, err := native.NextEvent(ctx)
				if err != nil || event.Native == nil {
					t.Fatal("rolling fixture omitted private notification", err)
				}
				if event.Native.Method == "warning" {
					break
				}
				if event.Native.Method != "account/login/completed" {
					t.Fatal("unexpected fixture event before quota marker")
				}
			}
			if publications != 0 || !reflect.DeepEqual(last, original) {
				t.Fatal("rejected rolling update replaced last good quota")
			}
			if strings.Contains(logs.String(), key+":primary") || !strings.Contains(logs.String(), "subscription_native_quota_rejected") {
				t.Fatal("private quota leaked or safe rejection diagnostic absent")
			}
		})
	}
}
