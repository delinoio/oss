package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/delinoio/oss/servers/delibase/internal/contracts"
	"github.com/delinoio/oss/servers/delibase/internal/reliability"
	"github.com/google/uuid"
)

type polarUsagePayload struct {
	EventName      string    `json:"event_name"`
	OrganizationID string    `json:"organization_id"`
	UsageRecordID  string    `json:"usage_record_id"`
	Units          int64     `json:"units"`
	CommittedAt    time.Time `json:"committed_at"`
}

// NewPolarUsageHandler recovers metered reporting independently of local
// authorization. Polar event external IDs make provider delivery idempotent
// even after worker crashes or dead-letter recovery.
func NewPolarUsageHandler(
	reporter contracts.PolarUsageReporter,
) reliability.Handler {
	return func(ctx context.Context, item reliability.Item) error {
		if reporter == nil || item.EntityID == uuid.Nil {
			return reliability.ErrInvalidInput
		}
		var payload polarUsagePayload
		if json.Unmarshal(item.Payload, &payload) != nil ||
			payload.EventName == "" || payload.Units < 0 ||
			payload.CommittedAt.IsZero() {
			return reliability.ErrInvalidInput
		}
		organizationID, err := uuid.Parse(payload.OrganizationID)
		if err != nil || organizationID.Version() != 7 ||
			organizationID.String() != payload.OrganizationID {
			return reliability.ErrInvalidInput
		}
		usageRecordID, err := uuid.Parse(payload.UsageRecordID)
		if err != nil || usageRecordID.Version() != 7 ||
			usageRecordID.String() != payload.UsageRecordID ||
			usageRecordID != item.EntityID {
			return reliability.ErrInvalidInput
		}
		return reporter.ReportUsage(ctx, contracts.UsageEvent{
			Name:               payload.EventName,
			ExternalCustomerID: payload.OrganizationID,
			ExternalID:         payload.UsageRecordID,
			Units:              payload.Units,
			Timestamp:          payload.CommittedAt,
		})
	}
}
