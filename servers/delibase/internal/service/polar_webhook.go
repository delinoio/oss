package service

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"time"

	"github.com/delinoio/oss/servers/delibase/internal/database"
	"github.com/delinoio/oss/servers/delibase/internal/database/dbgen"
	"github.com/delinoio/oss/servers/delibase/internal/reliability"
	"github.com/delinoio/oss/servers/internal/safelog"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const cycleGrantMicros int64 = 10_000_000

type polarBillingEvent struct {
	Type               string    `json:"type"`
	EventAt            time.Time `json:"event_at"`
	ObjectID           string    `json:"object_id"`
	OrderID            string    `json:"order_id"`
	CustomerID         string    `json:"customer_id"`
	ExternalID         string    `json:"external_id"`
	SubscriptionID     string    `json:"subscription_id"`
	ProductID          string    `json:"product_id"`
	Status             string    `json:"status"`
	Currency           string    `json:"currency"`
	BillingReason      string    `json:"billing_reason"`
	Paid               bool      `json:"paid"`
	CurrentPeriodStart time.Time `json:"current_period_start"`
	CurrentPeriodEnd   time.Time `json:"current_period_end"`
	AmountMicros       int64     `json:"amount_micros"`
	Chargeback         bool      `json:"chargeback"`
}

// NewPolarWebhookProcessor returns one idempotent handler shared by all
// allowlisted Polar inbox event IDs. Every financial effect commits in the
// same transaction and uses provider references as ledger uniqueness keys.
func NewPolarWebhookProcessor(
	store *database.Store,
	ids IDGenerator,
) reliability.Handler {
	return func(ctx context.Context, item reliability.Item) error {
		if store == nil || ids == nil || item.ID == uuid.Nil {
			return reliability.ErrInvalidInput
		}
		var event polarBillingEvent
		if json.Unmarshal(item.Payload, &event) != nil ||
			event.EventAt.IsZero() || event.ObjectID == "" {
			return reliability.ErrInvalidInput
		}
		return store.WithinTransaction(
			ctx, pgx.TxOptions{},
			func(queries *dbgen.Queries) error {
				var processErr error
				var subscription dbgen.Subscription
				var cancellationRequired bool
				switch reliability.WebhookEventType(event.Type) {
				case reliability.WebhookOrderPaid:
					processErr = processPaidCycle(ctx, queries, ids, event)
					cancellationRequired = true
				case reliability.WebhookSubscriptionCreated,
					reliability.WebhookSubscriptionUpdated,
					reliability.WebhookSubscriptionActive,
					reliability.WebhookSubscriptionUncanceled,
					reliability.WebhookSubscriptionPastDue,
					reliability.WebhookSubscriptionCanceled,
					reliability.WebhookSubscriptionRevoked:
					subscription, processErr = reconcilePolarSubscription(
						ctx, queries, ids, event,
					)
					cancellationRequired = true
				case reliability.WebhookRefundCreated,
					reliability.WebhookRefundUpdated:
					processErr = processPolarRefund(ctx, queries, ids, item, event)
				default:
					return reliability.ErrInvalidInput
				}
				if processErr != nil {
					return processErr
				}
				if cancellationRequired {
					if !subscription.ID.Valid {
						subscription, processErr = queries.GetSubscriptionByPolarID(
							ctx, event.SubscriptionID,
						)
						if processErr != nil {
							return processErr
						}
					}
					if processErr = enqueueDeletedOrganizationCancellation(
						ctx, queries, ids, item, subscription,
					); processErr != nil {
						return processErr
					}
				}
				return appendPolarBillingAudit(ctx, queries, item, event)
			},
		)
	}
}

func enqueueDeletedOrganizationCancellation(
	ctx context.Context,
	queries *dbgen.Queries,
	ids IDGenerator,
	item reliability.Item,
	subscription dbgen.Subscription,
) error {
	if subscription.Status != "pending" &&
		subscription.Status != "active" &&
		subscription.Status != "past_due" {
		return nil
	}
	organization, err := queries.GetOrganizationByID(ctx, subscription.OrganizationID)
	if err != nil {
		return err
	}
	if !organization.DeletedAt.Valid {
		return nil
	}
	actor, err := queries.GetOrganizationDeletionActor(
		ctx, subscription.OrganizationID,
	)
	if err != nil {
		return err
	}
	outboxID, err := ids.New()
	if err != nil {
		return err
	}
	organizationID := uuid.UUID(subscription.OrganizationID.Bytes)
	_, err = reliability.EnqueueOutbox(
		ctx,
		queries,
		reliability.OutboxInput{
			ID:             outboxID,
			Integration:    reliability.IntegrationPolar,
			Operation:      reliability.OperationCancelSubscription,
			AggregateType:  reliability.AggregateOrganization,
			AggregateID:    organizationID,
			Payload:        []byte(`{"reason":"organization_deletion"}`),
			IdempotencyKey: "polar-webhook:" + item.ID.String(),
			Actor:          safelog.ActorPseudonym(actor),
		},
	)
	return err
}

func appendPolarBillingAudit(
	ctx context.Context,
	queries *dbgen.Queries,
	item reliability.Item,
	event polarBillingEvent,
) error {
	eventType := polarBillingAuditEventType(
		reliability.WebhookEventType(event.Type),
	)
	var organizationID pgtype.UUID
	var err error
	switch reliability.WebhookEventType(event.Type) {
	case reliability.WebhookRefundCreated, reliability.WebhookRefundUpdated:
		var cycle dbgen.PolarPaidCycle
		cycle, err = queries.GetPolarPaidCycle(ctx, event.OrderID)
		organizationID = cycle.OrganizationID
	default:
		var subscription dbgen.Subscription
		subscription, err = queries.GetSubscriptionByPolarID(
			ctx, event.SubscriptionID,
		)
		organizationID = subscription.OrganizationID
	}
	if err != nil {
		return err
	}
	existing, err := queries.GetAuditEvent(ctx, pgUUID(item.ID))
	if err == nil {
		if existing.EventType != string(eventType) ||
			!existing.OrganizationID.Valid ||
			existing.OrganizationID.Bytes != organizationID.Bytes ||
			existing.Result != string(safelog.ResultSuccess) {
			return reliability.ErrIdempotencyConflict
		}
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	_, err = reliability.AppendAudit(
		ctx,
		queries,
		reliability.AuditInput{
			ID:             item.ID,
			OccurredAt:     event.EventAt,
			EventType:      eventType,
			OrganizationID: uuid.UUID(organizationID.Bytes),
			Result:         safelog.ResultSuccess,
		},
	)
	return err
}

func processPaidCycle(
	ctx context.Context,
	queries *dbgen.Queries,
	ids IDGenerator,
	event polarBillingEvent,
) error {
	if !event.Paid || event.Currency != "usd" ||
		event.OrderID == "" || event.SubscriptionID == "" ||
		(event.BillingReason != "subscription_create" &&
			event.BillingReason != "subscription_cycle") ||
		!event.CurrentPeriodStart.Before(event.CurrentPeriodEnd) {
		return reliability.ErrInvalidInput
	}
	mapping, err := queries.GetPolarCatalogMapping(ctx)
	if err != nil {
		return err
	}
	if event.ProductID != mapping.PolarProductID ||
		mapping.PriceMicros != cycleGrantMicros ||
		mapping.CycleGrantMicros != cycleGrantMicros ||
		mapping.Currency != "usd" || mapping.RecurringInterval != "month" {
		return reliability.ErrInvalidInput
	}
	existing, err := queries.GetPolarPaidCycleBinding(ctx, event.OrderID)
	if err == nil {
		customer, customerErr := resolvePolarCustomer(ctx, queries, event)
		if customerErr != nil {
			return customerErr
		}
		if customer.OrganizationID != existing.OrganizationID ||
			existing.PolarSubscriptionID != event.SubscriptionID ||
			!existing.PeriodStartsAt.Time.Equal(event.CurrentPeriodStart) ||
			!existing.PeriodEndsAt.Time.Equal(event.CurrentPeriodEnd) {
			return reliability.ErrInvalidInput
		}
		return nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	subscription, err := reconcilePolarSubscription(ctx, queries, ids, event)
	if err != nil {
		return err
	}
	organizationID := uuid.UUID(subscription.OrganizationID.Bytes)
	organization, err := queries.LockOrganizationForBillingHistory(
		ctx, subscription.OrganizationID,
	)
	if err != nil {
		return err
	}
	if _, err = queries.ReconcileInactiveBillingPeriodForReplacement(
		ctx,
		dbgen.ReconcileInactiveBillingPeriodForReplacementParams{
			ReplacementStartsAt:       pgTimestamp(event.CurrentPeriodStart),
			ReplacementEndsAt:         pgTimestamp(event.CurrentPeriodEnd),
			OrganizationID:            subscription.OrganizationID,
			ReplacementSubscriptionID: subscription.ID,
		},
	); err != nil {
		return err
	}
	periodID, err := ids.New()
	if err != nil {
		return err
	}
	period, err := queries.EnsureBillingPeriod(
		ctx,
		dbgen.EnsureBillingPeriodParams{
			ID:                 pgUUID(periodID),
			OrganizationID:     subscription.OrganizationID,
			SubscriptionID:     subscription.ID,
			StartsAt:           pgTimestamp(event.CurrentPeriodStart),
			EndsAt:             pgTimestamp(event.CurrentPeriodEnd),
			OverageLimitMicros: organization.OverageLimitMicros,
		},
	)
	if err != nil {
		return err
	}
	if _, err = queries.InsertPolarPaidCycle(
		ctx,
		dbgen.InsertPolarPaidCycleParams{
			PolarOrderID:    event.OrderID,
			OrganizationID:  subscription.OrganizationID,
			SubscriptionID:  subscription.ID,
			BillingPeriodID: period.ID,
			PeriodStartsAt:  pgTimestamp(event.CurrentPeriodStart),
			PeriodEndsAt:    pgTimestamp(event.CurrentPeriodEnd),
			PaidAt:          pgTimestamp(event.EventAt),
		},
	); err != nil {
		return err
	}
	balance, err := queries.CurrentOrganizationBalance(
		ctx, subscription.OrganizationID,
	)
	if err != nil {
		return err
	}
	nextBalance, ok := addInt64(balance, cycleGrantMicros)
	if !ok {
		return reliability.ErrInvalidInput
	}
	ledgerID, err := ids.New()
	if err != nil {
		return err
	}
	if _, err = queries.InsertBillingLedgerEntry(
		ctx,
		dbgen.InsertBillingLedgerEntryParams{
			ID:                 pgUUID(ledgerID),
			OrganizationID:     pgUUID(organizationID),
			BillingPeriodID:    period.ID,
			EntryType:          "credit_grant",
			AmountMicros:       cycleGrantMicros,
			BalanceAfterMicros: nextBalance,
			SourceReference:    "polar-order:" + event.OrderID,
		},
	); err != nil {
		return err
	}
	if !organization.DeletedAt.Valid {
		return nil
	}
	settledBalance, err := queries.CurrentSettledCreditBalance(
		ctx, subscription.OrganizationID,
	)
	if err != nil {
		return err
	}
	forfeiture, balanceAfter, ok :=
		settledCreditForfeiture(nextBalance, settledBalance)
	if !ok {
		return reliability.ErrInvalidInput
	}
	if forfeiture == 0 {
		return nil
	}
	forfeitureID, err := ids.New()
	if err != nil {
		return err
	}
	_, err = queries.ForfeitOrganizationCredit(
		ctx,
		dbgen.ForfeitOrganizationCreditParams{
			ID:                 pgUUID(forfeitureID),
			OrganizationID:     subscription.OrganizationID,
			AmountMicros:       forfeiture,
			BalanceAfterMicros: balanceAfter,
			SourceReference:    "polar-order-forfeiture:" + event.OrderID,
		},
	)
	return err
}

func reconcilePolarSubscription(
	ctx context.Context,
	queries *dbgen.Queries,
	ids IDGenerator,
	event polarBillingEvent,
) (dbgen.Subscription, error) {
	if event.SubscriptionID == "" {
		return dbgen.Subscription{}, reliability.ErrInvalidInput
	}
	mapping, err := queries.GetPolarCatalogMapping(ctx)
	if err != nil {
		return dbgen.Subscription{}, err
	}
	if event.ProductID != mapping.PolarProductID {
		return dbgen.Subscription{}, reliability.ErrInvalidInput
	}
	status := polarSubscriptionStatus(
		reliability.WebhookEventType(event.Type), event.Status,
	)
	if status == "" {
		return dbgen.Subscription{}, reliability.ErrInvalidInput
	}
	customer, err := resolvePolarCustomer(ctx, queries, event)
	if err != nil {
		return dbgen.Subscription{}, err
	}
	if _, err = queries.LockOrganizationForBillingHistory(
		ctx, customer.OrganizationID,
	); err != nil {
		return dbgen.Subscription{}, err
	}
	existing, err := queries.GetSubscriptionByPolarID(
		ctx, event.SubscriptionID,
	)
	if err == nil {
		if customer.OrganizationID != existing.OrganizationID {
			return dbgen.Subscription{}, reliability.ErrInvalidInput
		}
		if existing.ProviderEventAt.Time.After(event.EventAt) ||
			existing.Status == "revoked" {
			return existing, nil
		}
		updated, updateErr := queries.UpdateSubscriptionFromPolar(
			ctx,
			dbgen.UpdateSubscriptionFromPolarParams{
				Status:                status,
				CurrentPeriodStartsAt: optionalPGTimestamp(event.CurrentPeriodStart),
				CurrentPeriodEndsAt:   optionalPGTimestamp(event.CurrentPeriodEnd),
				ProviderEventAt:       pgTimestamp(event.EventAt),
				PolarSubscriptionID:   event.SubscriptionID,
			},
		)
		if errors.Is(updateErr, pgx.ErrNoRows) {
			return existing, nil
		}
		return updated, updateErr
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return dbgen.Subscription{}, err
	}
	id, err := ids.New()
	if err != nil {
		return dbgen.Subscription{}, err
	}
	return queries.InsertSubscription(
		ctx,
		dbgen.InsertSubscriptionParams{
			ID:                    pgUUID(id),
			OrganizationID:        customer.OrganizationID,
			PolarSubscriptionID:   event.SubscriptionID,
			Status:                status,
			CurrentPeriodStartsAt: optionalPGTimestamp(event.CurrentPeriodStart),
			CurrentPeriodEndsAt:   optionalPGTimestamp(event.CurrentPeriodEnd),
			ProviderEventAt:       pgTimestamp(event.EventAt),
		},
	)
}

func polarBillingAuditEventType(
	eventType reliability.WebhookEventType,
) reliability.AuditEventType {
	switch eventType {
	case reliability.WebhookOrderPaid:
		return reliability.AuditSettlementRecorded
	case reliability.WebhookRefundCreated, reliability.WebhookRefundUpdated:
		return reliability.AuditRefundRecorded
	default:
		return reliability.AuditSubscriptionUpdated
	}
}

func processPolarRefund(
	ctx context.Context,
	queries *dbgen.Queries,
	ids IDGenerator,
	item reliability.Item,
	event polarBillingEvent,
) error {
	if event.OrderID == "" || event.ObjectID == "" || event.AmountMicros < 0 ||
		event.Currency != "usd" {
		return reliability.ErrInvalidInput
	}
	cycle, err := queries.GetPolarPaidCycle(ctx, event.OrderID)
	if err != nil {
		return err
	}
	if _, err = queries.LockOrganizationForBillingHistory(
		ctx, cycle.OrganizationID,
	); err != nil {
		return err
	}
	previousReversal := int64(0)
	chargeback := event.Chargeback
	existing, err := queries.GetPolarRefund(ctx, event.ObjectID)
	if err == nil {
		if existing.PolarOrderID != event.OrderID {
			return reliability.ErrIdempotencyConflict
		}
		if existing.ProviderEventAt.Time.After(event.EventAt) {
			return nil
		}
		previousReversal = existing.ReversedMicros
		chargeback = existing.Chargeback || event.Chargeback
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	desired := int64(0)
	if event.Status == "succeeded" || chargeback {
		desired = event.AmountMicros
		if chargeback {
			desired = cycleGrantMicros
		}
		if desired > cycleGrantMicros {
			desired = cycleGrantMicros
		}
	}
	if desired < previousReversal {
		desired = previousReversal
	}
	remaining := cycle.GrantMicros - cycle.ReversedMicros
	delta := desired - previousReversal
	if delta > remaining {
		delta = remaining
		desired = previousReversal + delta
	}
	status := event.Status
	requestedMicros := event.AmountMicros
	if chargeback {
		status = "succeeded"
		if requestedMicros < desired {
			requestedMicros = desired
		}
	}
	if previousReversal > 0 && desired > 0 && status != "succeeded" {
		status = "succeeded"
	}
	if status != "pending" && status != "succeeded" &&
		status != "failed" && status != "canceled" {
		if chargeback {
			status = "succeeded"
		} else {
			return reliability.ErrInvalidInput
		}
	}
	if _, err = queries.UpsertPolarRefund(
		ctx,
		dbgen.UpsertPolarRefundParams{
			PolarRefundID:   event.ObjectID,
			OrganizationID:  cycle.OrganizationID,
			PolarOrderID:    event.OrderID,
			Status:          status,
			RequestedMicros: requestedMicros,
			ReversedMicros:  desired,
			Chargeback:      chargeback,
			ProviderEventAt: pgTimestamp(event.EventAt),
		},
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	if delta == 0 {
		return nil
	}
	if _, err = queries.AddPolarCycleReversal(
		ctx,
		dbgen.AddPolarCycleReversalParams{
			AmountMicros: delta,
			PolarOrderID: event.OrderID,
		},
	); err != nil {
		return err
	}
	runningBalance, err := queries.CurrentOrganizationBalance(ctx, cycle.OrganizationID)
	if err != nil {
		return err
	}
	nextRunningBalance, ok := addInt64(runningBalance, -delta)
	if !ok {
		return reliability.ErrInvalidInput
	}
	settledBalance, err := queries.CurrentSettledCreditBalance(
		ctx, cycle.OrganizationID,
	)
	if err != nil {
		return err
	}
	nextSettledBalance, ok := addInt64(settledBalance, -delta)
	if !ok {
		return reliability.ErrInvalidInput
	}
	ledgerID, err := ids.New()
	if err != nil {
		return err
	}
	source := "polar-refund:" + event.ObjectID + ":" + strconv.FormatInt(desired, 10)
	if _, err = queries.InsertBillingLedgerEntry(
		ctx,
		dbgen.InsertBillingLedgerEntryParams{
			ID:                 pgUUID(ledgerID),
			OrganizationID:     cycle.OrganizationID,
			BillingPeriodID:    cycle.BillingPeriodID,
			EntryType:          "credit_reversal",
			AmountMicros:       -delta,
			BalanceAfterMicros: nextRunningBalance,
			SourceReference:    source,
		},
	); err != nil {
		return err
	}
	shortfallBefore := maxInt64(-settledBalance, 0)
	shortfallAfter := maxInt64(-nextSettledBalance, 0)
	newShortfall := shortfallAfter - shortfallBefore
	if newShortfall > 0 {
		shortfallPeriodID := cycle.BillingPeriodID
		currentPeriod, currentPeriodErr := queries.GetCurrentActiveBillingPeriod(
			ctx, cycle.OrganizationID,
		)
		if currentPeriodErr == nil {
			shortfallPeriodID = currentPeriod.ID
		} else if !errors.Is(currentPeriodErr, pgx.ErrNoRows) {
			return currentPeriodErr
		}
		shortfallID, idErr := ids.New()
		if idErr != nil {
			return idErr
		}
		_, err = queries.InsertBillingShortfall(
			ctx,
			dbgen.InsertBillingShortfallParams{
				ID:              pgUUID(shortfallID),
				OrganizationID:  cycle.OrganizationID,
				BillingPeriodID: shortfallPeriodID,
				PolarRefundID:   event.ObjectID,
				SourceReference: "polar-event:" + item.ID.String(),
				AmountMicros:    newShortfall,
			},
		)
	}
	return err
}

func resolvePolarCustomer(
	ctx context.Context,
	queries *dbgen.Queries,
	event polarBillingEvent,
) (dbgen.PolarCustomer, error) {
	if event.ExternalID != "" {
		externalID, err := uuid.Parse(event.ExternalID)
		if err != nil || externalID.Version() != 7 ||
			externalID.String() != event.ExternalID {
			return dbgen.PolarCustomer{}, reliability.ErrInvalidInput
		}
		customer, err := queries.GetPolarCustomerByExternalID(
			ctx, pgUUID(externalID),
		)
		if err != nil {
			return dbgen.PolarCustomer{}, err
		}
		if event.CustomerID != "" && customer.PolarCustomerID != event.CustomerID {
			return dbgen.PolarCustomer{}, reliability.ErrInvalidInput
		}
		return customer, nil
	}
	if event.CustomerID == "" {
		return dbgen.PolarCustomer{}, reliability.ErrInvalidInput
	}
	return queries.GetPolarCustomerByProviderID(ctx, event.CustomerID)
}

func polarSubscriptionStatus(
	eventType reliability.WebhookEventType,
	status string,
) string {
	switch eventType {
	case reliability.WebhookOrderPaid:
		return "active"
	case reliability.WebhookSubscriptionActive:
		return "active"
	case reliability.WebhookSubscriptionUncanceled:
		return "active"
	case reliability.WebhookSubscriptionPastDue:
		return "past_due"
	case reliability.WebhookSubscriptionCanceled:
		return "canceled"
	case reliability.WebhookSubscriptionRevoked:
		return "revoked"
	}
	switch status {
	case "active", "trialing":
		return "active"
	case "past_due":
		return "past_due"
	case "canceled":
		return "canceled"
	case "revoked", "unpaid", "incomplete_expired":
		return "revoked"
	case "incomplete", "pending":
		return "pending"
	default:
		return ""
	}
}

func pgTimestamp(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value.UTC(), Valid: true}
}

func optionalPGTimestamp(value time.Time) pgtype.Timestamptz {
	if value.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgTimestamp(value)
}

func addInt64(left, right int64) (int64, bool) {
	if (right > 0 && left > math.MaxInt64-right) ||
		(right < 0 && left < math.MinInt64-right) {
		return 0, false
	}
	return left + right, true
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}

func settledCreditForfeiture(
	runningBalance int64,
	settledCreditBalance int64,
) (amountMicros int64, balanceAfterMicros int64, ok bool) {
	if settledCreditBalance <= 0 {
		return 0, runningBalance, true
	}
	balanceAfterMicros, ok = addInt64(runningBalance, -settledCreditBalance)
	return settledCreditBalance, balanceAfterMicros, ok
}
