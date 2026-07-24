// Package reliability provides the PostgreSQL-backed inbox, outbox, deletion
// job, and immutable audit primitives used by delibase business transactions.
package reliability

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/delinoio/oss/servers/internal/safeerr"
	"github.com/delinoio/oss/servers/internal/safelog"
	"github.com/google/uuid"
)

const (
	MaxNormalAttempts  = 12
	DeadLetterInterval = 24 * time.Hour
)

var (
	ErrInvalidInput        = errors.New("reliability: invalid input")
	ErrIdempotencyConflict = errors.New("reliability: idempotency conflict")
	ErrStaleClaim          = errors.New("reliability: stale claim")
)

// Queue identifies one durable reliability queue.
type Queue uint8

const (
	QueueWebhookInbox Queue = iota + 1
	QueueIntegrationOutbox
	QueueDeletionJob
)

func (queue Queue) String() string {
	switch queue {
	case QueueWebhookInbox:
		return "webhook_inbox"
	case QueueIntegrationOutbox:
		return "integration_outbox"
	case QueueDeletionJob:
		return "deletion_job"
	default:
		return "invalid"
	}
}

// Provider is a supported signed-webhook source.
type Provider string

const ProviderPolar Provider = "polar"

// WebhookEventType is a registered Polar event contract, never a worker
// control string supplied by a request.
type WebhookEventType string

const (
	WebhookOrderPaid            WebhookEventType = "order.paid"
	WebhookSubscriptionCreated  WebhookEventType = "subscription.created"
	WebhookSubscriptionUpdated  WebhookEventType = "subscription.updated"
	WebhookSubscriptionCanceled WebhookEventType = "subscription.canceled"
	WebhookSubscriptionRevoked  WebhookEventType = "subscription.revoked"
	WebhookRefundCreated        WebhookEventType = "refund.created"
	WebhookRefundUpdated        WebhookEventType = "refund.updated"
)

// Integration is a supported outbound provider.
type Integration string

const (
	IntegrationPolar Integration = "polar"
	IntegrationLogto Integration = "logto"
)

// IntegrationOperation is a stable outbound action.
type IntegrationOperation string

const (
	OperationReportUsage        IntegrationOperation = "report_usage"
	OperationCancelSubscription IntegrationOperation = "cancel_subscription"
	OperationDeleteLogtoAccount IntegrationOperation = "delete_account"
)

// AggregateType identifies the safe local entity owning an outbox event.
type AggregateType string

const (
	AggregateUsageRecord  AggregateType = "usage_record"
	AggregateOrganization AggregateType = "organization"
	AggregateAccount      AggregateType = "account"
)

// DeletionJobType identifies a durable deletion workflow.
type DeletionJobType string

const (
	DeletionAccount      DeletionJobType = "account"
	DeletionOrganization DeletionJobType = "organization"
)

// HandlerID is the complete stable worker dispatch key.
type HandlerID string

const (
	HandlerPolarOrderPaid            HandlerID = "polar.webhook.order_paid"
	HandlerPolarSubscriptionCreated  HandlerID = "polar.webhook.subscription_created"
	HandlerPolarSubscriptionUpdated  HandlerID = "polar.webhook.subscription_updated"
	HandlerPolarSubscriptionCanceled HandlerID = "polar.webhook.subscription_canceled"
	HandlerPolarSubscriptionRevoked  HandlerID = "polar.webhook.subscription_revoked"
	HandlerPolarRefundCreated        HandlerID = "polar.webhook.refund_created"
	HandlerPolarRefundUpdated        HandlerID = "polar.webhook.refund_updated"
	HandlerPolarReportUsage          HandlerID = "polar.outbox.report_usage"
	HandlerPolarCancelSubscription   HandlerID = "polar.outbox.cancel_subscription"
	HandlerLogtoDeleteAccount        HandlerID = "logto.outbox.delete_account"
	HandlerDeleteAccount             HandlerID = "deletion.account"
	HandlerDeleteOrganization        HandlerID = "deletion.organization"
)

// AuditEventType is the allowlisted immutable audit contract.
type AuditEventType string

const (
	AuditAuthorizationDecision       AuditEventType = "authorization.decision"
	AuditOrganizationCreated         AuditEventType = "organization.created"
	AuditOrganizationUpdated         AuditEventType = "organization.updated"
	AuditOrganizationDeleted         AuditEventType = "organization.deleted"
	AuditRoleUpdated                 AuditEventType = "role.updated"
	AuditInvitationCreated           AuditEventType = "invitation.created"
	AuditInvitationAccepted          AuditEventType = "invitation.accepted"
	AuditInvitationRevoked           AuditEventType = "invitation.revoked"
	AuditTeamCreated                 AuditEventType = "team.created"
	AuditTeamUpdated                 AuditEventType = "team.updated"
	AuditTeamDeleted                 AuditEventType = "team.deleted"
	AuditBillingLimitUpdated         AuditEventType = "billing_limit.updated"
	AuditCheckoutCreated             AuditEventType = "checkout.created"
	AuditSubscriptionUpdated         AuditEventType = "subscription.updated"
	AuditRefundRecorded              AuditEventType = "refund.recorded"
	AuditReservationCreated          AuditEventType = "reservation.created"
	AuditReservationCommitted        AuditEventType = "reservation.committed"
	AuditReservationReleased         AuditEventType = "reservation.released"
	AuditSettlementRecorded          AuditEventType = "settlement.recorded"
	AuditAccountDeletionRequested    AuditEventType = "account.deletion_requested"
	AuditOrganizationDeletionRequest AuditEventType = "organization.deletion_requested"
	AuditWebhookReceived             AuditEventType = "webhook.received"
	AuditWebhookProcessed            AuditEventType = "webhook.processed"
)

type WebhookInput struct {
	ID              uuid.UUID
	Provider        Provider
	ProviderEventID string
	EventType       WebhookEventType
	Payload         json.RawMessage
	Actor           safelog.ActorPseudonym
}

type OutboxInput struct {
	ID             uuid.UUID
	Integration    Integration
	Operation      IntegrationOperation
	AggregateType  AggregateType
	AggregateID    uuid.UUID
	Payload        json.RawMessage
	IdempotencyKey string
	Actor          safelog.ActorPseudonym
}

type DeletionInput struct {
	ID             uuid.UUID
	Type           DeletionJobType
	AccountID      uuid.UUID
	OrganizationID uuid.UUID
	IdempotencyKey string
	Actor          safelog.ActorPseudonym
}

// AuditMetadata deliberately exposes only request correlation fields accepted
// by the database allowlist.
type AuditMetadata struct {
	RequestID        string
	TraceID          string
	RequestMethod    string
	RequestProcedure string
}

type AuditInput struct {
	ID                uuid.UUID
	OccurredAt        time.Time
	EventType         AuditEventType
	Actor             safelog.ActorPseudonym
	OrganizationID    uuid.UUID
	TeamID            uuid.UUID
	ServiceIdentityID uuid.UUID
	MeterID           uuid.UUID
	ReservationID     uuid.UUID
	Decision          safelog.Decision
	Result            safelog.Result
	ErrorClass        safeerr.Class
	IncludeErrorClass bool
	Metadata          AuditMetadata
}

// Item is a leased immutable delivery. Payload must never be logged.
type Item struct {
	ID                     uuid.UUID
	Queue                  Queue
	HandlerID              HandlerID
	Payload                json.RawMessage
	EntityID               uuid.UUID
	Actor                  safelog.ActorPseudonym
	AttemptCount           int
	DeadLetterAttemptCount int
	DeadLetter             bool
	ClaimToken             uuid.UUID
}

type Handler func(context.Context, Item) error

type Clock interface {
	Now() time.Time
}

type Random interface {
	Float64() float64
}

type TokenGenerator interface {
	New() (uuid.UUID, error)
}

type Storage interface {
	RecoverExpired(context.Context, time.Time, time.Duration, time.Duration, float64) error
	Claim(context.Context, Queue, uuid.UUID, time.Time, time.Time) (Item, bool, error)
	Complete(context.Context, Item, time.Time) error
	Fail(context.Context, Item, time.Time, time.Time, bool, safeerr.Class) error
}
