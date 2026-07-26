package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/delinoio/oss/servers/delibase/internal/contracts"
	"github.com/delinoio/oss/servers/delibase/internal/reliability"
	"github.com/google/uuid"
)

type usageReporterStub struct {
	event contracts.UsageEvent
	calls int
}

func (stub *usageReporterStub) ReportUsage(
	_ context.Context,
	event contracts.UsageEvent,
) error {
	stub.event = event
	stub.calls++
	return nil
}

func TestPolarOveragePayloadReportsOnlyChargeableMicros(t *testing.T) {
	t.Parallel()
	organizationID := uuid.MustParse("0198a000-0000-7000-8000-000000000001")
	usageRecordID := uuid.MustParse("0198a000-0000-7000-8000-000000000002")
	committedAt := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)

	if payload, report := newPolarOveragePayload(
		"meter_usage", organizationID, usageRecordID, 0, committedAt,
	); report || payload != (polarUsagePayload{}) {
		t.Fatalf("credit-funded payload = %#v, report = %t", payload, report)
	}
	payload, report := newPolarOveragePayload(
		"meter_usage", organizationID, usageRecordID, 17, committedAt,
	)
	if !report || payload.Units != 17 ||
		payload.OrganizationID != organizationID.String() ||
		payload.UsageRecordID != usageRecordID.String() {
		t.Fatalf("overage payload = %#v, report = %t", payload, report)
	}
}

func TestPolarUsageHandlerPinsProviderIdempotencyToUsageRecord(t *testing.T) {
	t.Parallel()
	organizationID := "0198a000-0000-7000-8000-000000000001"
	usageRecordID := "0198a000-0000-7000-8000-000000000002"
	committedAt := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	payload, _ := json.Marshal(polarUsagePayload{
		EventName: "meter_usage", OrganizationID: organizationID,
		UsageRecordID: usageRecordID, Units: 42, CommittedAt: committedAt,
	})
	reporter := &usageReporterStub{}
	handler := NewPolarUsageHandler(reporter)
	err := handler(context.Background(), reliability.Item{
		EntityID: uuid.MustParse(usageRecordID),
		Payload:  payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	if reporter.event.ExternalCustomerID != organizationID ||
		reporter.event.ExternalID != usageRecordID ||
		reporter.event.Units != 42 ||
		!reporter.event.Timestamp.Equal(committedAt) ||
		reporter.calls != 1 {
		t.Fatalf("reported usage = %#v", reporter.event)
	}
}

func TestPolarUsageHandlerRejectsNonpositiveUnits(t *testing.T) {
	t.Parallel()
	organizationID := "0198a000-0000-7000-8000-000000000001"
	usageRecordID := "0198a000-0000-7000-8000-000000000002"
	committedAt := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	reporter := &usageReporterStub{}
	handler := NewPolarUsageHandler(reporter)

	for _, units := range []int64{0, -1} {
		payload, err := json.Marshal(polarUsagePayload{
			EventName: "meter_usage", OrganizationID: organizationID,
			UsageRecordID: usageRecordID, Units: units, CommittedAt: committedAt,
		})
		if err != nil {
			t.Fatal(err)
		}
		err = handler(context.Background(), reliability.Item{
			EntityID: uuid.MustParse(usageRecordID),
			Payload:  payload,
		})
		if !errors.Is(err, reliability.ErrInvalidInput) {
			t.Fatalf("units %d: handler error = %v", units, err)
		}
	}
	if reporter.calls != 0 {
		t.Fatalf("nonpositive usage reported %d times", reporter.calls)
	}
}
