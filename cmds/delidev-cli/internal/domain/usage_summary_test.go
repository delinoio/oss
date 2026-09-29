package domain

import (
	"strings"
	"testing"
	"time"
)

func usageDaySelection(t *testing.T, from, until, zone string) UsageSelection {
	t.Helper()
	start, err := time.Parse(time.RFC3339, from)
	if err != nil {
		t.Fatal(err)
	}
	end, err := time.Parse(time.RFC3339, until)
	if err != nil {
		t.Fatal(err)
	}
	return UsageSelection{From: start, Until: end, Granularity: UsageTimeGranularityDay, TimeZone: zone}
}

func TestUsageDayBucketsPartitionCalendarTimeAcrossDST(t *testing.T) {
	selection := usageDaySelection(t, "2026-03-07T12:00:00Z", "2026-03-10T12:00:00Z", "America/New_York")
	buckets, err := selection.UsageDayBuckets()
	if err != nil || len(buckets) != 4 {
		t.Fatalf("spring-forward buckets: %v %v", buckets, err)
	}
	want := []string{"2026-03-07T12:00:00Z", "2026-03-08T05:00:00Z", "2026-03-09T04:00:00Z", "2026-03-10T04:00:00Z", "2026-03-10T12:00:00Z"}
	for i, bucket := range buckets {
		if bucket.From.UTC().Format(time.RFC3339) != want[i] || bucket.Until.UTC().Format(time.RFC3339) != want[i+1] {
			t.Fatalf("spring-forward boundary %d: %s..%s", i, bucket.From.UTC().Format(time.RFC3339), bucket.Until.UTC().Format(time.RFC3339))
		}
		if !bucket.Until.After(bucket.From) || (i > 0 && !buckets[i-1].Until.Equal(bucket.From)) {
			t.Fatalf("spring-forward partition has a gap or empty day: %+v", buckets)
		}
	}
	if got := buckets[1].Until.Sub(buckets[1].From); got != 23*time.Hour {
		t.Fatalf("spring-forward calendar day is %s, want 23h", got)
	}

	fall := usageDaySelection(t, "2026-11-01T04:00:00Z", "2026-11-02T05:00:00Z", "America/New_York")
	buckets, err = fall.UsageDayBuckets()
	if err != nil || len(buckets) != 1 || buckets[0].Until.Sub(buckets[0].From) != 25*time.Hour {
		t.Fatalf("fall-back day did not retain both repeated hours: %+v %v", buckets, err)
	}
}

func TestUsageDayBucketsSkipSkippedCivilDateAndClipRange(t *testing.T) {
	zone, err := time.LoadLocation("Pacific/Apia")
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2011, time.December, 29, 0, 0, 0, 0, zone)
	until := time.Date(2012, time.January, 1, 0, 0, 0, 0, zone)
	buckets, err := (UsageSelection{From: from, Until: until, Granularity: UsageTimeGranularityDay, TimeZone: "Pacific/Apia"}).UsageDayBuckets()
	if err != nil || len(buckets) != 2 {
		t.Fatalf("skipped civil date created a zero-duration bucket: %+v %v", buckets, err)
	}
	for i, bucket := range buckets {
		if !bucket.Until.After(bucket.From) || (i > 0 && !buckets[i-1].Until.Equal(bucket.From)) {
			t.Fatalf("skipped-date partition has a gap or empty day: %+v", buckets)
		}
	}
	if !buckets[0].From.Equal(from) || !buckets[len(buckets)-1].Until.Equal(until) {
		t.Fatalf("clipped endpoints changed: %+v", buckets)
	}
}

func TestUsageGranularityAndTimezoneValidation(t *testing.T) {
	base := usageDaySelection(t, "2026-09-01T00:00:00Z", "2026-09-02T00:00:00Z", "UTC")
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []UsageSelection{
		{From: base.From, Until: base.Until, Granularity: UsageTimeGranularityUnspecified, TimeZone: "UTC"},
		{From: base.From, Until: base.Until, Granularity: UsageTimeGranularityDay},
		{From: base.From, Until: base.Until, Granularity: UsageTimeGranularityDay, TimeZone: "Local"},
		{From: base.From, Until: base.Until, Granularity: UsageTimeGranularityDay, TimeZone: " UTC"},
		{From: base.From, Until: base.Until, Granularity: UsageTimeGranularityDay, TimeZone: strings.Repeat("A", 257)},
		{From: base.From, Until: base.Until, Granularity: UsageTimeGranularityDay, TimeZone: "Mars/Olympus"},
		{From: base.From, Until: base.Until, Granularity: UsageTimeGranularity(2), TimeZone: "UTC"},
	} {
		if err := bad.Validate(); err == nil {
			t.Fatalf("invalid usage granularity or timezone accepted: %+v", bad)
		}
	}
}
