package domain

import (
	"reflect"
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

func TestUsageDayBucketsResolveMissingAndRepeatedMidnight(t *testing.T) {
	for _, scenario := range []struct {
		name       string
		zone       string
		boundaries []string
	}{
		{
			name:       "Santiago missing midnight",
			zone:       "America/Santiago",
			boundaries: []string{"2026-09-05T04:00:00Z", "2026-09-06T04:00:00Z", "2026-09-07T03:00:00Z"},
		},
		{
			name:       "Havana missing midnight",
			zone:       "America/Havana",
			boundaries: []string{"2026-03-07T05:00:00Z", "2026-03-08T05:00:00Z", "2026-03-09T04:00:00Z"},
		},
		{
			name:       "Beirut missing midnight",
			zone:       "Asia/Beirut",
			boundaries: []string{"2026-03-27T22:00:00Z", "2026-03-28T22:00:00Z", "2026-03-29T21:00:00Z"},
		},
		{
			name:       "Havana repeated midnight",
			zone:       "America/Havana",
			boundaries: []string{"2026-10-31T04:00:00Z", "2026-11-01T04:00:00Z", "2026-11-02T05:00:00Z"},
		},
		{
			name:       "clip within first repeated midnight hour",
			zone:       "America/Havana",
			boundaries: []string{"2026-11-01T04:30:00Z", "2026-11-02T05:00:00Z", "2026-11-02T05:30:00Z"},
		},
		{
			name:       "clip before missing midnight",
			zone:       "America/Santiago",
			boundaries: []string{"2026-09-06T03:30:00Z", "2026-09-06T04:00:00Z", "2026-09-06T04:30:00Z"},
		},
		{
			name:       "UTC year boundary",
			zone:       "UTC",
			boundaries: []string{"2026-12-31T12:00:00Z", "2027-01-01T00:00:00Z", "2027-01-01T12:00:00Z"},
		},
		{
			name:       "New York future year boundary",
			zone:       "America/New_York",
			boundaries: []string{"2050-12-31T05:00:00Z", "2051-01-01T05:00:00Z", "2051-01-02T05:00:00Z"},
		},
		{
			name:       "New York future leap year boundary",
			zone:       "America/New_York",
			boundaries: []string{"2052-12-31T05:00:00Z", "2053-01-01T05:00:00Z", "2053-01-02T05:00:00Z"},
		},
		{
			name:       "supported calendar upper bound",
			zone:       "UTC",
			boundaries: []string{"9999-12-30T12:00:00Z", "9999-12-31T00:00:00Z", "9999-12-31T23:59:59Z"},
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			selection := usageDaySelection(t, scenario.boundaries[0], scenario.boundaries[len(scenario.boundaries)-1], scenario.zone)
			buckets, err := selection.UsageDayBuckets()
			if err != nil || len(buckets) != len(scenario.boundaries)-1 {
				t.Fatalf("unexpected buckets: %+v %v", buckets, err)
			}
			zone, err := time.LoadLocation(scenario.zone)
			if err != nil {
				t.Fatal(err)
			}
			for i, bucket := range buckets {
				if bucket.From.UTC().Format(time.RFC3339) != scenario.boundaries[i] || bucket.Until.UTC().Format(time.RFC3339) != scenario.boundaries[i+1] {
					t.Fatalf("boundary %d: %s..%s, want %s..%s", i, bucket.From.UTC().Format(time.RFC3339), bucket.Until.UTC().Format(time.RFC3339), scenario.boundaries[i], scenario.boundaries[i+1])
				}
				if !bucket.Until.After(bucket.From) || (i > 0 && !buckets[i-1].Until.Equal(bucket.From)) {
					t.Fatalf("partition has a gap or empty day: %+v", buckets)
				}
				if bucket.From.In(zone).Format(time.DateOnly) != bucket.Until.Add(-time.Nanosecond).In(zone).Format(time.DateOnly) {
					t.Fatalf("bucket spans different civil dates: %+v", bucket)
				}
			}
		})
	}
}

func TestUsageDayBucketsRetainBucketLimit(t *testing.T) {
	selection := usageDaySelection(t, "2026-01-01T00:00:00Z", "2027-01-06T00:00:00Z", "UTC")
	buckets, err := selection.UsageDayBuckets()
	if err != nil || len(buckets) != UsageDayBucketLimit {
		t.Fatalf("exact bucket limit rejected: %d %v", len(buckets), err)
	}
	selection.Until = selection.Until.Add(time.Nanosecond)
	if buckets, err := selection.UsageDayBuckets(); err == nil || buckets != nil {
		t.Fatalf("bucket overflow returned partial analytics: %d %v", len(buckets), err)
	}
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
	if got := buckets[0].Until.UTC().Format(time.RFC3339); got != "2011-12-30T10:00:00Z" || !buckets[0].Until.Equal(buckets[1].From) {
		t.Fatalf("wrong skipped-date boundary: %+v", buckets)
	}
	if buckets[0].From.In(zone).Day() != 29 || buckets[1].From.In(zone).Day() != 31 {
		t.Fatalf("skipped date acquired a bucket: %+v", buckets)
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

func TestUsageDayBucketsSaoPauloMissingMidnightPartitionAndExactTotals(t *testing.T) {
	selection := usageDaySelection(t, "2018-11-03T03:00:00Z", "2018-11-06T02:00:00Z", "America/Sao_Paulo")
	if err := selection.Validate(); err != nil {
		t.Fatal(err)
	}
	buckets, err := selection.UsageDayBuckets()
	if err != nil || len(buckets) != 3 {
		t.Fatalf("Sao Paulo civil dates: %+v %v", buckets, err)
	}
	zone, err := time.LoadLocation(selection.TimeZone)
	if err != nil {
		t.Fatal(err)
	}
	boundaries := []string{"2018-11-03T03:00:00Z", "2018-11-04T03:00:00Z", "2018-11-05T02:00:00Z", "2018-11-06T02:00:00Z"}
	dates := []string{"2018-11-03", "2018-11-04", "2018-11-05"}
	durations := []time.Duration{24 * time.Hour, 23 * time.Hour, 24 * time.Hour}
	var partition time.Duration
	for i, bucket := range buckets {
		if bucket.From.UTC().Format(time.RFC3339) != boundaries[i] || bucket.Until.UTC().Format(time.RFC3339) != boundaries[i+1] {
			t.Fatalf("wrong civil boundary %d: %+v", i, bucket)
		}
		if bucket.From.In(zone).Format(time.DateOnly) != dates[i] || bucket.Until.Add(-time.Nanosecond).In(zone).Format(time.DateOnly) != dates[i] {
			t.Fatalf("duplicate or folded civil date %d: %+v", i, bucket)
		}
		if got := bucket.Until.Sub(bucket.From); got != durations[i] {
			t.Fatalf("day %s duration = %s, want %s", dates[i], got, durations[i])
		}
		if i > 0 && !buckets[i-1].Until.Equal(bucket.From) {
			t.Fatal("civil partition has a gap or overlap")
		}
		partition += bucket.Until.Sub(bucket.From)
	}
	if !buckets[0].From.Equal(selection.From) || !buckets[2].Until.Equal(selection.Until) || partition != selection.Until.Sub(selection.From) || partition != 71*time.Hour {
		t.Fatal("requested half-open range was not completely partitioned")
	}
	// This date has no real midnight. Its first instant is 01:00, rather than
	// time.Date's normalized 23:00 on the preceding date.
	if start := buckets[1].From.In(zone); start.Hour() != 1 || start.Minute() != 0 {
		t.Fatalf("nonexistent midnight was used: %s", start)
	}
	large, seven, zero, eleven, outside := int64(9007199254740993), int64(7), int64(0), int64(11), int64(99)
	events := []struct {
		at    time.Time
		total *int64
	}{
		{selection.From.Add(-time.Nanosecond), &outside},
		{selection.From, &large},
		{buckets[1].From.Add(-time.Nanosecond), &seven},
		{buckets[1].From, &large},
		{buckets[2].From.Add(-time.Nanosecond), nil},
		{buckets[2].From, &zero},
		{selection.Until.Add(-time.Nanosecond), &eleven},
		{selection.Until, &outside},
	}
	var overall UsageTotals
	for _, event := range events {
		selected := !event.at.Before(selection.From) && event.at.Before(selection.Until)
		matches := 0
		for i := range buckets {
			if !event.at.Before(buckets[i].From) && event.at.Before(buckets[i].Until) {
				buckets[i].Totals.Add(&NativeTokenCounts{Total: event.total})
				matches++
			}
		}
		if selected {
			overall.Add(&NativeTokenCounts{Total: event.total})
		}
		if selected && matches != 1 || !selected && matches != 0 {
			t.Fatalf("boundary event belongs to %d buckets: %s", matches, event.at)
		}
	}
	var merged UsageTotals
	for i, expected := range []UsageMeasure{{KnownTotal: "9007199254741000", MeasuredResponses: 2}, {KnownTotal: "9007199254740993", MeasuredResponses: 1, UnavailableResponses: 1}, {KnownTotal: "11", MeasuredResponses: 2}} {
		if buckets[i].Totals.Responses != 2 || buckets[i].Totals.Total != expected {
			t.Fatalf("day %s lost exact boundary totals: %+v", dates[i], buckets[i].Totals)
		}
		merged.Merge(buckets[i].Totals)
	}
	if !reflect.DeepEqual(merged, overall) || overall.Responses != 6 || overall.Total != (UsageMeasure{KnownTotal: "18014398509482004", MeasuredResponses: 5, UnavailableResponses: 1}) {
		t.Fatalf("daily totals do not reconcile exactly: daily=%+v overall=%+v", merged, overall)
	}
}
