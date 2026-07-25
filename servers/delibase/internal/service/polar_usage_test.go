package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/delinoio/oss/servers/delibase/internal/contracts"
	"github.com/delinoio/oss/servers/delibase/internal/reliability"
	"github.com/google/uuid"
)

type usageReporterStub struct {
	event contracts.UsageEvent
}

func (stub *usageReporterStub) ReportUsage(
	_ context.Context,
	event contracts.UsageEvent,
) error {
	stub.event = event
	return nil
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
		!reporter.event.Timestamp.Equal(committedAt) {
		t.Fatalf("reported usage = %#v", reporter.event)
	}
}
