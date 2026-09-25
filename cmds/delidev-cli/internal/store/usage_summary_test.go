package store

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func readUsage(s *Store, f domain.UsageSelection) (domain.UsageSummary, error) {
	var result domain.UsageSummary
	err := s.Read(context.Background(), func(tx *Tx) error { var err error; result, err = tx.UsageSummary(f); return err })
	return result, err
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
			filter.ModelID = domain.NewID()
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
			copy.ModelID = domain.NewID()
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
