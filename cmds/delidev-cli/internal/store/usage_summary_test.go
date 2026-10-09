package store

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func readUsage(s *Store, f domain.UsageSelection) (domain.UsageSummary, error) {
	var result domain.UsageSummary
	err := s.Read(context.Background(), func(tx *Tx) error { var err error; result, err = tx.UsageSummary(f); return err })
	return result, err
}

func writeUsageAt(t *testing.T, s *Store, record domain.ResponseUsageRecord, observed time.Time) domain.ID {
	t.Helper()
	id := domain.NewID()
	if _, _, err := writeResponse(s, id, record); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE response_usage SET created_at=? WHERE id=?", observed.UnixMilli(), id); err != nil {
		t.Fatal(err)
	}
	return id
}

func usageCounts(input, output, total int64) *domain.NativeTokenCounts {
	cached, reasoning := int64(0), int64(0)
	return &domain.NativeTokenCounts{Input: &input, Cached: &cached, Output: &output, Reasoning: &reasoning, Total: &total}
}

func TestUsageSummaryDailyAttributionAcrossMissingMidnight(t *testing.T) {
	for _, scenario := range []struct {
		name  string
		zone  string
		from  string
		next  string
		until string
	}{
		{name: "Santiago", zone: "America/Santiago", from: "2026-09-05T04:00:00Z", next: "2026-09-06T04:00:00Z", until: "2026-09-07T03:00:00Z"},
		{name: "Havana", zone: "America/Havana", from: "2026-03-07T05:00:00Z", next: "2026-03-08T05:00:00Z", until: "2026-03-09T04:00:00Z"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			s, _ := openTest(t)
			fixture := seedSearch(t, s, "source", domain.Archived)
			parse := func(value string) time.Time {
				t.Helper()
				parsed, err := time.Parse(time.RFC3339, value)
				if err != nil {
					t.Fatal(err)
				}
				return parsed
			}
			from, next, until := parse(scenario.from), parse(scenario.next), parse(scenario.until)
			for i, observation := range []struct {
				at     time.Time
				counts *domain.NativeTokenCounts
			}{
				{at: from.Add(-time.Millisecond), counts: usageCounts(100, 0, 100)},
				{at: next.Add(-30 * time.Minute), counts: usageCounts(math.MaxInt64, 0, math.MaxInt64)},
				{at: next, counts: usageCounts(10, 7, 17)},
				{at: next.Add(30 * time.Minute)},
				{at: until, counts: usageCounts(100, 0, 100)},
			} {
				record := responseRecord(fixture)
				record.Sequence = uint64(i + 1)
				record.Usage.ResponseDigest = fmt.Sprintf("%064x", i+1)
				record.Usage.Counts = observation.counts
				writeUsageAt(t, s, record, observation.at)
			}
			selection := domain.UsageSelection{From: from, Until: until, Granularity: domain.UsageTimeGranularityDay, TimeZone: scenario.zone}
			summary, err := readUsage(s, selection)
			if err != nil {
				t.Fatal(err)
			}
			if summary.Analytics == nil || len(summary.Analytics.Days) != 2 {
				t.Fatalf("missing daily analytics: %+v", summary.Analytics)
			}
			days := summary.Analytics.Days
			if !days[0].From.Equal(from) || !days[0].Until.Equal(next) || !days[1].From.Equal(next) || !days[1].Until.Equal(until) || days[0].Until.Sub(days[0].From) != 24*time.Hour || days[1].Until.Sub(days[1].From) != 23*time.Hour {
				t.Fatalf("wrong midnight-gap boundaries: %+v", days)
			}
			if days[0].Totals.Responses != 1 || days[0].Totals.Total.KnownTotal != "9223372036854775807" || days[1].Totals.Responses != 2 || days[1].Totals.Total.KnownTotal != "17" || days[1].Totals.Total.UnavailableResponses != 1 {
				t.Fatalf("response assigned to another civil date: %+v", days)
			}
			var combined domain.UsageTotals
			for _, day := range days {
				combined.Merge(day.Totals)
			}
			if !reflect.DeepEqual(combined, summary.Totals) || summary.Totals.Responses != 3 || summary.Totals.Total.KnownTotal != "9223372036854775824" || summary.Totals.Total.MeasuredResponses != 2 || summary.Totals.Total.UnavailableResponses != 1 {
				t.Fatalf("daily and overall totals differ: %+v %+v", combined, summary.Totals)
			}
			legacy, err := readUsage(s, domain.UsageSelection{From: from, Until: until})
			if err != nil || !reflect.DeepEqual(legacy.Totals, summary.Totals) {
				t.Fatalf("daily attribution changed overall totals: %+v %v", legacy.Totals, err)
			}
		})
	}
}

func TestUsageSummaryDailyAndModelAnalyticsShareRetentionSnapshot(t *testing.T) {
	s, _ := openTest(t)
	fixture := seedSearch(t, s, "source", domain.Archived)
	zone, err := time.LoadLocation("Asia/Seoul")
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, zone)
	until := time.Date(2026, 9, 5, 12, 0, 0, 0, zone)
	provider := domain.NewID()
	modelA := (domain.ModelIdentity{ProviderID: provider, NativeID: "fixture"}).Key()
	modelB := (domain.ModelIdentity{ProviderID: provider, NativeID: "other"}).Key()
	base := responseRecord(fixture)
	base.ProviderID, base.ModelID = provider, modelA
	price := preparePrice(t, s, base)
	if _, err := s.db.Exec("UPDATE pricing_versions SET created_at=? WHERE id=?", time.Date(2027, 2, 1, 0, 0, 0, 0, time.UTC).UnixMilli(), price.ID); err != nil {
		t.Fatal(err)
	}
	write := func(model domain.ID, input, output, total int64, at time.Time, digest int) {
		record := base
		record.ModelID = model
		record.Sequence = uint64(digest)
		record.Usage.ResponseDigest = fmt.Sprintf("%064x", digest)
		if total < 0 {
			record.Usage.Counts = nil
		} else {
			record.Usage.Counts = usageCounts(input, output, total)
		}
		writeUsageAt(t, s, record, at)
	}
	write(modelA, 100, 20, 120, time.Date(2026, 9, 1, 12, 0, 0, 0, zone), 1)
	write(modelA, 0, 0, 0, time.Date(2026, 9, 2, 12, 0, 0, 0, zone), 2)
	write(modelB, 0, 0, -1, time.Date(2026, 9, 3, 12, 0, 0, 0, zone), 3)
	write(modelA, 20, 10, 30, time.Date(2026, 9, 5, 10, 0, 0, 0, zone), 4)
	write(modelB, 10, 5, 15, time.Date(2026, 9, 5, 11, 0, 0, 0, zone), 5)
	write(modelA, 900, 100, 1000, from.Add(-time.Millisecond), 6)
	write(modelA, 900, 100, 1000, until, 7)
	missing := seedSearch(t, s, "accepted without response", domain.NotArchived)
	if _, err := s.db.Exec("UPDATE entities SET created_at=? WHERE kind='job' AND json_extract(body,'$.input.execution_id')=?", time.Date(2026, 9, 4, 12, 0, 0, 0, zone).UnixMilli(), missing.execution); err != nil {
		t.Fatal(err)
	}
	// A duplicate publication preserves the response's first-retention timestamp.
	duplicate := base
	duplicate.Sequence++
	duplicate.Usage.ResponseDigest = fmt.Sprintf("%064x", 1)
	duplicate.Usage.Counts = usageCounts(100, 20, 120)
	if retained, replayed, err := writeResponse(s, domain.NewID(), duplicate); err != nil || !replayed {
		t.Fatalf("duplicate response was not deduplicated: %s %t %v", retained, replayed, err)
	}
	selection := domain.UsageSelection{From: from, Until: until, Granularity: domain.UsageTimeGranularityDay, TimeZone: "Asia/Seoul"}
	summary, err := readUsage(s, selection)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Totals.Responses != 5 || summary.Totals.Total.KnownTotal != "165" || summary.Totals.Total.MeasuredResponses != 4 || summary.Totals.Total.UnavailableResponses != 1 || summary.AcceptedExecutionsWithoutResponse != 1 {
		t.Fatalf("wrong overall total or coverage: %+v", summary)
	}
	if summary.Analytics == nil || summary.Analytics.TimeZone != "Asia/Seoul" || len(summary.Analytics.Days) != 5 {
		t.Fatalf("daily analytics missing or not clipped: %+v", summary.Analytics)
	}
	wantTotals := []string{"120", "0", "", "", "45"}
	wantResponses := []uint32{1, 1, 1, 0, 2}
	wantMeasured := []uint32{1, 1, 0, 0, 2}
	wantUnavailable := []uint32{0, 0, 1, 0, 0}
	for i, day := range summary.Analytics.Days {
		measure := day.Totals.Total
		if measure.KnownTotal != wantTotals[i] || day.Totals.Responses != wantResponses[i] || measure.MeasuredResponses != wantMeasured[i] || measure.UnavailableResponses != wantUnavailable[i] {
			t.Fatalf("day %d lost zero/missing/no-record distinction: %+v", i, day)
		}
		if i > 0 && !summary.Analytics.Days[i-1].Until.Equal(day.From) {
			t.Fatalf("day buckets have a gap: %+v", summary.Analytics.Days)
		}
	}
	if summary.Analytics.Days[0].Totals.Total.KnownTotal != "120" || !summary.Analytics.Days[0].From.Equal(from) || !summary.Analytics.Days[4].Until.Equal(until) {
		t.Fatalf("day buckets used pricing time or changed the selected endpoints: %+v", summary.Analytics.Days)
	}
	models := summary.Analytics.Models
	if len(models) != 2 || models[0].ModelID != modelA || models[0].Totals.Total.KnownTotal != "150" || models[1].ModelID != modelB || models[1].Totals.Total.KnownTotal != "15" || models[1].Totals.Total.UnavailableResponses != 1 || summary.Analytics.OtherModels != nil {
		t.Fatalf("model totals did not reconcile with overall total: %+v", summary.Analytics)
	}
	if models[0].Totals.Total.KnownTotal != "150" || summary.Totals.Total.KnownTotal != "165" {
		t.Fatal("daily/model/overall totals do not reconcile")
	}
	legacy, err := readUsage(s, domain.UsageSelection{From: from, Until: until})
	if err != nil || legacy.Analytics != nil {
		t.Fatalf("unspecified granularity changed the original summary shape: %+v %v", legacy, err)
	}
}

func TestUsageSummaryModelRankingAndOtherModels(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		values []int64
		other  string
	}{
		{name: "ranked zeros", values: []int64{100, 90, 80, 70, 60, 0, 0, -1}, other: "0"},
		{name: "nonzero other", values: []int64{100, 90, 80, 70, 60, 50, 40, -1}, other: "90"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			s, _ := openTest(t)
			fixture := seedSearch(t, s, "source", domain.NotArchived)
			base := responseRecord(fixture)
			provider := domain.NewID()
			for index, total := range scenario.values {
				record := base
				record.ProviderID = provider
				record.ModelID = (domain.ModelIdentity{ProviderID: provider, NativeID: fmt.Sprintf("rank-%d", index)}).Key()
				record.Sequence = uint64(index + 1)
				record.Usage.ResponseDigest = fmt.Sprintf("%064x", index+1)
				if total < 0 {
					record.Usage.Counts = nil
				} else {
					record.Usage.Counts = usageCounts(total, 0, total)
				}
				writeUsageAt(t, s, record, time.Date(2026, 9, 1, index, 0, 0, 0, time.UTC))
			}
			selection := domain.UsageSelection{From: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), Until: time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC), Granularity: domain.UsageTimeGranularityDay, TimeZone: "UTC"}
			summary, err := readUsage(s, selection)
			if err != nil || summary.Analytics == nil || len(summary.Analytics.Models) != len(scenario.values) {
				t.Fatalf("missing complete model analytics: %+v %v", summary.Analytics, err)
			}
			if summary.Analytics.Models[0].Totals.Total.KnownTotal != "100" || summary.Analytics.Models[1].Totals.Total.KnownTotal != "90" {
				t.Fatalf("models are not exactly ranked: %+v", summary.Analytics.Models)
			}
			if summary.Analytics.Models[len(summary.Analytics.Models)-1].Totals.Total.MeasuredResponses != 0 {
				t.Fatalf("unmeasured model was ranked as zero: %+v", summary.Analytics.Models)
			}
			other := summary.Analytics.OtherModels
			if other == nil || other.ModelCount != 2 || other.Totals.Total.KnownTotal != scenario.other || other.Totals.Total.MeasuredResponses != 2 {
				t.Fatalf("other models did not aggregate only the measured groups after five: %+v", other)
			}
		})
	}
}

func TestSortUsageAnalyticsModelsUsesExactIntegerAndOriginalIdTuple(t *testing.T) {
	models := []domain.UsageAnalyticsModel{
		{ProviderID: "provider-b", ModelID: "model-a", Totals: domain.UsageTotals{Total: domain.UsageMeasure{KnownTotal: "18446744073709551614", MeasuredResponses: 1}}},
		{ProviderID: "provider-a", ModelID: "model-b", Totals: domain.UsageTotals{Total: domain.UsageMeasure{KnownTotal: "18446744073709551614", MeasuredResponses: 1}}},
		{ProviderID: "provider-a", ModelID: "model-a", Totals: domain.UsageTotals{Total: domain.UsageMeasure{KnownTotal: "9", MeasuredResponses: 1}}},
		{ProviderID: "provider-a", ModelID: "model-c", Totals: domain.UsageTotals{Total: domain.UsageMeasure{UnavailableResponses: 1}}},
	}
	domain.SortUsageAnalyticsModels(models)
	if models[0].ProviderID != "provider-a" || models[0].ModelID != "model-b" || models[1].ProviderID != "provider-b" || models[1].ModelID != "model-a" || models[2].Totals.Total.KnownTotal != "9" || models[3].Totals.Total.KnownTotal != "" {
		t.Fatalf("model ordering rounded totals or used labels: %+v", models)
	}
}
func usageWindow() domain.UsageSelection {
	return domain.UsageSelection{From: time.Now().Add(-time.Hour), Until: time.Now().Add(time.Hour)}
}
func TestUsageSummaryExactNullableCountersAndHistoricalFilters(t *testing.T) {
	s, _ := openTest(t)
	f := seedSearch(t, s, "original", domain.Archived)
	record := responseRecord(f)
	maximum := int64(math.MaxInt64)
	record.Usage.Counts.Total = &maximum
	for i := 1; i <= 3; i++ {
		record.Usage.ResponseDigest = fmt.Sprintf("%064x", i)
		record.Sequence = uint64(i)
		if i == 3 {
			record.Usage.Counts = nil
		}
		if _, _, err := writeResponse(s, domain.NewID(), record); err != nil {
			t.Fatal(err)
		}
	}
	missing := seedSearch(t, s, "without telemetry", domain.NotArchived)
	selection := usageWindow()
	result, err := readUsage(s, selection)
	if err != nil || result.Totals.Responses != 3 || result.Totals.Total.KnownTotal != "18446744073709551614" || result.Totals.Total.MeasuredResponses != 2 || result.Totals.Total.UnavailableResponses != 1 || result.Totals.CachedInput.KnownTotal != "4" || result.Totals.CacheWriteInput.KnownTotal != "" || result.Totals.CacheWriteInput.UnavailableResponses != 3 || result.AcceptedExecutionsWithoutResponse != 1 {
		t.Fatalf("wrong known subtotal/coverage: %+v %v", result, err)
	}
	if len(result.Groups) != 1 || result.Groups[0].AccountID != record.AccountID || result.Groups[0].ProjectID != record.ProjectID {
		t.Fatal("lost immutable attribution")
	}
	for _, field := range []string{"session", "project", "account", "provider", "model", "general"} {
		filter := selection
		switch field {
		case "session":
			filter.SessionID = domain.NewID()
		case "project":
			filter.ProjectID = domain.NewID()
		case "account":
			filter.AccountID = domain.NewID()
		case "provider":
			filter.ProviderID = domain.NewID()
		case "model":
			filter.ModelID = (domain.ModelIdentity{ProviderID: record.ProviderID, NativeID: "unmatched"}).Key()
		case "general":
			filter.GeneralChat = true
		}
		value, err := readUsage(s, filter)
		if err != nil || value.Totals.Responses != 0 || value.Totals.Total.KnownTotal != "" || len(value.Groups) != 0 {
			t.Fatal("filter ignored or empty invented zero", field, err)
		}
	}
	selection.SessionID = missing.session
	result, err = readUsage(s, selection)
	if err != nil || result.AcceptedExecutionsWithoutResponse != 1 || result.Totals.Total.KnownTotal != "" {
		t.Fatal("missing response became measured zero", err)
	}
	selection.SessionID = f.session
	_, err = s.Mutate(context.Background(), domain.NewID(), "fixture.relabel", nil, func(tx *Tx) (any, error) {
		row, err := tx.Get(domain.SessionKind, f.session)
		if err != nil {
			return nil, err
		}
		value, _ := Decode[domain.Session](row)
		value.CurrentExecution = &domain.ExecutionSelection{AccountID: domain.NewID()}
		return tx.Put(row.Kind, row.ID, row.Revision, row.SessionID, row.ProjectID, value)
	})
	if err != nil {
		t.Fatal(err)
	}
	selection.AccountID = record.AccountID
	result, err = readUsage(s, selection)
	if err != nil || result.Totals.Responses != 3 {
		t.Fatal("current account relabeled history", err)
	}
	// Reads do not change archive or session revision.
	row, err := s.Get(context.Background(), domain.SessionKind, f.session)
	if err != nil || row.Revision != 2 {
		t.Fatal("usage read mutated original session", err)
	}
}
func TestUsageSummaryHalfOpenTimeZeroAndGroupBounds(t *testing.T) {
	s, _ := openTest(t)
	record := responseRecord(seedSearch(t, s, "source", domain.NotArchived))
	zero := int64(0)
	record.Usage.Counts.Total = &zero
	id := domain.NewID()
	if _, _, err := writeResponse(s, id, record); err != nil {
		t.Fatal(err)
	}
	retained, err := s.ResponseUsage(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	filter := domain.UsageSelection{From: retained.CreatedAt, Until: retained.CreatedAt.Add(time.Millisecond)}
	summary, err := readUsage(s, filter)
	if err != nil || summary.Totals.Total.KnownTotal != "0" {
		t.Fatal("measured zero was lost", err)
	}
	filter.From = retained.CreatedAt.Add(-time.Hour)
	filter.Until = retained.CreatedAt
	summary, err = readUsage(s, filter)
	if err != nil || summary.Totals.Responses != 0 {
		t.Fatal("exclusive endpoint included", err)
	}
	_, err = s.Mutate(context.Background(), domain.NewID(), "fixture.group-limit", nil, func(tx *Tx) (any, error) {
		for i := 0; i < maxUsageGroups; i++ {
			copy := record
			copy.ModelID = (domain.ModelIdentity{ProviderID: record.ProviderID, NativeID: fmt.Sprintf("group-%d", i)}).Key()
			copy.Usage.ResponseDigest = fmt.Sprintf("%064x", i+100)
			if _, _, err := tx.PutResponseUsage(domain.NewID(), copy); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	summary, err = readUsage(s, usageWindow())
	assertCode(t, err, domain.ResourceExhausted)
	if summary.Totals.Responses != 0 || len(summary.Groups) != 0 {
		t.Fatal("bound returned misleading partial total")
	}
	dailyBound := usageWindow()
	dailyBound.Granularity = domain.UsageTimeGranularityDay
	dailyBound.TimeZone = "UTC"
	summary, err = readUsage(s, dailyBound)
	assertCode(t, err, domain.ResourceExhausted)
	if summary.Analytics != nil || summary.Totals.Responses != 0 || len(summary.Groups) != 0 {
		t.Fatal("daily analytics capacity failure returned a partial summary")
	}
	narrow := usageWindow()
	narrow.ModelID = record.ModelID
	summary, err = readUsage(s, narrow)
	if err != nil || summary.Totals.Responses != 1 {
		t.Fatal("narrow selection unavailable", err)
	}
	for _, bad := range []domain.UsageSelection{{From: time.UnixMilli(-1), Until: time.Now()}, {From: time.Now(), Until: time.Now().Add(367 * 24 * time.Hour)}, {From: filter.From, Until: filter.From}, {From: filter.From, Until: filter.Until, SessionID: "invalid"}, {From: filter.From, Until: filter.Until, ProjectID: domain.NewID(), GeneralChat: true}} {
		if _, err := readUsage(s, bad); err == nil {
			t.Fatal("invalid usage scope accepted")
		}
	}
}

func TestSessionTitleUsageStaysOutOfConversationSummary(t *testing.T) {
	s, _ := openTest(t)
	f := seedSearch(t, s, "title usage", domain.NotArchived)
	conversation := responseRecord(f)
	conversation.Usage.ResponseDigest = fmt.Sprintf("%064x", 901)
	if _, _, err := writeResponse(s, domain.NewID(), conversation); err != nil {
		t.Fatal(err)
	}
	title := responseRecord(f)
	title.Purpose = domain.SessionTitleUsage
	title.Usage.ResponseDigest = fmt.Sprintf("%064x", 902)
	title.ThreadID, title.TurnID, title.Sequence = string(domain.NewID()), string(domain.NewID()), 1
	titleID := domain.NewID()
	if _, _, err := writeResponse(s, titleID, title); err != nil {
		t.Fatal(err)
	}
	retained, err := s.ResponseUsage(context.Background(), titleID)
	if err != nil || retained.Record.Purpose != domain.SessionTitleUsage {
		t.Fatalf("title purpose was not durably retained: %+v %v", retained, err)
	}
	selection := usageWindow()
	selection.SessionID = f.session
	result, err := readUsage(s, selection)
	if err != nil || result.Totals.Responses != 1 || result.Groups[0].Totals.Responses != 1 {
		t.Fatalf("title usage entered conversation-turn counts: %+v %v", result, err)
	}
}
