// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestNativeReviewUsageRetainsUnknownAndDeduplicatesAfterRestart(t *testing.T) {
	ctx := context.Background()
	s, root := openTest(t)
	record := responseRecord(seedSearch(t, s, "native review", domain.NotArchived))
	record.Purpose = domain.NativeCodeReviewUsage
	record.Usage.Counts = nil
	job, id := domain.NewID(), domain.NewID()
	write := func(record domain.ResponseUsageRecord) (domain.ID, bool, error) {
		var retained domain.ID
		var duplicate bool
		_, err := s.Mutate(ctx, domain.NewID(), "fixture.native-review-usage", record, func(tx *Tx) (any, error) {
			var err error
			retained, duplicate, err = tx.PutNativeCodeReviewUsage(job, id, record)
			return nil, err
		})
		return retained, duplicate, err
	}
	if _, duplicate, err := write(record); err != nil || duplicate {
		t.Fatal("original receipt", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	var err error
	s, err = Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	record.Sequence++
	if retained, duplicate, err := write(record); err != nil || !duplicate || retained != id {
		t.Fatal("restart replay changed receipt", err)
	}
	err = s.Read(ctx, func(tx *Tx) error {
		values, err := tx.NativeCodeReviewUsage(job)
		if err != nil {
			return err
		}
		if len(values) != 1 || values[0].Record.Usage.Counts != nil || values[0].Record.Sequence != 3 || values[0].Estimate.Coverage != domain.EstimateUnavailable {
			t.Fatal("unknown counters invented zero or replay changed source")
		}
		total, err := tx.SessionBudgetEstimate(record.SessionID, "")
		if err == nil && total.UnavailableResponses != 1 {
			t.Fatal("auxiliary replay charged budget twice")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	changed := record
	changed.TurnID = string(domain.NewID())
	if _, _, err := write(changed); err == nil {
		t.Fatal("foreign turn reused original response")
	}
	ordinary := record
	ordinary.Purpose = domain.ConversationUsage
	if _, _, err := writeResponse(s, domain.NewID(), ordinary); err == nil {
		t.Fatal("auxiliary response charged ordinary history")
	}
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM response_usage").Scan(&count); err != nil || count != 0 {
		t.Fatal("legacy purpose table widened", err)
	}
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.remove-session", nil, func(tx *Tx) (any, error) { return nil, tx.Delete(domain.SessionKind, record.SessionID, 1) })
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Read(ctx, func(tx *Tx) error {
		values, err := tx.NativeCodeReviewUsage(job)
		if err == nil && len(values) != 0 {
			t.Fatal("deleted session retained auxiliary receipt")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
