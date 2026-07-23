package reliability

import (
	"context"
	"errors"
	"time"

	"github.com/delinoio/oss/servers/delibase/internal/database/dbgen"
	"github.com/delinoio/oss/servers/internal/safeerr"
	"github.com/delinoio/oss/servers/internal/safelog"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// PostgreSQLStorage implements leased worker state transitions using generated
// sqlc queries. Every transition compares the current claim token.
type PostgreSQLStorage struct {
	queries dbgen.Querier
}

func NewPostgreSQLStorage(queries dbgen.Querier) (*PostgreSQLStorage, error) {
	if queries == nil {
		return nil, ErrInvalidInput
	}
	return &PostgreSQLStorage{queries: queries}, nil
}

func (storage *PostgreSQLStorage) RecoverExhausted(
	ctx context.Context,
	now time.Time,
) error {
	if storage == nil || storage.queries == nil || now.IsZero() {
		return ErrInvalidInput
	}
	timestamp := pgTime(now)
	if _, err := storage.queries.RecoverExhaustedWebhookInbox(ctx, timestamp); err != nil {
		return err
	}
	if _, err := storage.queries.RecoverExhaustedIntegrationOutbox(ctx, timestamp); err != nil {
		return err
	}
	if _, err := storage.queries.RecoverExhaustedDeletionJobs(ctx, timestamp); err != nil {
		return err
	}
	return nil
}

func (storage *PostgreSQLStorage) Claim(
	ctx context.Context,
	queue Queue,
	claimToken uuid.UUID,
	now time.Time,
	expiresAt time.Time,
) (Item, bool, error) {
	if storage == nil || storage.queries == nil || claimToken == uuid.Nil ||
		now.IsZero() || !expiresAt.After(now) {
		return Item{}, false, ErrInvalidInput
	}
	parameters := claimParameters(claimToken, now, expiresAt)
	var item Item
	var err error
	switch queue {
	case QueueWebhookInbox:
		var row dbgen.WebhookInbox
		row, err = storage.queries.ClaimWebhookInbox(ctx, dbgen.ClaimWebhookInboxParams(parameters))
		if err == nil {
			item, err = webhookItem(row)
		}
	case QueueIntegrationOutbox:
		var row dbgen.IntegrationOutbox
		row, err = storage.queries.ClaimIntegrationOutbox(ctx, dbgen.ClaimIntegrationOutboxParams(parameters))
		if err == nil {
			item, err = outboxItem(row)
		}
	case QueueDeletionJob:
		var row dbgen.DeletionJob
		row, err = storage.queries.ClaimDeletionJob(ctx, dbgen.ClaimDeletionJobParams(parameters))
		if err == nil {
			item, err = deletionItem(row)
		}
	default:
		return Item{}, false, ErrInvalidInput
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return Item{}, false, nil
	}
	if err != nil {
		return Item{}, false, err
	}
	return item, true, nil
}

type commonClaimParameters struct {
	ClaimToken     pgtype.UUID
	Now            pgtype.Timestamptz
	ClaimExpiresAt pgtype.Timestamptz
}

func claimParameters(
	claimToken uuid.UUID,
	now time.Time,
	expiresAt time.Time,
) commonClaimParameters {
	return commonClaimParameters{
		ClaimToken:     pgUUID(claimToken),
		Now:            pgTime(now),
		ClaimExpiresAt: pgTime(expiresAt),
	}
}

func (storage *PostgreSQLStorage) Complete(
	ctx context.Context,
	item Item,
	completedAt time.Time,
) error {
	if storage == nil || storage.queries == nil || completedAt.IsZero() ||
		item.ID == uuid.Nil || item.ClaimToken == uuid.Nil {
		return ErrInvalidInput
	}
	var err error
	switch item.Queue {
	case QueueWebhookInbox:
		_, err = storage.queries.CompleteWebhookInbox(ctx, dbgen.CompleteWebhookInboxParams{
			CompletedAt: pgTime(completedAt),
			ID:          pgUUID(item.ID),
			ClaimToken:  pgUUID(item.ClaimToken),
		})
	case QueueIntegrationOutbox:
		_, err = storage.queries.CompleteIntegrationOutbox(ctx, dbgen.CompleteIntegrationOutboxParams{
			CompletedAt: pgTime(completedAt),
			ID:          pgUUID(item.ID),
			ClaimToken:  pgUUID(item.ClaimToken),
		})
	case QueueDeletionJob:
		_, err = storage.queries.CompleteDeletionJob(ctx, dbgen.CompleteDeletionJobParams{
			CompletedAt: pgTime(completedAt),
			ID:          pgUUID(item.ID),
			ClaimToken:  pgUUID(item.ClaimToken),
		})
	default:
		return ErrInvalidInput
	}
	return transitionError(err)
}

func (storage *PostgreSQLStorage) Fail(
	ctx context.Context,
	item Item,
	failedAt time.Time,
	nextAttemptAt time.Time,
	deadLetter bool,
	class safeerr.Class,
) error {
	if storage == nil || storage.queries == nil || failedAt.IsZero() ||
		!nextAttemptAt.After(failedAt) ||
		item.ID == uuid.Nil || item.ClaimToken == uuid.Nil {
		return ErrInvalidInput
	}
	parameters := commonFailureParameters{
		NextAttemptAt:  pgTime(nextAttemptAt),
		DeadLetter:     deadLetter,
		FailedAt:       pgTime(failedAt),
		SafeErrorClass: pgtype.Text{String: class.String(), Valid: true},
		ID:             pgUUID(item.ID),
		ClaimToken:     pgUUID(item.ClaimToken),
	}
	var err error
	switch item.Queue {
	case QueueWebhookInbox:
		_, err = storage.queries.FailWebhookInbox(ctx, dbgen.FailWebhookInboxParams(parameters))
	case QueueIntegrationOutbox:
		_, err = storage.queries.FailIntegrationOutbox(ctx, dbgen.FailIntegrationOutboxParams(parameters))
	case QueueDeletionJob:
		_, err = storage.queries.FailDeletionJob(ctx, dbgen.FailDeletionJobParams(parameters))
	default:
		return ErrInvalidInput
	}
	return transitionError(err)
}

type commonFailureParameters struct {
	NextAttemptAt  pgtype.Timestamptz
	DeadLetter     bool
	FailedAt       pgtype.Timestamptz
	SafeErrorClass pgtype.Text
	ID             pgtype.UUID
	ClaimToken     pgtype.UUID
}

func transitionError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrStaleClaim
	}
	return err
}

func webhookItem(row dbgen.WebhookInbox) (Item, error) {
	id, err := uuidFromPG(row.ID)
	if err != nil {
		return Item{}, err
	}
	claimToken, err := uuidFromPG(row.ClaimToken)
	if err != nil {
		return Item{}, err
	}
	handler := webhookHandler(WebhookEventType(row.EventType))
	if handler == "" {
		return Item{}, ErrInvalidInput
	}
	return Item{
		ID:                     id,
		Queue:                  QueueWebhookInbox,
		HandlerID:              handler,
		Payload:                append([]byte(nil), row.Payload...),
		Actor:                  safelog.ActorPseudonym(row.ActorReference),
		AttemptCount:           int(row.AttemptCount),
		DeadLetterAttemptCount: int(row.DeadLetterAttemptCount),
		DeadLetter:             row.DeadLetteredAt.Valid,
		ClaimToken:             claimToken,
	}, nil
}

func outboxItem(row dbgen.IntegrationOutbox) (Item, error) {
	id, err := uuidFromPG(row.ID)
	if err != nil {
		return Item{}, err
	}
	claimToken, err := uuidFromPG(row.ClaimToken)
	if err != nil {
		return Item{}, err
	}
	entityID, err := uuidFromPG(row.AggregateID)
	if err != nil {
		return Item{}, err
	}
	handler := outboxHandler(Integration(row.Integration), IntegrationOperation(row.Operation))
	if handler == "" {
		return Item{}, ErrInvalidInput
	}
	return Item{
		ID:                     id,
		Queue:                  QueueIntegrationOutbox,
		HandlerID:              handler,
		Payload:                append([]byte(nil), row.Payload...),
		EntityID:               entityID,
		Actor:                  safelog.ActorPseudonym(row.ActorReference),
		AttemptCount:           int(row.AttemptCount),
		DeadLetterAttemptCount: int(row.DeadLetterAttemptCount),
		DeadLetter:             row.DeadLetteredAt.Valid,
		ClaimToken:             claimToken,
	}, nil
}

func deletionItem(row dbgen.DeletionJob) (Item, error) {
	id, err := uuidFromPG(row.ID)
	if err != nil {
		return Item{}, err
	}
	claimToken, err := uuidFromPG(row.ClaimToken)
	if err != nil {
		return Item{}, err
	}
	jobType := DeletionJobType(row.JobType)
	handler := deletionHandler(jobType)
	if handler == "" {
		return Item{}, ErrInvalidInput
	}
	entity := row.AccountID
	if jobType == DeletionOrganization {
		entity = row.OrganizationID
	}
	entityID, err := uuidFromPG(entity)
	if err != nil {
		return Item{}, err
	}
	return Item{
		ID:                     id,
		Queue:                  QueueDeletionJob,
		HandlerID:              handler,
		EntityID:               entityID,
		Actor:                  safelog.ActorPseudonym(row.ActorReference),
		AttemptCount:           int(row.AttemptCount),
		DeadLetterAttemptCount: int(row.DeadLetterAttemptCount),
		DeadLetter:             row.DeadLetteredAt.Valid,
		ClaimToken:             claimToken,
	}, nil
}
