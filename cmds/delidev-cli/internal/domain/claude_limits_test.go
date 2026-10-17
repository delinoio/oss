// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

var claudeUsageNow = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

func TestParseClaudeUsageQuotaNormalizesRecognizedWindows(t *testing.T) {
	body := `{"five_hour":{"utilization":25,"resets_at":"2026-10-16T15:00:00.123456+00:00","extra":1},"seven_day":{"utilization":50.0,"resets_at":"2026-10-20T09:00:00+09:00"},"seven_day_opus":{"utilization":99},"extra_usage":{"is_enabled":false}}`
	quota, err := ParseClaudeUsageQuota([]byte(body), claudeUsageNow)
	if err != nil {
		t.Fatal(err)
	}
	if !quota.ObservedAt.Equal(claudeUsageNow) || len(quota.Windows) != 2 {
		t.Fatalf("unexpected quota %+v", quota)
	}
	five, seven := quota.Windows[0], quota.Windows[1]
	if five.ID != ClaudeUsageFiveHour || five.Remaining != 0.75 || five.ResetAt == nil || !five.ResetAt.Equal(time.Date(2026, 10, 16, 15, 0, 0, 123456000, time.UTC)) {
		t.Fatalf("five-hour window %+v", five)
	}
	if seven.ID != ClaudeUsageSevenDay || seven.Remaining != 0.5 || seven.ResetAt == nil || !seven.ResetAt.Equal(time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)) || seven.ResetAt.Location() != time.UTC {
		t.Fatalf("seven-day window %+v", seven)
	}
}

func TestParseClaudeUsageQuotaBoundaryUtilization(t *testing.T) {
	for _, tc := range []struct {
		utilization string
		remaining   float64
	}{{"0", 1}, {"-0", 1}, {"100", 0}, {"100.0", 0}, {"1e2", 0}, {"2.5e1", 0.75}} {
		quota, err := ParseClaudeUsageQuota([]byte(`{"five_hour":{"utilization":`+tc.utilization+`}}`), claudeUsageNow)
		if err != nil || quota.Windows[0].Remaining != tc.remaining || quota.Windows[0].ResetAt != nil {
			t.Fatalf("utilization %s: %+v %v", tc.utilization, quota, err)
		}
	}
	// A value that is not exactly 100 must never read as exactly zero remaining.
	quota, err := ParseClaudeUsageQuota([]byte(`{"five_hour":{"utilization":99.999999999999999999999999}}`), claudeUsageNow)
	if err != nil || quota.Windows[0].Remaining <= 0 {
		t.Fatalf("near-exhausted window rounded to zero: %+v %v", quota, err)
	}
	quota, err = ParseClaudeUsageQuota([]byte(`{"five_hour":{"utilization":0.000000000000000000001}}`), claudeUsageNow)
	if err != nil || quota.Windows[0].Remaining > 1 || quota.Windows[0].Remaining < 0.9999999 {
		t.Fatalf("near-fresh window: %+v %v", quota, err)
	}
}

func TestParseClaudeUsageQuotaTreatsMissingAndNullWindowsAsUnavailable(t *testing.T) {
	for name, body := range map[string]string{
		"null seven day":      `{"five_hour":{"utilization":10},"seven_day":null}`,
		"missing seven day":   `{"five_hour":{"utilization":10}}`,
		"null utilization":    `{"five_hour":{"utilization":10},"seven_day":{"utilization":null,"resets_at":null}}`,
		"missing utilization": `{"five_hour":{"utilization":10},"seven_day":{"resets_at":"2026-10-20T00:00:00Z"}}`,
	} {
		quota, err := ParseClaudeUsageQuota([]byte(body), claudeUsageNow)
		if err != nil || len(quota.Windows) != 1 || quota.Windows[0].ID != ClaudeUsageFiveHour || quota.Windows[0].Remaining != 0.9 {
			t.Fatalf("%s: %+v %v", name, quota, err)
		}
	}
	quota, err := ParseClaudeUsageQuota([]byte(`{"five_hour":null,"seven_day":{"utilization":20}}`), claudeUsageNow)
	if err != nil || len(quota.Windows) != 1 || quota.Windows[0].ID != ClaudeUsageSevenDay {
		t.Fatalf("a missing five-hour window must not be reported as zero: %+v %v", quota, err)
	}
}

func TestParseClaudeUsageQuotaRejectsEmptyResponsesWithoutAdvancingObservation(t *testing.T) {
	for name, body := range map[string]string{
		"empty object":     `{}`,
		"all null":         `{"five_hour":null,"seven_day":null}`,
		"unrelated only":   `{"seven_day_opus":{"utilization":10},"five_hour_x":{"utilization":10}}`,
		"case differences": `{"Five_Hour":{"utilization":10},"SEVEN_DAY":{"utilization":10}}`,
		"null utilization": `{"five_hour":{"utilization":null}}`,
	} {
		_, err := ParseClaudeUsageQuota([]byte(body), claudeUsageNow)
		var failure *Error
		if !errors.As(err, &failure) || failure.Cause != ClaudeUsageEmptyCause {
			t.Fatalf("%s was not rejected as empty: %v", name, err)
		}
	}
}

func TestParseClaudeUsageQuotaFailsMalformedRecognizedWindowsAtomically(t *testing.T) {
	for name, body := range map[string]string{
		"empty body":               ``,
		"not json":                 `utilization`,
		"array":                    `[]`,
		"scalar":                   `1`,
		"trailing data":            `{"five_hour":{"utilization":10}} {}`,
		"trailing garbage":         `{"five_hour":{"utilization":10}}x`,
		"truncated":                `{"five_hour":{"utilization":10}`,
		"duplicate window":         `{"five_hour":{"utilization":10},"five_hour":{"utilization":20}}`,
		"duplicate utilization":    `{"five_hour":{"utilization":10,"utilization":20}}`,
		"duplicate reset":          `{"five_hour":{"utilization":10,"resets_at":null,"resets_at":null}}`,
		"window is array":          `{"five_hour":[]}`,
		"window is number":         `{"five_hour":10}`,
		"window is string":         `{"five_hour":"10"}`,
		"string utilization":       `{"five_hour":{"utilization":"10"}}`,
		"boolean utilization":      `{"five_hour":{"utilization":true}}`,
		"negative utilization":     `{"five_hour":{"utilization":-0.1}}`,
		"over one hundred":         `{"five_hour":{"utilization":100.000001}}`,
		"huge exponent":            `{"five_hour":{"utilization":1e99999999999}}`,
		"tiny exponent":            `{"five_hour":{"utilization":1e-99999999999}}`,
		"overlong number":          `{"five_hour":{"utilization":0.` + strings.Repeat("0", 80) + `1}}`,
		"bad reset":                `{"five_hour":{"utilization":10,"resets_at":"tomorrow"}}`,
		"numeric reset":            `{"five_hour":{"utilization":10,"resets_at":1760000000}}`,
		"reset in unavailable":     `{"five_hour":{"utilization":10},"seven_day":{"utilization":null,"resets_at":"tomorrow"}}`,
		"malformed beside healthy": `{"five_hour":{"utilization":10},"seven_day":{"utilization":150}}`,
		"zero reset":               `{"five_hour":{"utilization":10,"resets_at":"0001-01-01T00:00:00Z"}}`,
		"far future reset":         `{"five_hour":{"utilization":10,"resets_at":"10000-01-01T00:00:00Z"}}`,
	} {
		quota, err := ParseClaudeUsageQuota([]byte(body), claudeUsageNow)
		var failure *Error
		if !errors.As(err, &failure) || failure.Cause != ClaudeUsageMalformedCause || len(quota.Windows) != 0 || !quota.ObservedAt.IsZero() {
			t.Fatalf("%s was not rejected atomically: %+v %v", name, quota, err)
		}
	}
}

func TestParseClaudeUsageQuotaIgnoresDuplicatedUnrelatedKeysAndKeepsCaseSensitivity(t *testing.T) {
	quota, err := ParseClaudeUsageQuota([]byte(`{"other":1,"other":2,"Five_Hour":{"utilization":1},"five_hour":{"utilization":10,"Utilization":99,"resets_at":null,"extra":{"a":[1,2,{"b":null}]}}}`), claudeUsageNow)
	if err != nil || len(quota.Windows) != 1 || quota.Windows[0].Remaining != 0.9 {
		t.Fatalf("unrelated or differently cased keys influenced the result: %+v %v", quota, err)
	}
}

func TestParseClaudeUsageQuotaBoundsBodyAndObservationTime(t *testing.T) {
	body := `{"five_hour":{"utilization":10},"padding":"` + strings.Repeat("a", MaxClaudeUsageResponseBytes) + `"}`
	if _, err := ParseClaudeUsageQuota([]byte(body), claudeUsageNow); err == nil {
		t.Fatal("an oversized response was accepted")
	}
	exact := `{"five_hour":{"utilization":10},"padding":"`
	exact += strings.Repeat("a", MaxClaudeUsageResponseBytes-len(exact)-2) + `"}`
	if len(exact) != MaxClaudeUsageResponseBytes {
		t.Fatalf("fixture size %d", len(exact))
	}
	if _, err := ParseClaudeUsageQuota([]byte(exact), claudeUsageNow); err != nil {
		t.Fatalf("a response exactly at the bound was rejected: %v", err)
	}
	if _, err := ParseClaudeUsageQuota([]byte(`{"five_hour":{"utilization":10}}`), time.Time{}); err == nil {
		t.Fatal("a zero observation time was accepted")
	}
}

func TestParseClaudeUsageQuotaErrorsExposeNoProviderText(t *testing.T) {
	_, err := ParseClaudeUsageQuota([]byte(`{"five_hour":{"utilization":"SECRET-sk-ant-oat01-value"}}`), claudeUsageNow)
	if err == nil || strings.Contains(err.Error(), "SECRET") || strings.Contains(SafeError(err).Message, "sk-ant") || SafeError(err).Code != Unavailable {
		t.Fatalf("failure exposed provider text: %v", err)
	}
}

func FuzzParseClaudeUsageQuota(f *testing.F) {
	for _, seed := range []string{`{}`, `{"five_hour":{"utilization":25}}`, `{"seven_day":null}`, `[`, `{"five_hour":{"utilization":1e400}}`} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		quota, err := ParseClaudeUsageQuota(body, claudeUsageNow)
		if err != nil {
			if len(quota.Windows) != 0 {
				t.Fatal("a failure returned partial windows")
			}
			return
		}
		if len(quota.Windows) == 0 || len(quota.Windows) > 2 {
			t.Fatal("a success without a bounded window set")
		}
		for _, w := range quota.Windows {
			if !w.ID.Valid() || !(w.Remaining >= 0 && w.Remaining <= 1) {
				t.Fatalf("invalid window %+v", w)
			}
		}
	})
}
