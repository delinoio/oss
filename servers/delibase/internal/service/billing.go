package service

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"time"

	"connectrpc.com/connect"
	delibasev1 "github.com/delinoio/oss/protos/delibase/gen/go/delibase/v1"
	"github.com/delinoio/oss/servers/delibase/internal/contracts"
	"github.com/delinoio/oss/servers/delibase/internal/database/dbgen"
	"github.com/delinoio/oss/servers/delibase/internal/reliability"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (service *Billing) GetBillingSummary(
	ctx context.Context,
	request *connect.Request[delibasev1.GetBillingSummaryRequest],
) (*connect.Response[delibasev1.GetBillingSummaryResponse], error) {
	subject, err := userSubject(ctx)
	if err != nil {
		return nil, err
	}
	if request == nil || request.Msg == nil {
		return nil, invalidArgument()
	}
	organizationID, err := parseUUIDv7(request.Msg.OrganizationId)
	if err != nil {
		return nil, err
	}
	var response *delibasev1.GetBillingSummaryResponse
	err = service.dependencies.Store.WithinTransaction(
		ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly},
		func(queries *dbgen.Queries) error {
			account, transactionErr := activeAccount(ctx, queries, subject)
			if transactionErr != nil {
				return transactionErr
			}
			access, transactionErr := billingAccess(
				ctx, queries, organizationID, account.ID, false,
			)
			if transactionErr != nil {
				return transactionErr
			}
			row, transactionErr := queries.GetBillingSummary(
				ctx, pgUUID(organizationID),
			)
			if transactionErr != nil {
				return databaseError(transactionErr)
			}
			summary := billingSummaryMessage(row)
			if access.Role == "member" {
				summary = memberBillingSummary(summary)
			}
			response = &delibasev1.GetBillingSummaryResponse{Summary: summary}
			return nil
		},
	)
	if err != nil {
		return nil, databaseError(err)
	}
	return connect.NewResponse(response), nil
}

func (service *Billing) CreateSubscriptionCheckout(
	ctx context.Context,
	request *connect.Request[delibasev1.CreateSubscriptionCheckoutRequest],
) (*connect.Response[delibasev1.CreateSubscriptionCheckoutResponse], error) {
	subject, err := userSubject(ctx)
	if err != nil {
		return nil, err
	}
	if request == nil || request.Msg == nil ||
		!validReturnURL(request.Msg.SuccessUrl) ||
		!validReturnURL(request.Msg.CancelUrl) {
		return nil, invalidArgument()
	}
	organizationID, err := parseUUIDv7(request.Msg.OrganizationId)
	if err != nil {
		return nil, err
	}
	key, err := validateIdempotency(request.Msg.Idempotency)
	if err != nil {
		return nil, err
	}
	digest := requestDigest(
		organizationID.String(), request.Msg.SuccessUrl, request.Msg.CancelUrl,
	)
	replayed := &delibasev1.CreateSubscriptionCheckoutResponse{}
	found, err := service.billingReplay(
		ctx, subject, organizationID, "create_subscription_checkout",
		key, digest, replayed,
	)
	if err != nil {
		return nil, err
	}
	if found {
		setIdempotency(
			&replayed.Idempotency,
			delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_CREATE_SUBSCRIPTION_CHECKOUT,
			true,
			replayed.Idempotency.GetOriginallyCompletedAt().AsTime(),
		)
		return connect.NewResponse(replayed), nil
	}
	if err := service.ensureSubscriptionCheckoutAvailable(
		ctx, subject, organizationID,
	); err != nil {
		return nil, err
	}
	if service.dependencies.Polar == nil {
		return nil, serviceError(
			connect.CodeUnavailable,
			delibasev1.ErrorReason_ERROR_REASON_UNSPECIFIED,
		)
	}
	checkout, err := service.dependencies.Polar.CreateCheckout(
		ctx,
		contracts.CheckoutRequest{
			OrganizationID: organizationID.String(),
			SuccessURL:     request.Msg.SuccessUrl,
			CancelURL:      request.Msg.CancelUrl,
			IdempotencyKey: providerIdempotencyKey(
				"create_subscription_checkout", subject, organizationID, key,
			),
		},
	)
	if err != nil {
		// No local write occurs before Polar returns a valid hosted checkout.
		return nil, serviceError(
			connect.CodeUnavailable,
			delibasev1.ErrorReason_ERROR_REASON_UNSPECIFIED,
		)
	}
	response := &delibasev1.CreateSubscriptionCheckoutResponse{
		CheckoutUrl: checkout.URL,
		ExpiresAt:   timestampFromTime(checkout.ExpiresAt),
	}
	err = service.persistBillingMutation(
		ctx, subject, organizationID, "create_subscription_checkout", key, digest,
		delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_CREATE_SUBSCRIPTION_CHECKOUT,
		reliability.AuditCheckoutCreated, response, &response.Idempotency,
	)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(response), nil
}

func (service *Billing) CreateBillingPortalSession(
	ctx context.Context,
	request *connect.Request[delibasev1.CreateBillingPortalSessionRequest],
) (*connect.Response[delibasev1.CreateBillingPortalSessionResponse], error) {
	subject, err := userSubject(ctx)
	if err != nil {
		return nil, err
	}
	if request == nil || request.Msg == nil || !validReturnURL(request.Msg.ReturnUrl) {
		return nil, invalidArgument()
	}
	organizationID, err := parseUUIDv7(request.Msg.OrganizationId)
	if err != nil {
		return nil, err
	}
	key, err := validateIdempotency(request.Msg.Idempotency)
	if err != nil {
		return nil, err
	}
	digest := requestDigest(organizationID.String(), request.Msg.ReturnUrl)
	replayed := &delibasev1.CreateBillingPortalSessionResponse{}
	found, err := service.billingReplay(
		ctx, subject, organizationID, "create_billing_portal_session",
		key, digest, replayed,
	)
	if err != nil {
		return nil, err
	}
	if found {
		setIdempotency(
			&replayed.Idempotency,
			delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_CREATE_BILLING_PORTAL_SESSION,
			true,
			replayed.Idempotency.GetOriginallyCompletedAt().AsTime(),
		)
		return connect.NewResponse(replayed), nil
	}
	if service.dependencies.Polar == nil {
		return nil, serviceError(connect.CodeUnavailable, 0)
	}
	session, err := service.dependencies.Polar.CreatePortalSession(
		ctx,
		contracts.PortalRequest{
			OrganizationID: organizationID.String(),
			ReturnURL:      request.Msg.ReturnUrl,
			IdempotencyKey: providerIdempotencyKey(
				"create_billing_portal_session", subject, organizationID, key,
			),
		},
	)
	if err != nil {
		return nil, serviceError(connect.CodeUnavailable, 0)
	}
	response := &delibasev1.CreateBillingPortalSessionResponse{
		PortalUrl: session.URL,
		ExpiresAt: timestampFromTime(session.ExpiresAt),
	}
	err = service.persistBillingMutation(
		ctx, subject, organizationID, "create_billing_portal_session", key, digest,
		delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_CREATE_BILLING_PORTAL_SESSION,
		reliability.AuditCheckoutCreated, response, &response.Idempotency,
	)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(response), nil
}

func (service *Billing) UpdateOverageLimit(
	ctx context.Context,
	request *connect.Request[delibasev1.UpdateOverageLimitRequest],
) (*connect.Response[delibasev1.UpdateOverageLimitResponse], error) {
	subject, err := userSubject(ctx)
	if err != nil {
		return nil, err
	}
	if request == nil || request.Msg == nil || request.Msg.MonthlyLimit == nil ||
		request.Msg.MonthlyLimit.Value < 0 {
		return nil, serviceError(
			connect.CodeInvalidArgument,
			delibasev1.ErrorReason_ERROR_REASON_OVERAGE_LIMIT_INVALID,
		)
	}
	organizationID, err := parseUUIDv7(request.Msg.OrganizationId)
	if err != nil {
		return nil, err
	}
	key, err := validateIdempotency(request.Msg.Idempotency)
	if err != nil {
		return nil, err
	}
	limit := request.Msg.MonthlyLimit.Value
	digest := requestDigest(organizationID.String(), strconv.FormatInt(limit, 10))
	actor, err := actorFor(service.dependencies, subject)
	if err != nil {
		return nil, err
	}
	var response *delibasev1.UpdateOverageLimitResponse
	err = service.dependencies.Store.WithinTransaction(
		ctx, pgx.TxOptions{},
		func(queries *dbgen.Queries) error {
			response = &delibasev1.UpdateOverageLimitResponse{}
			account, replayed, completedAt, transactionErr := replayWithActiveAccount(
				ctx, queries, subject, "update_overage_limit", key, digest, response,
			)
			if transactionErr != nil {
				return transactionErr
			}
			if replayed {
				setIdempotency(
					&response.Idempotency,
					delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_UPDATE_OVERAGE_LIMIT,
					true, completedAt,
				)
				return nil
			}
			if _, transactionErr = billingAccess(
				ctx, queries, organizationID, account.ID, true,
			); transactionErr != nil {
				return transactionErr
			}
			if _, transactionErr = queries.LockOrganizationForBilling(
				ctx, pgUUID(organizationID),
			); transactionErr != nil {
				return databaseError(transactionErr)
			}
			if _, transactionErr = queries.UpdateOrganizationOverageLimit(
				ctx,
				dbgen.UpdateOrganizationOverageLimitParams{
					OverageLimitMicros: limit,
					OrganizationID:     pgUUID(organizationID),
				},
			); transactionErr != nil {
				return databaseError(transactionErr)
			}
			if _, transactionErr = queries.UpdateCurrentBillingPeriodOverageLimit(
				ctx,
				dbgen.UpdateCurrentBillingPeriodOverageLimitParams{
					OverageLimitMicros: limit,
					OrganizationID:     pgUUID(organizationID),
				},
			); transactionErr != nil {
				return databaseError(transactionErr)
			}
			row, transactionErr := queries.GetBillingSummary(
				ctx, pgUUID(organizationID),
			)
			if transactionErr != nil {
				return databaseError(transactionErr)
			}
			response.Summary = billingSummaryMessage(row)
			if transactionErr = appendAudit(
				ctx, service.dependencies, queries,
				reliability.AuditBillingLimitUpdated, actor, organizationID,
			); transactionErr != nil {
				return transactionErr
			}
			completedAt = service.dependencies.Clock.Now().UTC()
			setIdempotency(
				&response.Idempotency,
				delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_UPDATE_OVERAGE_LIMIT,
				false, completedAt,
			)
			_, transactionErr = persistIdempotency(
				ctx, service.dependencies, queries, subject, "update_overage_limit",
				key, digest, response,
			)
			return transactionErr
		},
	)
	if err != nil {
		return nil, databaseError(err)
	}
	return connect.NewResponse(response), nil
}

func (service *Billing) ListLedgerEntries(
	ctx context.Context,
	request *connect.Request[delibasev1.ListLedgerEntriesRequest],
) (*connect.Response[delibasev1.ListLedgerEntriesResponse], error) {
	subject, err := userSubject(ctx)
	if err != nil {
		return nil, err
	}
	if request == nil || request.Msg == nil {
		return nil, invalidArgument()
	}
	organizationID, err := parseUUIDv7(request.Msg.OrganizationId)
	if err != nil {
		return nil, err
	}
	entryType, valid := ledgerOperationName(request.Msg.Operation)
	if !valid {
		return nil, invalidArgument()
	}
	pageSize, afterID, err := page(request.Msg.Page)
	if err != nil {
		return nil, err
	}
	from, to, err := billingTimeRange(request.Msg.FromTime, request.Msg.ToTime)
	if err != nil {
		return nil, err
	}
	var response *delibasev1.ListLedgerEntriesResponse
	err = service.dependencies.Store.WithinTransaction(
		ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly},
		func(queries *dbgen.Queries) error {
			account, transactionErr := activeAccount(ctx, queries, subject)
			if transactionErr != nil {
				return transactionErr
			}
			if _, transactionErr = billingAccess(
				ctx, queries, organizationID, account.ID, true,
			); transactionErr != nil {
				return transactionErr
			}
			rows, transactionErr := queries.ListLedgerEntries(
				ctx,
				dbgen.ListLedgerEntriesParams{
					OrganizationID: pgUUID(organizationID),
					EntryType:      entryType,
					FromTime:       pgtype.Timestamptz{Time: from, Valid: true},
					ToTime:         pgtype.Timestamptz{Time: to, Valid: true},
					AfterID:        afterID,
					PageLimit:      pageSize + 1,
				},
			)
			if transactionErr != nil {
				return databaseError(transactionErr)
			}
			next := ""
			if len(rows) > int(pageSize) {
				next = nextCursor(rows[pageSize-1].ID)
				rows = rows[:pageSize]
			}
			entries := make([]*delibasev1.LedgerEntry, 0, len(rows))
			for _, row := range rows {
				entries = append(entries, ledgerEntryMessage(row))
			}
			response = &delibasev1.ListLedgerEntriesResponse{
				Entries: entries,
				Page:    &delibasev1.PageResponse{NextCursor: next},
			}
			return nil
		},
	)
	if err != nil {
		return nil, databaseError(err)
	}
	return connect.NewResponse(response), nil
}

func (service *Billing) billingReplay(
	ctx context.Context,
	subject string,
	organizationID uuid.UUID,
	operation string,
	key string,
	digest []byte,
	target proto.Message,
) (bool, error) {
	var found bool
	err := service.dependencies.Store.WithinTransaction(
		ctx, pgx.TxOptions{},
		func(queries *dbgen.Queries) error {
			account, transactionErr := activeAccount(ctx, queries, subject)
			if transactionErr != nil {
				return transactionErr
			}
			if _, transactionErr = billingAccess(
				ctx, queries, organizationID, account.ID, true,
			); transactionErr != nil {
				return transactionErr
			}
			found, _, transactionErr = replay(
				ctx, queries, subject, operation, key, digest, target,
			)
			return transactionErr
		},
	)
	return found, databaseError(err)
}

func (service *Billing) ensureSubscriptionCheckoutAvailable(
	ctx context.Context,
	subject string,
	organizationID uuid.UUID,
) error {
	err := service.dependencies.Store.WithinTransaction(
		ctx, pgx.TxOptions{},
		func(queries *dbgen.Queries) error {
			account, transactionErr := activeAccount(ctx, queries, subject)
			if transactionErr != nil {
				return transactionErr
			}
			if _, transactionErr = billingAccess(
				ctx, queries, organizationID, account.ID, true,
			); transactionErr != nil {
				return transactionErr
			}
			if _, transactionErr = queries.LockOrganizationForBilling(
				ctx, pgUUID(organizationID),
			); transactionErr != nil {
				return transactionErr
			}
			if _, transactionErr = queries.GetActiveSubscriptionForOrganization(
				ctx, pgUUID(organizationID),
			); transactionErr == nil {
				return serviceError(
					connect.CodeAlreadyExists,
					delibasev1.ErrorReason_ERROR_REASON_RESOURCE_CONFLICT,
				)
			} else if !errors.Is(transactionErr, pgx.ErrNoRows) {
				return transactionErr
			}
			return nil
		},
	)
	return databaseError(err)
}

func (service *Billing) persistBillingMutation(
	ctx context.Context,
	subject string,
	organizationID uuid.UUID,
	operation string,
	key string,
	digest []byte,
	idempotentOperation delibasev1.IdempotentOperation,
	auditEvent reliability.AuditEventType,
	response proto.Message,
	result **delibasev1.IdempotencyResult,
) error {
	actor, err := actorFor(service.dependencies, subject)
	if err != nil {
		return err
	}
	err = service.dependencies.Store.WithinTransaction(
		ctx, pgx.TxOptions{},
		func(queries *dbgen.Queries) error {
			account, transactionErr := activeAccount(ctx, queries, subject)
			if transactionErr != nil {
				return transactionErr
			}
			if _, transactionErr = billingAccess(
				ctx, queries, organizationID, account.ID, true,
			); transactionErr != nil {
				return transactionErr
			}
			replayed, completedAt, transactionErr := replay(
				ctx, queries, subject, operation, key, digest, response,
			)
			if transactionErr != nil {
				return transactionErr
			}
			if replayed {
				setIdempotency(result, idempotentOperation, true, completedAt)
				return nil
			}
			if transactionErr = appendAudit(
				ctx, service.dependencies, queries, auditEvent, actor, organizationID,
			); transactionErr != nil {
				return transactionErr
			}
			completedAt = service.dependencies.Clock.Now().UTC()
			setIdempotency(result, idempotentOperation, false, completedAt)
			_, transactionErr = persistIdempotency(
				ctx, service.dependencies, queries, subject, operation, key,
				digest, response,
			)
			return transactionErr
		},
	)
	return databaseError(err)
}

func billingAccess(
	ctx context.Context,
	queries *dbgen.Queries,
	organizationID uuid.UUID,
	accountID pgtype.UUID,
	adminRequired bool,
) (dbgen.GetBillingAccessRow, error) {
	row, err := queries.GetBillingAccess(
		ctx,
		dbgen.GetBillingAccessParams{
			OrganizationID: pgUUID(organizationID),
			AccountID:      accountID,
		},
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return dbgen.GetBillingAccessRow{}, serviceError(
			connect.CodePermissionDenied,
			delibasev1.ErrorReason_ERROR_REASON_ORGANIZATION_MEMBERSHIP_REQUIRED,
		)
	}
	if err != nil {
		return dbgen.GetBillingAccessRow{}, databaseError(err)
	}
	if adminRequired && row.Role != "owner" && row.Role != "admin" {
		return dbgen.GetBillingAccessRow{}, serviceError(
			connect.CodePermissionDenied,
			delibasev1.ErrorReason_ERROR_REASON_ADMIN_ROLE_REQUIRED,
		)
	}
	return row, nil
}

func billingSummaryMessage(row dbgen.GetBillingSummaryRow) *delibasev1.BillingSummary {
	summary := &delibasev1.BillingSummary{
		OrganizationId:         uuidMessage(row.OrganizationID),
		SubscriptionStatus:     subscriptionStatus(row.SubscriptionStatus),
		AvailableCredit:        &delibasev1.UsdMicros{Value: row.AvailableCreditMicros},
		HeldCredit:             &delibasev1.UsdMicros{Value: row.HeldCreditMicros},
		CommittedOverage:       &delibasev1.UsdMicros{Value: row.CommittedOverageMicros},
		HeldOverage:            &delibasev1.UsdMicros{Value: row.HeldOverageMicros},
		MonthlyOverageLimit:    &delibasev1.UsdMicros{Value: row.MonthlyOverageLimitMicros},
		OverageLimitConfigured: row.OverageLimitConfigured,
		NewOverageAllowed:      row.NewOverageAllowed,
	}
	if row.BillingPeriodID.Valid {
		summary.CurrentPeriod = &delibasev1.BillingPeriod{
			BillingPeriodId: uuidMessage(row.BillingPeriodID),
			Status:          delibasev1.BillingPeriodStatus_BILLING_PERIOD_STATUS_OPEN,
			StartsAt:        timestamp(row.StartsAt),
			EndsAt:          timestamp(row.EndsAt),
		}
	}
	return summary
}

func memberBillingSummary(summary *delibasev1.BillingSummary) *delibasev1.BillingSummary {
	return &delibasev1.BillingSummary{
		OrganizationId:  summary.OrganizationId,
		AvailableCredit: summary.AvailableCredit,
	}
}

func subscriptionStatus(value string) delibasev1.SubscriptionStatus {
	switch value {
	case "none":
		return delibasev1.SubscriptionStatus_SUBSCRIPTION_STATUS_NONE
	case "pending":
		return delibasev1.SubscriptionStatus_SUBSCRIPTION_STATUS_CHECKOUT_PENDING
	case "active":
		return delibasev1.SubscriptionStatus_SUBSCRIPTION_STATUS_ACTIVE
	case "past_due":
		return delibasev1.SubscriptionStatus_SUBSCRIPTION_STATUS_PAST_DUE
	case "canceled":
		return delibasev1.SubscriptionStatus_SUBSCRIPTION_STATUS_CANCELED
	case "revoked":
		return delibasev1.SubscriptionStatus_SUBSCRIPTION_STATUS_REVOKED
	default:
		return delibasev1.SubscriptionStatus_SUBSCRIPTION_STATUS_UNSPECIFIED
	}
}

func ledgerOperationName(value delibasev1.LedgerOperation) (string, bool) {
	switch value {
	case delibasev1.LedgerOperation_LEDGER_OPERATION_UNSPECIFIED:
		return "", true
	case delibasev1.LedgerOperation_LEDGER_OPERATION_CREDIT_GRANT:
		return "credit_grant", true
	case delibasev1.LedgerOperation_LEDGER_OPERATION_CREDIT_REVERSAL:
		return "credit_reversal", true
	case delibasev1.LedgerOperation_LEDGER_OPERATION_CREDIT_HOLD:
		return "credit_hold", true
	case delibasev1.LedgerOperation_LEDGER_OPERATION_CREDIT_COMMIT:
		return "credit_commit", true
	case delibasev1.LedgerOperation_LEDGER_OPERATION_CREDIT_RELEASE:
		return "credit_release", true
	case delibasev1.LedgerOperation_LEDGER_OPERATION_OVERAGE_HOLD:
		return "overage_hold", true
	case delibasev1.LedgerOperation_LEDGER_OPERATION_OVERAGE_COMMIT:
		return "overage_commit", true
	case delibasev1.LedgerOperation_LEDGER_OPERATION_OVERAGE_RELEASE:
		return "overage_release", true
	case delibasev1.LedgerOperation_LEDGER_OPERATION_CREDIT_FORFEITURE:
		return "credit_forfeiture", true
	default:
		return "", false
	}
}

func ledgerEntryMessage(row dbgen.LedgerEntry) *delibasev1.LedgerEntry {
	operation, _ := ledgerOperationFromName(row.EntryType)
	return &delibasev1.LedgerEntry{
		LedgerEntryId:    uuidMessage(row.ID),
		OrganizationId:   uuidMessage(row.OrganizationID),
		Operation:        operation,
		Amount:           &delibasev1.UsdMicros{Value: row.AmountMicros},
		BalanceAfter:     &delibasev1.UsdMicros{Value: row.BalanceAfterMicros},
		BillingPeriodId:  uuidMessage(row.BillingPeriodID),
		ReservationId:    uuidMessage(row.ReservationID),
		UsageRecordId:    uuidMessage(row.UsageRecordID),
		TeamIdSnapshot:   uuidMessage(row.TeamIDSnapshot),
		TeamNameSnapshot: row.TeamNameSnapshot.String,
		CreatedAt:        timestamp(row.CreatedAt),
	}
}

func ledgerOperationFromName(value string) (delibasev1.LedgerOperation, bool) {
	for operation := delibasev1.LedgerOperation(1); operation <= delibasev1.LedgerOperation_LEDGER_OPERATION_CREDIT_FORFEITURE; operation++ {
		if name, ok := ledgerOperationName(operation); ok && name == value {
			return operation, true
		}
	}
	return delibasev1.LedgerOperation_LEDGER_OPERATION_UNSPECIFIED, false
}

func billingTimeRange(
	fromValue, toValue *timestamppb.Timestamp,
) (time.Time, time.Time, error) {
	from := time.Unix(0, 0).UTC()
	to := time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
	if fromValue != nil {
		if err := fromValue.CheckValid(); err != nil {
			return time.Time{}, time.Time{}, invalidArgument()
		}
		from = fromValue.AsTime().UTC()
	}
	if toValue != nil {
		if err := toValue.CheckValid(); err != nil {
			return time.Time{}, time.Time{}, invalidArgument()
		}
		to = toValue.AsTime().UTC()
	}
	if !from.Before(to) {
		return time.Time{}, time.Time{}, invalidArgument()
	}
	return from, to, nil
}

func validReturnURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" &&
		parsed.User == nil && parsed.Fragment == "" && len(value) <= 2083
}

func timestampFromTime(value time.Time) *timestamppb.Timestamp {
	if value.IsZero() {
		return nil
	}
	return timestamppb.New(value.UTC())
}
