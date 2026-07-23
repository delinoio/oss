package reliability

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/delinoio/oss/servers/delibase/internal/database/dbgen"
	"github.com/delinoio/oss/servers/internal/redact"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

const maximumPayloadBytes = 1 << 20

const (
	deletionAccountTargetConstraint      = "deletion_jobs_account_target_unique"
	deletionOrganizationTargetConstraint = "deletion_jobs_organization_target_unique"
)

var (
	safeExternalID  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,254}$`)
	idempotencyUUID = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	actorPattern    = regexp.MustCompile(`^(|actor:v1:[0-9a-f]{32})$`)
)

// EnqueueWebhook persists a verified webhook idempotently. Call it with the
// sqlc Queries supplied to database.Store.WithinTransaction when the inbox
// insert must commit atomically with other local state.
func EnqueueWebhook(
	ctx context.Context,
	queries dbgen.Querier,
	input WebhookInput,
) (uuid.UUID, error) {
	if queries == nil || !validUUIDv7(input.ID) ||
		input.Provider != ProviderPolar || webhookHandler(input.EventType) == "" ||
		!safeExternalID.MatchString(input.ProviderEventID) ||
		!validActor(string(input.Actor)) {
		return uuid.Nil, ErrInvalidInput
	}
	payload, err := validatePayload(input.Payload)
	if err != nil {
		return uuid.Nil, err
	}
	digest := sha256.Sum256(payload)
	row, err := queries.EnqueueWebhookInbox(ctx, dbgen.EnqueueWebhookInboxParams{
		ID:              pgUUID(input.ID),
		Provider:        string(input.Provider),
		ProviderEventID: input.ProviderEventID,
		EventType:       string(input.EventType),
		Payload:         payload,
		PayloadSha256:   digest[:],
		ActorReference:  string(input.Actor),
	})
	if err != nil {
		if err == pgx.ErrNoRows {
			return uuid.Nil, ErrIdempotencyConflict
		}
		return uuid.Nil, err
	}
	return uuidFromPG(row.ID)
}

// EnqueueOutbox persists an outbound side effect idempotently.
func EnqueueOutbox(
	ctx context.Context,
	queries dbgen.Querier,
	input OutboxInput,
) (uuid.UUID, error) {
	if queries == nil || !validUUIDv7(input.ID) || !validUUIDv7(input.AggregateID) ||
		outboxHandler(input.Integration, input.Operation) == "" ||
		!validAggregate(input.AggregateType, input.Integration, input.Operation) ||
		!validIdempotencyKey(input.IdempotencyKey) ||
		!validActor(string(input.Actor)) {
		return uuid.Nil, ErrInvalidInput
	}
	payload, err := validatePayload(input.Payload)
	if err != nil {
		return uuid.Nil, err
	}
	row, err := queries.EnqueueIntegrationOutbox(ctx, dbgen.EnqueueIntegrationOutboxParams{
		ID:             pgUUID(input.ID),
		Integration:    string(input.Integration),
		Operation:      string(input.Operation),
		AggregateType:  string(input.AggregateType),
		AggregateID:    pgUUID(input.AggregateID),
		Payload:        payload,
		IdempotencyKey: input.IdempotencyKey,
		ActorReference: string(input.Actor),
	})
	if err != nil {
		if err == pgx.ErrNoRows {
			return uuid.Nil, ErrIdempotencyConflict
		}
		return uuid.Nil, err
	}
	return uuidFromPG(row.ID)
}

// EnqueueDeletion persists an account or organization deletion workflow
// idempotently.
func EnqueueDeletion(
	ctx context.Context,
	queries dbgen.Querier,
	input DeletionInput,
) (uuid.UUID, error) {
	if queries == nil || !validUUIDv7(input.ID) ||
		!validIdempotencyKey(input.IdempotencyKey) ||
		!validActor(string(input.Actor)) {
		return uuid.Nil, ErrInvalidInput
	}
	accountID := pgtype.UUID{}
	organizationID := pgtype.UUID{}
	switch input.Type {
	case DeletionAccount:
		if !validUUIDv7(input.AccountID) || input.OrganizationID != uuid.Nil {
			return uuid.Nil, ErrInvalidInput
		}
		accountID = pgUUID(input.AccountID)
	case DeletionOrganization:
		if !validUUIDv7(input.OrganizationID) || input.AccountID != uuid.Nil {
			return uuid.Nil, ErrInvalidInput
		}
		organizationID = pgUUID(input.OrganizationID)
	default:
		return uuid.Nil, ErrInvalidInput
	}
	row, err := queries.EnqueueDeletionJob(ctx, dbgen.EnqueueDeletionJobParams{
		ID:             pgUUID(input.ID),
		AccountID:      accountID,
		OrganizationID: organizationID,
		JobType:        string(input.Type),
		IdempotencyKey: input.IdempotencyKey,
		ActorReference: string(input.Actor),
	})
	if err != nil {
		if err == pgx.ErrNoRows || deletionTargetConflict(err) {
			return uuid.Nil, ErrIdempotencyConflict
		}
		return uuid.Nil, err
	}
	return uuidFromPG(row.ID)
}

// AppendAudit appends one immutable, typed, credential-free audit record.
func AppendAudit(
	ctx context.Context,
	queries dbgen.Querier,
	input AuditInput,
) (uuid.UUID, error) {
	if queries == nil || !validUUIDv7(input.ID) ||
		!validAuditType(input.EventType) || !validActor(string(input.Actor)) ||
		!validDecision(input.Decision) || !validResult(input.Result) ||
		!validOptionalUUIDv7(input.OrganizationID) ||
		!validOptionalUUIDv7(input.TeamID) ||
		!validOptionalUUIDv7(input.ServiceIdentityID) ||
		!validOptionalUUIDv7(input.MeterID) ||
		!validOptionalUUIDv7(input.ReservationID) {
		return uuid.Nil, ErrInvalidInput
	}
	if input.OccurredAt.IsZero() {
		return uuid.Nil, ErrInvalidInput
	}
	metadata, err := marshalAuditMetadata(input.Metadata)
	if err != nil {
		return uuid.Nil, err
	}
	errorClass := pgtype.Text{}
	if input.IncludeErrorClass {
		errorClass = pgtype.Text{String: input.ErrorClass.String(), Valid: true}
	}
	row, err := queries.AppendAuditEvent(ctx, dbgen.AppendAuditEventParams{
		ID:                pgUUID(input.ID),
		OccurredAt:        pgTime(input.OccurredAt),
		EventType:         string(input.EventType),
		ActorReference:    string(input.Actor),
		OrganizationID:    optionalUUID(input.OrganizationID),
		TeamID:            optionalUUID(input.TeamID),
		ServiceIdentityID: optionalUUID(input.ServiceIdentityID),
		MeterID:           optionalUUID(input.MeterID),
		ReservationID:     optionalUUID(input.ReservationID),
		Decision:          string(input.Decision),
		Result:            string(input.Result),
		SafeErrorClass:    errorClass,
		Metadata:          metadata,
	})
	if err != nil {
		return uuid.Nil, err
	}
	return uuidFromPG(row.ID)
}

func validatePayload(raw json.RawMessage) ([]byte, error) {
	if len(raw) == 0 || len(raw) > maximumPayloadBytes {
		return nil, ErrInvalidInput
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, ErrInvalidInput
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, ErrInvalidInput
	}
	object, ok := value.(map[string]any)
	if !ok || !payloadValueIsSafe("", object, 0) {
		return nil, ErrInvalidInput
	}
	canonical, err := json.Marshal(object)
	if err != nil || len(canonical) > maximumPayloadBytes {
		return nil, ErrInvalidInput
	}
	return canonical, nil
}

func payloadValueIsSafe(key string, value any, depth int) bool {
	if depth > 32 || redact.IsSensitiveKey(key) || billingPIIKey(key) {
		return false
	}
	switch typed := value.(type) {
	case string:
		if _, err := uuid.Parse(typed); err == nil {
			return true
		}
		return len(typed) <= maximumPayloadBytes && redact.Text(typed) == typed
	case json.Number:
		number := typed.String()
		return redact.Text(number) == number
	case map[string]any:
		for childKey, child := range typed {
			childPath := childKey
			if key != "" {
				childPath = key + "." + childKey
			}
			if !payloadValueIsSafe(childPath, child, depth+1) {
				return false
			}
		}
	case []any:
		for _, child := range typed {
			if !payloadValueIsSafe(key, child, depth+1) {
				return false
			}
		}
	}
	return true
}

func validIdempotencyKey(value string) bool {
	if !safeExternalID.MatchString(value) {
		return false
	}
	// Canonical UUID segments are safe identifiers, but their numeric tails can
	// match the shared card-number detector. Mask them before checking the
	// remaining key for credential and billing-data shapes.
	withoutUUIDs := idempotencyUUID.ReplaceAllString(value, "uuid")
	return redact.Text(withoutUUIDs) == withoutUUIDs
}

func deletionTargetConflict(err error) bool {
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "23505" {
		return false
	}
	switch postgresError.ConstraintName {
	case deletionAccountTargetConstraint, deletionOrganizationTargetConstraint:
		return true
	default:
		return false
	}
}

func billingPIIKey(key string) bool {
	normalized := strings.Map(func(character rune) rune {
		switch character {
		case '-', '_', '.', ' ', '/':
			return -1
		default:
			return character
		}
	}, strings.ToLower(key))
	for _, fragment := range []string{
		"billingname",
		"customername",
		"fullname",
		"postal",
		"taxid",
		"paymentmethod",
		"billingdetails",
	} {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}

func marshalAuditMetadata(metadata AuditMetadata) ([]byte, error) {
	value := make(map[string]string, 4)
	for key, item := range map[string]string{
		"request_id":        metadata.RequestID,
		"trace_id":          metadata.TraceID,
		"request_method":    metadata.RequestMethod,
		"request_procedure": metadata.RequestProcedure,
	} {
		if item == "" {
			continue
		}
		if redact.Text(item) != item {
			return nil, ErrInvalidInput
		}
		value[key] = item
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, ErrInvalidInput
	}
	return encoded, nil
}

func validActor(actor string) bool { return actorPattern.MatchString(actor) }

func validUUIDv7(id uuid.UUID) bool {
	return id != uuid.Nil && id.Version() == 7 && id.Variant() == uuid.RFC4122
}

func validOptionalUUIDv7(id uuid.UUID) bool {
	return id == uuid.Nil || validUUIDv7(id)
}

func pgUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: [16]byte(id), Valid: true}
}

func optionalUUID(id uuid.UUID) pgtype.UUID {
	if id == uuid.Nil {
		return pgtype.UUID{}
	}
	return pgUUID(id)
}

func uuidFromPG(id pgtype.UUID) (uuid.UUID, error) {
	if !id.Valid {
		return uuid.Nil, ErrInvalidInput
	}
	return uuid.UUID(id.Bytes), nil
}

func pgTime(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value.UTC(), Valid: true}
}
