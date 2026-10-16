// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"math"
	"strings"
	"testing"
	"time"
)

func TestParseClaudeUsageQuota(t *testing.T) {
	now := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	got, err := ParseClaudeUsageQuota([]byte(`{"five_hour":{"utilization":25,"resets_at":"2026-10-16T12:00:00Z"},"seven_day":{"utilization":50,"resets_at":"2026-10-20T12:00:00Z"},"other":{"x":1}}`), now)
	if err != nil || len(got.Windows) != 2 || !got.ObservedAt.Equal(now) {
		t.Fatalf("got %+v err %v", got, err)
	}
	if got.Windows[0].ID != "five_hour" || math.Abs(*got.Windows[0].Remaining-0.75) > 1e-9 || got.Windows[0].ResetAt == nil || *got.Windows[0].DurationMinutes != 300 {
		t.Fatalf("five_hour %+v", got.Windows[0])
	}
	if got.Windows[1].ID != "seven_day" || *got.Windows[1].Remaining != 0.5 {
		t.Fatalf("seven_day %+v", got.Windows[1])
	}
	if err := got.Validate(now); err != nil {
		t.Fatal(err)
	}
}

func TestParseClaudeUsageQuotaBoundsAndSparse(t *testing.T) {
	now := time.Now()
	for _, body := range []string{`{"five_hour":{"utilization":0,"resets_at":null}}`, `{"seven_day":{"utilization":100}}`, `{"five_hour":null,"seven_day":{"utilization":1}}`} {
		got, err := ParseClaudeUsageQuota([]byte(body), now)
		if err != nil || len(got.Windows) == 0 {
			t.Fatalf("%s: %v", body, err)
		}
	}
	got, _ := ParseClaudeUsageQuota([]byte(`{"five_hour":{"utilization":100}}`), now)
	if *got.Windows[0].Remaining != 0 || got.Windows[0].ResetAt != nil {
		t.Fatalf("%+v", got.Windows[0])
	}
}

func TestParseClaudeUsageQuotaRejects(t *testing.T) {
	now := time.Now()
	for _, body := range []string{
		``, `null`, `[]`, `{}`, `{"five_hour":null}`, `{"other":{}}`,
		`{"five_hour":{"utilization":"25"}}`, `{"five_hour":{"utilization":-1}}`, `{"five_hour":{"utilization":100.5}}`,
		`{"five_hour":{"utilization":1e999}}`, `{"five_hour":{}}`, `{"five_hour":{"utilization":5,"resets_at":"soon"}}`,
		`{"five_hour":{"utilization":5,"resets_at":7}}`, `{"five_hour":{"utilization":5},"seven_day":{"utilization":200}}`,
		`{"five_hour":{"utilization":5}`, `{"five_hour":{"utilization":5}} {}`,
	} {
		if _, err := ParseClaudeUsageQuota([]byte(body), now); err == nil {
			t.Fatalf("accepted %q", body)
		}
	}
	if _, err := ParseClaudeUsageQuota([]byte(`{"five_hour":{"utilization":5}}`), time.Time{}); err == nil {
		t.Fatal("zero time accepted")
	}
	big := `{"x":"` + strings.Repeat("a", ClaudeUsageResponseLimit) + `"}`
	if _, err := ParseClaudeUsageQuota([]byte(big), now); err == nil {
		t.Fatal("oversized accepted")
	}
}
