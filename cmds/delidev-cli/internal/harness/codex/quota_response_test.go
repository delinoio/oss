// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const quotaResponseBase = `{"rateLimits":{"primary":{"usedPercent":80,"windowDurationMins":300,"resetsAt":1900000000}},"rateLimitsByLimitId":null,"rateLimitResetCredits":{"availableCount":2,"credits":[{"id":"credit_1","resetType":"codexRateLimits","status":"available","grantedAt":1700000000,"expiresAt":null,"title":null,"description":null}]}}`

func quotaResponseWith(fields string) string {
	if fields == "" {
		return quotaResponseBase
	}
	return strings.TrimSuffix(quotaResponseBase, "}") + "," + fields + "}"
}

func TestManagedNativeQuotaResponseObservationsAreDiscarded(t *testing.T) {
	for name, fields := range map[string]string{
		"legacy":            "",
		"ordinary-null":     `"ordinaryUsageAllowed":null`,
		"ordinary-false":    `"ordinaryUsageAllowed":false`,
		"ordinary-true":     `"ordinaryUsageAllowed":true`,
		"account-null":      `"accountId":null`,
		"account-populated": `"accountId":"private-quota-account"`,
		"upsell-null":       `"rateLimitUpsell":null`,
		"upsell-populated":  `"rateLimitUpsell":{"private_upsell":{"nested_key":["private-quota-upsell",true,null]}}`,
		"all-null":          `"ordinaryUsageAllowed":null,"accountId":null,"rateLimitUpsell":null`,
		"all-populated":     `"ordinaryUsageAllowed":true,"accountId":"private-quota-account","rateLimitUpsell":{"private_upsell":{"nested_key":"private-quota-upsell"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			config := fixtureConfig(t, "managed-ready")
			config.Mode, config.ManagedAuthentication = SubscriptionProtocol, true
			config.Process.Env = append(config.Process.Env, "DELIDEV_CODEX_QUOTA_READ="+quotaResponseWith(fields))
			var logs bytes.Buffer
			config.Process.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			native, err := Open(ctx, config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := native.Close(); err != nil {
					t.Error("original native cleanup failed", err)
				}
			})
			progress, err := native.StartManagedLogin(ctx, true)
			if err != nil {
				t.Fatal(err)
			}
			if err := native.WaitManagedLogin(ctx, progress.LoginID); err != nil {
				t.Fatal(err)
			}
			observed, err := native.ReadManagedQuota(ctx, domain.NewID())
			if err != nil {
				t.Fatal("official quota observations rejected", err)
			}
			if len(observed.Windows) != 1 || observed.Windows[0].Remaining == nil || *observed.Windows[0].Remaining != 0.2 || observed.Credits == nil || observed.Credits.AvailableCount != 2 || observed.Credits.Credits == nil || len(*observed.Credits.Credits) != 1 {
				t.Fatal("authoritative quota/credit values changed", observed)
			}
			encoded, err := json.Marshal(observed)
			if err != nil {
				t.Fatal(err)
			}
			for _, private := range []string{"ordinaryUsageAllowed", "accountId", "rateLimitUpsell", "private-quota-account", "private-quota-upsell", "nested_key"} {
				if bytes.Contains(encoded, []byte(private)) || strings.Contains(logs.String(), private) {
					t.Fatal("discarded private observation escaped", private)
				}
			}
		})
	}
}

func TestPinnedNativeQuotaResponseStrictEnvelope(t *testing.T) {
	for name, raw := range map[string]string{
		"ordinary-type":    quotaResponseWith(`"ordinaryUsageAllowed":"true"`),
		"account-type":     quotaResponseWith(`"accountId":123`),
		"unknown":          quotaResponseWith(`"unrelatedField":null`),
		"duplicate-root":   quotaResponseWith(`"accountId":null,"accountId":"other"`),
		"duplicate-opaque": quotaResponseWith(`"rateLimitUpsell":{"nested_key":0,"nested_key":1}`),
		"oversized-opaque": quotaResponseWith(`"rateLimitUpsell":"` + strings.Repeat("x", 1<<20) + `"`),
	} {
		t.Run(name, func(t *testing.T) {
			var native nativeQuotaRead
			if err := domain.Decode([]byte(raw), &native); err == nil {
				t.Fatal("invalid private response accepted")
			}
		})
	}
}

func TestPinnedNativeQuotaResponseInventoryAbsenceAndZero(t *testing.T) {
	for name, inventory := range map[string]string{
		"missing":    "",
		"null":       `,"rateLimitResetCredits":null`,
		"zero":       `,"rateLimitResetCredits":{"availableCount":0,"credits":[]}`,
		"count-only": `,"rateLimitResetCredits":{"availableCount":2,"credits":null}`,
	} {
		t.Run(name, func(t *testing.T) {
			raw := `{"rateLimits":{"primary":{"usedPercent":80}},"ordinaryUsageAllowed":false,"accountId":"private-quota-account","rateLimitUpsell":null` + inventory + `}`
			var native nativeQuotaRead
			if err := domain.Decode([]byte(raw), &native); err != nil {
				t.Fatal(err)
			}
			observed, err := projectQuota(native, domain.NewID(), time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			switch name {
			case "missing", "null":
				if observed.Credits != nil {
					t.Fatal("unknown inventory became observed")
				}
			case "zero":
				if observed.Credits == nil || observed.Credits.AvailableCount != 0 || observed.Credits.Credits == nil || len(*observed.Credits.Credits) != 0 {
					t.Fatal("observed zero lost")
				}
			case "count-only":
				if observed.Credits == nil || observed.Credits.AvailableCount != 2 || observed.Credits.Credits != nil {
					t.Fatal("count-only inventory changed")
				}
			}
		})
	}
}
