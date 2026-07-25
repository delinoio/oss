package service

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"

	"connectrpc.com/connect"
	delibasev1 "github.com/delinoio/oss/protos/delibase/gen/go/delibase/v1"
	"github.com/delinoio/oss/servers/delibase/internal/database/dbgen"
	"github.com/delinoio/oss/servers/delibase/internal/reliability"
	"github.com/delinoio/oss/servers/internal/auth"
	"github.com/delinoio/oss/servers/internal/redact"
	"github.com/delinoio/oss/servers/internal/requestmeta"
	"github.com/delinoio/oss/servers/internal/safelog"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const usageExpirationBatchSize int32 = 100

func (service *Usage) ReserveUsage(
	ctx context.Context,
	request *connect.Request[delibasev1.ReserveUsageRequest],
) (*connect.Response[delibasev1.ReserveUsageResponse], error) {
	serviceClientID, subject, err := usageSubjects(ctx)
	if err != nil {
		return nil, err
	}
	if request == nil || request.Msg == nil || request.Msg.MaximumUnits == nil {
		return nil, invalidArgument()
	}
	organizationID, err := parseUUIDv7(request.Msg.OrganizationId)
	if err != nil {
		return nil, err
	}
	teamID, err := parseUUIDv7(request.Msg.TeamId)
	if err != nil {
		return nil, err
	}
	meterID, err := parseUUIDv7(request.Msg.MeterId)
	if err != nil {
		return nil, err
	}
	maximumUnits := request.Msg.MaximumUnits.Value
	if maximumUnits < 0 {
		return nil, serviceError(
			connect.CodeInvalidArgument,
			delibasev1.ErrorReason_ERROR_REASON_RESERVATION_UNITS_NEGATIVE,
		)
	}
	if maximumUnits == 0 {
		return nil, serviceError(
			connect.CodeInvalidArgument,
			delibasev1.ErrorReason_ERROR_REASON_USAGE_UNITS_INVALID,
		)
	}
	clientReference, err := validateClientReference(request.Msg.ClientReference)
	if err != nil {
		return nil, err
	}
	key, err := validateIdempotency(request.Msg.Idempotency)
	if err != nil {
		return nil, err
	}
	digest := requestDigest(
		organizationID.String(),
		teamID.String(),
		meterID.String(),
		strconv.FormatInt(maximumUnits, 10),
		clientReference,
	)
	actor, err := actorFor(service.dependencies, subject)
	if err != nil {
		return nil, err
	}

	var response *delibasev1.ReserveUsageResponse
	err = service.dependencies.Store.WithinTransaction(
		ctx,
		pgx.TxOptions{},
		func(queries *dbgen.Queries) error {
			response = &delibasev1.ReserveUsageResponse{}
			serviceIdentity, transactionErr := usageServiceIdentity(
				ctx, queries, serviceClientID,
			)
			if transactionErr != nil {
				return transactionErr
			}
			callerID := usageCallerID(serviceIdentity.ID)
			replayed, completedAt, transactionErr := replayForCaller(
				ctx, queries, "service", callerID, "reserve_usage", key, digest,
				response,
			)
			if transactionErr != nil {
				return transactionErr
			}
			if replayed {
				setIdempotency(
					&response.Idempotency,
					delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_RESERVE_USAGE,
					true,
					completedAt,
				)
				return nil
			}
			if !serviceIdentity.Enabled {
				return serviceMeterNotAllowed()
			}
			if _, transactionErr = queries.LockOrganizationForBilling(
				ctx, pgUUID(organizationID),
			); transactionErr != nil {
				return usageMembershipRequired(transactionErr)
			}
			replayed, completedAt, transactionErr = replayForCaller(
				ctx, queries, "service", callerID, "reserve_usage", key, digest,
				response,
			)
			if transactionErr != nil {
				return transactionErr
			}
			if replayed {
				setIdempotency(
					&response.Idempotency,
					delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_RESERVE_USAGE,
					true,
					completedAt,
				)
				return nil
			}
			if _, transactionErr = expireOrganizationReservations(
				ctx, service.dependencies, queries, organizationID,
			); transactionErr != nil {
				return transactionErr
			}
			account, team, transactionErr := authorizeUsageTeam(
				ctx, queries, subject, organizationID, teamID,
			)
			if transactionErr != nil {
				return transactionErr
			}
			meter, transactionErr := queries.GetUsageMeterAuthorization(
				ctx,
				dbgen.GetUsageMeterAuthorizationParams{
					ServiceIdentityID: serviceIdentity.ID,
					MeterID:           pgUUID(meterID),
				},
			)
			if errors.Is(transactionErr, pgx.ErrNoRows) {
				return serviceMeterNotAllowed()
			}
			if transactionErr != nil {
				return databaseError(transactionErr)
			}
			maximumCost, ok := multiplyNonnegativeInt64(
				maximumUnits, meter.UsdMicrosPerUnit,
			)
			if !ok {
				return serviceError(
					connect.CodeInvalidArgument,
					delibasev1.ErrorReason_ERROR_REASON_MONEY_OVERFLOW,
				)
			}
			capacity, transactionErr := queries.GetUsageCapacity(
				ctx,
				dbgen.GetUsageCapacityParams{
					ReservationTtlSeconds: meter.ReservationTtlSeconds,
					OrganizationID:        pgUUID(organizationID),
				},
			)
			if transactionErr != nil {
				return databaseError(transactionErr)
			}
			heldCredit := minInt64(maximumCost, capacity.AvailableCreditMicros)
			heldOverage := maximumCost - heldCredit
			if heldOverage > capacity.AvailableOverageMicros ||
				(heldOverage > 0 && !capacity.OverageTtlAllowed) {
				return usageCapacityError(capacity)
			}
			reservationID, transactionErr := service.dependencies.IDs.New()
			if transactionErr != nil {
				return serviceError(connect.CodeInternal, 0)
			}
			reservation, transactionErr := queries.InsertUsageReservation(
				ctx,
				dbgen.InsertUsageReservationParams{
					ID:                         pgUUID(reservationID),
					OrganizationID:             pgUUID(organizationID),
					TeamID:                     pgUUID(teamID),
					TeamNameSnapshot:           team.Name,
					MeterID:                    pgUUID(meterID),
					PriceVersionID:             meter.PriceVersionID,
					AccountID:                  account.ID,
					ServiceIdentityID:          serviceIdentity.ID,
					MaximumUnits:               maximumUnits,
					UsdMicrosPerUnit:           meter.UsdMicrosPerUnit,
					MaximumCostMicros:          maximumCost,
					HeldCreditMicros:           heldCredit,
					HeldOverageMicros:          heldOverage,
					ClientReference:            clientReference,
					ReservationTtlSeconds:      meter.ReservationTtlSeconds,
					UserActorReferenceSnapshot: string(actor),
				},
			)
			if transactionErr != nil {
				return databaseError(transactionErr)
			}
			if transactionErr = appendUsageLedger(
				ctx, service.dependencies, queries, reservation,
				"credit_hold", -heldCredit, uuid.Nil,
				"reservation:"+reservationID.String()+":credit-hold", actor,
			); transactionErr != nil {
				return transactionErr
			}
			if transactionErr = appendUsageLedger(
				ctx, service.dependencies, queries, reservation,
				"overage_hold", -heldOverage, uuid.Nil,
				"reservation:"+reservationID.String()+":overage-hold", actor,
			); transactionErr != nil {
				return transactionErr
			}
			if transactionErr = appendUsageAudit(
				ctx, service.dependencies, queries,
				reliability.AuditReservationCreated, actor, reservation,
			); transactionErr != nil {
				return transactionErr
			}
			completedAt = service.dependencies.Clock.Now().UTC()
			response.Reservation = usageReservationMessage(reservation)
			setIdempotency(
				&response.Idempotency,
				delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_RESERVE_USAGE,
				false,
				completedAt,
			)
			_, transactionErr = persistIdempotencyForCaller(
				ctx, service.dependencies, queries, "service", callerID,
				"reserve_usage", key, digest, response,
			)
			return transactionErr
		},
	)
	if err != nil {
		return nil, databaseError(err)
	}
	return connect.NewResponse(response), nil
}

func (service *Usage) CommitUsage(
	ctx context.Context,
	request *connect.Request[delibasev1.CommitUsageRequest],
) (*connect.Response[delibasev1.CommitUsageResponse], error) {
	serviceClientID, subject, err := usageSubjects(ctx)
	if err != nil {
		return nil, err
	}
	if request == nil || request.Msg == nil || request.Msg.ActualUnits == nil {
		return nil, invalidArgument()
	}
	organizationID, err := parseUUIDv7(request.Msg.OrganizationId)
	if err != nil {
		return nil, err
	}
	reservationID, err := parseUUIDv7(request.Msg.ReservationId)
	if err != nil {
		return nil, err
	}
	actualUnits := request.Msg.ActualUnits.Value
	if actualUnits < 0 {
		return nil, serviceError(
			connect.CodeInvalidArgument,
			delibasev1.ErrorReason_ERROR_REASON_USAGE_UNITS_INVALID,
		)
	}
	key, err := validateIdempotency(request.Msg.Idempotency)
	if err != nil {
		return nil, err
	}
	digest := requestDigest(
		organizationID.String(),
		reservationID.String(),
		strconv.FormatInt(actualUnits, 10),
	)
	actor, err := actorFor(service.dependencies, subject)
	if err != nil {
		return nil, err
	}

	var response *delibasev1.CommitUsageResponse
	var committedBusinessError error
	err = service.dependencies.Store.WithinTransaction(
		ctx,
		pgx.TxOptions{},
		func(queries *dbgen.Queries) error {
			response = &delibasev1.CommitUsageResponse{}
			serviceIdentity, transactionErr := usageServiceIdentity(
				ctx, queries, serviceClientID,
			)
			if transactionErr != nil {
				return transactionErr
			}
			callerID := usageCallerID(serviceIdentity.ID)
			replayed, completedAt, transactionErr := replayForCaller(
				ctx, queries, "service", callerID, "commit_usage", key, digest,
				response,
			)
			if transactionErr != nil {
				return transactionErr
			}
			if replayed {
				setIdempotency(
					&response.Idempotency,
					delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_COMMIT_USAGE,
					true,
					completedAt,
				)
				return nil
			}
			if _, transactionErr = queries.LockOrganizationForBilling(
				ctx, pgUUID(organizationID),
			); transactionErr != nil {
				return usageMembershipRequired(transactionErr)
			}
			replayed, completedAt, transactionErr = replayForCaller(
				ctx, queries, "service", callerID, "commit_usage", key, digest,
				response,
			)
			if transactionErr != nil {
				return transactionErr
			}
			if replayed {
				setIdempotency(
					&response.Idempotency,
					delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_COMMIT_USAGE,
					true,
					completedAt,
				)
				return nil
			}
			if _, transactionErr = expireOrganizationReservations(
				ctx, service.dependencies, queries, organizationID,
			); transactionErr != nil {
				return transactionErr
			}
			reservation, transactionErr := queries.LockUsageReservation(
				ctx,
				dbgen.LockUsageReservationParams{
					OrganizationID: pgUUID(organizationID),
					ReservationID:  pgUUID(reservationID),
				},
			)
			if errors.Is(transactionErr, pgx.ErrNoRows) {
				return reservationNotFound()
			}
			if transactionErr != nil {
				return databaseError(transactionErr)
			}
			if reservation.ServiceIdentityID != serviceIdentity.ID {
				return reservationNotFound()
			}
			account, transactionErr := usageAccount(ctx, queries, subject)
			if transactionErr != nil {
				return transactionErr
			}
			if account.ID != reservation.AccountID {
				return teamAccessDenied()
			}
			if reservation.Status != "held" {
				committedBusinessError = reservationStateError(reservation.Status)
				return nil
			}
			if _, _, transactionErr = authorizeUsageTeam(
				ctx,
				queries,
				subject,
				organizationID,
				uuid.UUID(reservation.TeamID.Bytes),
			); transactionErr != nil {
				return transactionErr
			}
			if actualUnits > reservation.MaximumUnits {
				return serviceError(
					connect.CodeInvalidArgument,
					delibasev1.ErrorReason_ERROR_REASON_COMMIT_UNITS_EXCEED_RESERVED,
				)
			}
			totalCost, ok := multiplyNonnegativeInt64(
				actualUnits, reservation.UsdMicrosPerUnit,
			)
			if !ok {
				return serviceError(
					connect.CodeInvalidArgument,
					delibasev1.ErrorReason_ERROR_REASON_MONEY_OVERFLOW,
				)
			}
			creditApplied := minInt64(totalCost, reservation.HeldCreditMicros)
			overageApplied := totalCost - creditApplied
			capacity, transactionErr := queries.GetUsageCapacity(
				ctx,
				dbgen.GetUsageCapacityParams{
					ReservationTtlSeconds: 1,
					OrganizationID:        pgUUID(organizationID),
				},
			)
			if transactionErr != nil {
				return databaseError(transactionErr)
			}
			if capacity.SettledCreditMicros < capacity.HeldCreditMicros {
				return serviceError(
					connect.CodeFailedPrecondition,
					delibasev1.ErrorReason_ERROR_REASON_AVAILABLE_FUNDS_EXHAUSTED,
				)
			}
			if reservation.HeldOverageMicros > 0 &&
				(!capacity.BillingPeriodID.Valid ||
					capacity.BillingPeriodID != reservation.OverageBillingPeriodID ||
					capacity.SubscriptionStatus != "active" ||
					overageCapacityExhausted(capacity)) {
				return usageCapacityError(capacity)
			}
			usageRecordID, transactionErr := service.dependencies.IDs.New()
			if transactionErr != nil {
				return serviceError(connect.CodeInternal, 0)
			}
			usageRecord, transactionErr := queries.InsertUsageRecord(
				ctx,
				dbgen.InsertUsageRecordParams{
					ID:                   pgUUID(usageRecordID),
					ReservationID:        reservation.ID,
					OrganizationID:       reservation.OrganizationID,
					TeamID:               reservation.TeamID,
					TeamNameSnapshot:     reservation.TeamNameSnapshot,
					MeterID:              reservation.MeterID,
					AccountID:            reservation.AccountID,
					ServiceIdentityID:    reservation.ServiceIdentityID,
					CommittedUnits:       actualUnits,
					TotalCostMicros:      totalCost,
					CreditAppliedMicros:  creditApplied,
					OverageAppliedMicros: overageApplied,
				},
			)
			if transactionErr != nil {
				return databaseError(transactionErr)
			}
			if transactionErr = appendUsageLedger(
				ctx, service.dependencies, queries, reservation,
				"credit_commit", -creditApplied, usageRecordID,
				"usage:"+usageRecordID.String()+":credit-commit", actor,
			); transactionErr != nil {
				return transactionErr
			}
			if transactionErr = appendUsageLedger(
				ctx, service.dependencies, queries, reservation,
				"overage_commit", -overageApplied, usageRecordID,
				"usage:"+usageRecordID.String()+":overage-commit", actor,
			); transactionErr != nil {
				return transactionErr
			}
			if transactionErr = releaseUsageHolds(
				ctx, service.dependencies, queries, reservation, actor,
			); transactionErr != nil {
				return transactionErr
			}
			reservation, transactionErr = queries.FinalizeUsageReservation(
				ctx,
				dbgen.FinalizeUsageReservationParams{
					Status:         "committed",
					OrganizationID: reservation.OrganizationID,
					ReservationID:  reservation.ID,
				},
			)
			if transactionErr != nil {
				return databaseError(transactionErr)
			}
			polarPayload, reportOverage := newPolarOveragePayload(
				usageRecord.PolarEventNameSnapshot,
				organizationID,
				usageRecordID,
				overageApplied,
				usageRecord.CommittedAt.Time.UTC(),
			)
			if reportOverage {
				outboxPayload, marshalErr := json.Marshal(polarPayload)
				if marshalErr != nil {
					return serviceError(connect.CodeInternal, 0)
				}
				outboxID, idErr := service.dependencies.IDs.New()
				if idErr != nil {
					return serviceError(connect.CodeInternal, 0)
				}
				if _, transactionErr = reliability.EnqueueOutbox(
					ctx,
					queries,
					reliability.OutboxInput{
						ID:             outboxID,
						Integration:    reliability.IntegrationPolar,
						Operation:      reliability.OperationReportUsage,
						AggregateType:  reliability.AggregateUsageRecord,
						AggregateID:    usageRecordID,
						Payload:        outboxPayload,
						IdempotencyKey: "usage-record:" + usageRecordID.String(),
						Actor:          actor,
					},
				); transactionErr != nil {
					return databaseError(transactionErr)
				}
			}
			if transactionErr = appendUsageAudit(
				ctx, service.dependencies, queries,
				reliability.AuditReservationCommitted, actor, reservation,
			); transactionErr != nil {
				return transactionErr
			}
			completedAt = service.dependencies.Clock.Now().UTC()
			response.Reservation = usageReservationMessage(reservation)
			response.Commit = usageCommitMessage(
				usageRecord,
				reservation.HeldCreditMicros-creditApplied,
				reservation.HeldOverageMicros-overageApplied,
			)
			setIdempotency(
				&response.Idempotency,
				delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_COMMIT_USAGE,
				false,
				completedAt,
			)
			_, transactionErr = persistIdempotencyForCaller(
				ctx, service.dependencies, queries, "service", callerID,
				"commit_usage", key, digest, response,
			)
			return transactionErr
		},
	)
	if err != nil {
		return nil, databaseError(err)
	}
	if committedBusinessError != nil {
		return nil, committedBusinessError
	}
	return connect.NewResponse(response), nil
}

func (service *Usage) ReleaseUsage(
	ctx context.Context,
	request *connect.Request[delibasev1.ReleaseUsageRequest],
) (*connect.Response[delibasev1.ReleaseUsageResponse], error) {
	serviceClientID, subject, err := usageSubjects(ctx)
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
	reservationID, err := parseUUIDv7(request.Msg.ReservationId)
	if err != nil {
		return nil, err
	}
	key, err := validateIdempotency(request.Msg.Idempotency)
	if err != nil {
		return nil, err
	}
	digest := requestDigest(organizationID.String(), reservationID.String())
	actor, err := actorFor(service.dependencies, subject)
	if err != nil {
		return nil, err
	}

	var response *delibasev1.ReleaseUsageResponse
	var committedBusinessError error
	err = service.dependencies.Store.WithinTransaction(
		ctx,
		pgx.TxOptions{},
		func(queries *dbgen.Queries) error {
			response = &delibasev1.ReleaseUsageResponse{}
			serviceIdentity, transactionErr := usageServiceIdentity(
				ctx, queries, serviceClientID,
			)
			if transactionErr != nil {
				return transactionErr
			}
			callerID := usageCallerID(serviceIdentity.ID)
			replayed, completedAt, transactionErr := replayForCaller(
				ctx, queries, "service", callerID, "release_usage", key, digest,
				response,
			)
			if transactionErr != nil {
				return transactionErr
			}
			if replayed {
				setIdempotency(
					&response.Idempotency,
					delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_RELEASE_USAGE,
					true,
					completedAt,
				)
				return nil
			}
			if _, transactionErr = queries.LockOrganizationForBilling(
				ctx, pgUUID(organizationID),
			); transactionErr != nil {
				return usageMembershipRequired(transactionErr)
			}
			replayed, completedAt, transactionErr = replayForCaller(
				ctx, queries, "service", callerID, "release_usage", key, digest,
				response,
			)
			if transactionErr != nil {
				return transactionErr
			}
			if replayed {
				setIdempotency(
					&response.Idempotency,
					delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_RELEASE_USAGE,
					true,
					completedAt,
				)
				return nil
			}
			if _, transactionErr = expireOrganizationReservations(
				ctx, service.dependencies, queries, organizationID,
			); transactionErr != nil {
				return transactionErr
			}
			reservation, transactionErr := queries.LockUsageReservation(
				ctx,
				dbgen.LockUsageReservationParams{
					OrganizationID: pgUUID(organizationID),
					ReservationID:  pgUUID(reservationID),
				},
			)
			if errors.Is(transactionErr, pgx.ErrNoRows) {
				return reservationNotFound()
			}
			if transactionErr != nil {
				return databaseError(transactionErr)
			}
			if reservation.ServiceIdentityID != serviceIdentity.ID {
				return reservationNotFound()
			}
			account, transactionErr := usageAccount(ctx, queries, subject)
			if transactionErr != nil {
				return transactionErr
			}
			if account.ID != reservation.AccountID {
				return teamAccessDenied()
			}
			switch reservation.Status {
			case "expired":
				response.Reservation = usageReservationMessage(reservation)
				response.Release = usageReleaseMessage(reservation)
			case "held":
				if transactionErr = releaseUsageHolds(
					ctx, service.dependencies, queries, reservation, actor,
				); transactionErr != nil {
					return transactionErr
				}
				reservation, transactionErr = queries.FinalizeUsageReservation(
					ctx,
					dbgen.FinalizeUsageReservationParams{
						Status:         "released",
						OrganizationID: reservation.OrganizationID,
						ReservationID:  reservation.ID,
					},
				)
				if transactionErr != nil {
					return databaseError(transactionErr)
				}
				if transactionErr = appendUsageAudit(
					ctx, service.dependencies, queries,
					reliability.AuditReservationReleased, actor, reservation,
				); transactionErr != nil {
					return transactionErr
				}
				response.Reservation = usageReservationMessage(reservation)
				response.Release = usageReleaseMessage(reservation)
			default:
				committedBusinessError = reservationStateError(reservation.Status)
				return nil
			}
			completedAt = service.dependencies.Clock.Now().UTC()
			setIdempotency(
				&response.Idempotency,
				delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_RELEASE_USAGE,
				false,
				completedAt,
			)
			_, transactionErr = persistIdempotencyForCaller(
				ctx, service.dependencies, queries, "service", callerID,
				"release_usage", key, digest, response,
			)
			return transactionErr
		},
	)
	if err != nil {
		return nil, databaseError(err)
	}
	if committedBusinessError != nil {
		return nil, committedBusinessError
	}
	return connect.NewResponse(response), nil
}

func (service *Billing) ListUsageRecords(
	ctx context.Context,
	request *connect.Request[delibasev1.ListUsageRecordsRequest],
) (*connect.Response[delibasev1.ListUsageRecordsResponse], error) {
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
	teamID, err := parseOptionalUUIDv7(request.Msg.TeamId)
	if err != nil {
		return nil, err
	}
	meterID, err := parseOptionalUUIDv7(request.Msg.MeterId)
	if err != nil {
		return nil, err
	}
	userAccountID, err := parseOptionalUUIDv7(request.Msg.UserAccountId)
	if err != nil {
		return nil, err
	}
	pageSize, afterID, err := page(request.Msg.Page)
	if err != nil {
		return nil, err
	}
	from, to, err := billingTimeRange(request.Msg.FromTime, request.Msg.ToTime)
	if err != nil {
		return nil, err
	}

	var response *delibasev1.ListUsageRecordsResponse
	err = service.dependencies.Store.WithinTransaction(
		ctx,
		pgx.TxOptions{},
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
			rows, transactionErr := queries.ListVisibleUsageRecords(
				ctx,
				dbgen.ListVisibleUsageRecordsParams{
					OrganizationID:         pgUUID(organizationID),
					FromTime:               pgTimestamp(from),
					ToTime:                 pgTimestamp(to),
					AfterID:                afterID,
					TeamID:                 optionalPGUUID(teamID),
					MeterID:                optionalPGUUID(meterID),
					UserAccountID:          optionalPGUUID(userAccountID),
					FullOrganizationAccess: access.Role == "owner" || access.Role == "admin",
					CallerAccountID:        account.ID,
					PageLimit:              pageSize + 1,
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
			records := make([]*delibasev1.UsageRecord, 0, len(rows))
			for _, row := range rows {
				records = append(records, visibleUsageRecordMessage(row))
			}
			response = &delibasev1.ListUsageRecordsResponse{
				Records: records,
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

func usageSubjects(ctx context.Context) (serviceClientID, subject string, err error) {
	principal, ok := auth.PrincipalFromContext(ctx)
	if !ok || principal.M2M == nil || principal.User == nil {
		return "", "", serviceError(
			connect.CodeUnauthenticated,
			delibasev1.ErrorReason_ERROR_REASON_AUTHENTICATION_REQUIRED,
		)
	}
	subject, err = userSubject(ctx)
	if err != nil {
		return "", "", err
	}
	serviceClientID = strings.TrimSpace(principal.M2M.ServiceID)
	if principal.M2M.Type != auth.TokenTypeM2M || serviceClientID == "" ||
		len(serviceClientID) > 255 ||
		(principal.M2M.ClientID != "" &&
			principal.M2M.ClientID != serviceClientID) ||
		(principal.M2M.Subject != "" &&
			principal.M2M.Subject != serviceClientID) {
		return "", "", serviceError(
			connect.CodeUnauthenticated,
			delibasev1.ErrorReason_ERROR_REASON_AUTHENTICATION_INVALID,
		)
	}
	return serviceClientID, subject, nil
}

func usageServiceIdentity(
	ctx context.Context,
	queries *dbgen.Queries,
	serviceClientID string,
) (dbgen.ServiceIdentity, error) {
	identity, err := queries.GetUsageServiceIdentity(ctx, serviceClientID)
	if errors.Is(err, pgx.ErrNoRows) {
		return dbgen.ServiceIdentity{}, serviceMeterNotAllowed()
	}
	if err != nil {
		return dbgen.ServiceIdentity{}, databaseError(err)
	}
	return identity, nil
}

func usageAccount(
	ctx context.Context,
	queries *dbgen.Queries,
	subject string,
) (dbgen.Account, error) {
	account, err := queries.GetUsageAccountBySubject(ctx, subject)
	if errors.Is(err, pgx.ErrNoRows) {
		return dbgen.Account{}, serviceError(
			connect.CodePermissionDenied,
			delibasev1.ErrorReason_ERROR_REASON_ORGANIZATION_MEMBERSHIP_REQUIRED,
		)
	}
	if err != nil {
		return dbgen.Account{}, databaseError(err)
	}
	if account.Status != "active" {
		return dbgen.Account{}, serviceError(
			connect.CodePermissionDenied,
			delibasev1.ErrorReason_ERROR_REASON_RESOURCE_DELETED,
		)
	}
	return account, nil
}

func authorizeUsageTeam(
	ctx context.Context,
	queries *dbgen.Queries,
	subject string,
	organizationID uuid.UUID,
	teamID uuid.UUID,
) (dbgen.Account, dbgen.GetTeamInOrganizationRow, error) {
	account, err := usageAccount(ctx, queries, subject)
	if err != nil {
		return dbgen.Account{}, dbgen.GetTeamInOrganizationRow{}, err
	}
	if _, err = queries.GetOrganizationMembership(
		ctx,
		dbgen.GetOrganizationMembershipParams{
			OrganizationID: pgUUID(organizationID),
			AccountID:      account.ID,
		},
	); errors.Is(err, pgx.ErrNoRows) {
		return dbgen.Account{}, dbgen.GetTeamInOrganizationRow{},
			serviceError(
				connect.CodePermissionDenied,
				delibasev1.ErrorReason_ERROR_REASON_ORGANIZATION_MEMBERSHIP_REQUIRED,
			)
	} else if err != nil {
		return dbgen.Account{}, dbgen.GetTeamInOrganizationRow{},
			databaseError(err)
	}
	team, err := queries.GetTeamInOrganization(
		ctx,
		dbgen.GetTeamInOrganizationParams{
			OrganizationID: pgUUID(organizationID),
			TeamID:         pgUUID(teamID),
		},
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return dbgen.Account{}, dbgen.GetTeamInOrganizationRow{},
			teamAccessDenied()
	}
	if err != nil {
		return dbgen.Account{}, dbgen.GetTeamInOrganizationRow{},
			databaseError(err)
	}
	if _, err = queries.GetEffectiveTeamAccess(
		ctx,
		dbgen.GetEffectiveTeamAccessParams{
			TeamID:         pgUUID(teamID),
			AccountID:      account.ID,
			OrganizationID: pgUUID(organizationID),
		},
	); errors.Is(err, pgx.ErrNoRows) {
		return dbgen.Account{}, dbgen.GetTeamInOrganizationRow{},
			teamAccessDenied()
	} else if err != nil {
		return dbgen.Account{}, dbgen.GetTeamInOrganizationRow{},
			databaseError(err)
	}
	return account, team, nil
}

func expireOrganizationReservations(
	ctx context.Context,
	dependencies Dependencies,
	queries *dbgen.Queries,
	organizationID uuid.UUID,
) (int, error) {
	reservations, err := queries.ListExpiredUsageReservationsForOrganization(
		ctx, pgUUID(organizationID),
	)
	if err != nil {
		return 0, databaseError(err)
	}
	for index, reservation := range reservations {
		if err := releaseUsageHolds(
			ctx, dependencies, queries, reservation, "",
		); err != nil {
			return index, err
		}
		reservation, err = queries.FinalizeUsageReservation(
			ctx,
			dbgen.FinalizeUsageReservationParams{
				Status:         "expired",
				OrganizationID: reservation.OrganizationID,
				ReservationID:  reservation.ID,
			},
		)
		if err != nil {
			return index, databaseError(err)
		}
		if err = appendUsageAudit(
			ctx, dependencies, queries, reliability.AuditReservationReleased,
			"", reservation,
		); err != nil {
			return index, err
		}
	}
	return len(reservations), nil
}

func releaseUsageHolds(
	ctx context.Context,
	dependencies Dependencies,
	queries *dbgen.Queries,
	reservation dbgen.UsageReservation,
	actor safelog.ActorPseudonym,
) error {
	reservationID := uuid.UUID(reservation.ID.Bytes)
	if err := appendUsageLedger(
		ctx, dependencies, queries, reservation,
		"credit_release", reservation.HeldCreditMicros, uuid.Nil,
		"reservation:"+reservationID.String()+":credit-release", actor,
	); err != nil {
		return err
	}
	return appendUsageLedger(
		ctx, dependencies, queries, reservation,
		"overage_release", reservation.HeldOverageMicros, uuid.Nil,
		"reservation:"+reservationID.String()+":overage-release", actor,
	)
}

func appendUsageLedger(
	ctx context.Context,
	dependencies Dependencies,
	queries *dbgen.Queries,
	reservation dbgen.UsageReservation,
	entryType string,
	amount int64,
	usageRecordID uuid.UUID,
	sourceReference string,
	actor safelog.ActorPseudonym,
) error {
	if amount == 0 {
		return nil
	}
	balance, err := queries.CurrentOrganizationBalance(
		ctx, reservation.OrganizationID,
	)
	if err != nil {
		return databaseError(err)
	}
	nextBalance, ok := addInt64(balance, amount)
	if !ok {
		return serviceError(
			connect.CodeInvalidArgument,
			delibasev1.ErrorReason_ERROR_REASON_MONEY_OVERFLOW,
		)
	}
	id, err := dependencies.IDs.New()
	if err != nil {
		return serviceError(connect.CodeInternal, 0)
	}
	periodID := pgtype.UUID{}
	if entryType == "credit_commit" || entryType == "overage_commit" {
		record, recordErr := queries.GetUsageRecordByReservation(
			ctx,
			dbgen.GetUsageRecordByReservationParams{
				OrganizationID: reservation.OrganizationID,
				ReservationID:  reservation.ID,
			},
		)
		if recordErr != nil {
			return databaseError(recordErr)
		}
		periodID = record.BillingPeriodID
	}
	_, err = queries.InsertUsageLedgerEntry(
		ctx,
		dbgen.InsertUsageLedgerEntryParams{
			ID:                 pgUUID(id),
			OrganizationID:     reservation.OrganizationID,
			BillingPeriodID:    periodID,
			EntryType:          entryType,
			AmountMicros:       amount,
			BalanceAfterMicros: nextBalance,
			ReservationID:      reservation.ID,
			UsageRecordID:      optionalPGUUID(usageRecordID),
			TeamIDSnapshot:     reservation.TeamID,
			TeamNameSnapshot: pgtype.Text{
				String: reservation.TeamNameSnapshot,
				Valid:  true,
			},
			SourceReference: sourceReference,
			ActorReference:  string(actor),
		},
	)
	return databaseError(err)
}

func appendUsageAudit(
	ctx context.Context,
	dependencies Dependencies,
	queries *dbgen.Queries,
	event reliability.AuditEventType,
	actor safelog.ActorPseudonym,
	reservation dbgen.UsageReservation,
) error {
	id, err := dependencies.IDs.New()
	if err != nil {
		return serviceError(connect.CodeInternal, 0)
	}
	metadata, _ := requestmeta.FromContext(ctx)
	_, err = reliability.AppendAudit(
		ctx,
		queries,
		reliability.AuditInput{
			ID:                id,
			OccurredAt:        dependencies.Clock.Now().UTC(),
			EventType:         event,
			Actor:             actor,
			OrganizationID:    uuid.UUID(reservation.OrganizationID.Bytes),
			TeamID:            uuid.UUID(reservation.TeamID.Bytes),
			TeamNameSnapshot:  reservation.TeamNameSnapshot,
			ServiceIdentityID: uuid.UUID(reservation.ServiceIdentityID.Bytes),
			MeterID:           uuid.UUID(reservation.MeterID.Bytes),
			ReservationID:     uuid.UUID(reservation.ID.Bytes),
			Result:            safelog.ResultSuccess,
			Metadata: reliability.AuditMetadata{
				RequestID: metadata.RequestID,
				TraceID:   metadata.TraceID,
			},
		},
	)
	return databaseError(err)
}

func usageReservationMessage(
	row dbgen.UsageReservation,
) *delibasev1.UsageReservation {
	return &delibasev1.UsageReservation{
		ReservationId:     uuidMessage(row.ID),
		OrganizationId:    uuidMessage(row.OrganizationID),
		TeamId:            uuidMessage(row.TeamID),
		TeamNameSnapshot:  row.TeamNameSnapshot,
		MeterId:           uuidMessage(row.MeterID),
		PriceVersionId:    uuidMessage(row.PriceVersionID),
		UserAccountId:     uuidMessage(row.AccountID),
		ServiceIdentityId: uuidMessage(row.ServiceIdentityID),
		MaximumUnits:      &delibasev1.UsageUnits{Value: row.MaximumUnits},
		UsdMicrosPerUnit:  &delibasev1.UsdMicros{Value: row.UsdMicrosPerUnit},
		MaximumCost:       &delibasev1.UsdMicros{Value: row.MaximumCostMicros},
		HeldCredit:        &delibasev1.UsdMicros{Value: row.HeldCreditMicros},
		HeldOverage:       &delibasev1.UsdMicros{Value: row.HeldOverageMicros},
		ClientReference:   row.ClientReference,
		Status:            reservationStatus(row.Status),
		CreatedAt:         timestamp(row.CreatedAt),
		ExpiresAt:         timestamp(row.ExpiresAt),
		FinalizedAt:       timestamp(row.FinalizedAt),
	}
}

func usageCommitMessage(
	row dbgen.UsageRecord,
	creditReleased int64,
	overageReleased int64,
) *delibasev1.UsageCommit {
	return &delibasev1.UsageCommit{
		UsageRecordId:       uuidMessage(row.ID),
		ReservationId:       uuidMessage(row.ReservationID),
		CommittedUnits:      &delibasev1.UsageUnits{Value: row.CommittedUnits},
		TotalCost:           &delibasev1.UsdMicros{Value: row.TotalCostMicros},
		CreditApplied:       &delibasev1.UsdMicros{Value: row.CreditAppliedMicros},
		OverageApplied:      &delibasev1.UsdMicros{Value: row.OverageAppliedMicros},
		CreditHoldReleased:  &delibasev1.UsdMicros{Value: creditReleased},
		OverageHoldReleased: &delibasev1.UsdMicros{Value: overageReleased},
		CommittedAt:         timestamp(row.CommittedAt),
	}
}

func usageReleaseMessage(
	row dbgen.UsageReservation,
) *delibasev1.UsageRelease {
	return &delibasev1.UsageRelease{
		ReservationId:       uuidMessage(row.ID),
		CreditHoldReleased:  &delibasev1.UsdMicros{Value: row.HeldCreditMicros},
		OverageHoldReleased: &delibasev1.UsdMicros{Value: row.HeldOverageMicros},
		ReservationStatus:   reservationStatus(row.Status),
		FinalizedAt:         timestamp(row.FinalizedAt),
	}
}

func visibleUsageRecordMessage(
	row dbgen.ListVisibleUsageRecordsRow,
) *delibasev1.UsageRecord {
	return &delibasev1.UsageRecord{
		UsageRecordId:     uuidMessage(row.ID),
		OrganizationId:    uuidMessage(row.OrganizationID),
		ReservationId:     uuidMessage(row.ReservationID),
		MeterId:           uuidMessage(row.MeterID),
		PriceVersionId:    uuidMessage(row.PriceVersionID),
		TeamIdSnapshot:    uuidMessage(row.TeamID),
		TeamNameSnapshot:  row.TeamNameSnapshot,
		UserAccountId:     uuidMessage(row.AccountID),
		ServiceIdentityId: uuidMessage(row.ServiceIdentityID),
		Units:             &delibasev1.UsageUnits{Value: row.CommittedUnits},
		UsdMicrosPerUnit:  &delibasev1.UsdMicros{Value: row.UsdMicrosPerUnit},
		TotalCost:         &delibasev1.UsdMicros{Value: row.TotalCostMicros},
		CreditApplied:     &delibasev1.UsdMicros{Value: row.CreditAppliedMicros},
		OverageApplied:    &delibasev1.UsdMicros{Value: row.OverageAppliedMicros},
		ClientReference:   row.ClientReference,
		Status:            usageRecordStatus(row.DeliveryStatus),
		CommittedAt:       timestamp(row.CommittedAt),
	}
}

func reservationStatus(value string) delibasev1.ReservationStatus {
	switch value {
	case "held":
		return delibasev1.ReservationStatus_RESERVATION_STATUS_ACTIVE
	case "committed":
		return delibasev1.ReservationStatus_RESERVATION_STATUS_COMMITTED
	case "released":
		return delibasev1.ReservationStatus_RESERVATION_STATUS_RELEASED
	case "expired":
		return delibasev1.ReservationStatus_RESERVATION_STATUS_EXPIRED
	default:
		return delibasev1.ReservationStatus_RESERVATION_STATUS_UNSPECIFIED
	}
}

func usageRecordStatus(value string) delibasev1.UsageRecordStatus {
	switch value {
	case "committed":
		return delibasev1.UsageRecordStatus_USAGE_RECORD_STATUS_COMMITTED
	case "polar_pending":
		return delibasev1.UsageRecordStatus_USAGE_RECORD_STATUS_POLAR_PENDING
	case "polar_reported":
		return delibasev1.UsageRecordStatus_USAGE_RECORD_STATUS_POLAR_REPORTED
	default:
		return delibasev1.UsageRecordStatus_USAGE_RECORD_STATUS_UNSPECIFIED
	}
}

func validateClientReference(value string) (string, error) {
	if !idempotencyKeyPattern.MatchString(value) || redact.Text(value) != value {
		return "", serviceError(
			connect.CodeInvalidArgument,
			delibasev1.ErrorReason_ERROR_REASON_CLIENT_REFERENCE_CONFLICT,
		)
	}
	return value, nil
}

func multiplyNonnegativeInt64(left, right int64) (int64, bool) {
	if left < 0 || right < 0 {
		return 0, false
	}
	if left != 0 && right > math.MaxInt64/left {
		return 0, false
	}
	return left * right, true
}

func minInt64(left, right int64) int64 {
	if left < right {
		return left
	}
	return right
}

func usageCallerID(value pgtype.UUID) string {
	if !value.Valid {
		return ""
	}
	return uuid.UUID(value.Bytes).String()
}

func overageCapacityExhausted(capacity dbgen.GetUsageCapacityRow) bool {
	committedAndHeld, ok := addInt64(
		capacity.CommittedOverageMicros,
		capacity.HeldOverageMicros,
	)
	if !ok {
		return true
	}
	total, ok := addInt64(committedAndHeld, capacity.RefundShortfallMicros)
	return !ok || total > capacity.EffectiveOverageLimitMicros
}

func usageCapacityError(capacity dbgen.GetUsageCapacityRow) error {
	switch capacity.SubscriptionStatus {
	case "past_due":
		return serviceError(
			connect.CodeFailedPrecondition,
			delibasev1.ErrorReason_ERROR_REASON_SUBSCRIPTION_PAST_DUE,
		)
	case "canceled":
		return serviceError(
			connect.CodeFailedPrecondition,
			delibasev1.ErrorReason_ERROR_REASON_SUBSCRIPTION_CANCELED,
		)
	case "revoked":
		return serviceError(
			connect.CodeFailedPrecondition,
			delibasev1.ErrorReason_ERROR_REASON_SUBSCRIPTION_REVOKED,
		)
	case "active":
		if !capacity.BillingPeriodID.Valid {
			return serviceError(
				connect.CodeFailedPrecondition,
				delibasev1.ErrorReason_ERROR_REASON_SUBSCRIPTION_INACTIVE,
			)
		}
	default:
		return serviceError(
			connect.CodeFailedPrecondition,
			delibasev1.ErrorReason_ERROR_REASON_SUBSCRIPTION_INACTIVE,
		)
	}
	if !capacity.OverageLimitConfigured {
		return serviceError(
			connect.CodeFailedPrecondition,
			delibasev1.ErrorReason_ERROR_REASON_OVERAGE_NOT_CONFIGURED,
		)
	}
	if capacity.RequestedOverageLimitMicros == 0 {
		return serviceError(
			connect.CodeFailedPrecondition,
			delibasev1.ErrorReason_ERROR_REASON_OVERAGE_DISABLED,
		)
	}
	return serviceError(
		connect.CodeResourceExhausted,
		delibasev1.ErrorReason_ERROR_REASON_OVERAGE_LIMIT_EXHAUSTED,
	)
}

func serviceMeterNotAllowed() error {
	return serviceError(
		connect.CodePermissionDenied,
		delibasev1.ErrorReason_ERROR_REASON_SERVICE_METER_NOT_ALLOWED,
	)
}

func usageMembershipRequired(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return serviceError(
			connect.CodePermissionDenied,
			delibasev1.ErrorReason_ERROR_REASON_ORGANIZATION_MEMBERSHIP_REQUIRED,
		)
	}
	return databaseError(err)
}

func reservationNotFound() error {
	return serviceError(
		connect.CodeNotFound,
		delibasev1.ErrorReason_ERROR_REASON_RESERVATION_NOT_FOUND,
	)
}

func reservationStateError(status string) error {
	switch status {
	case "expired":
		return serviceError(
			connect.CodeFailedPrecondition,
			delibasev1.ErrorReason_ERROR_REASON_RESERVATION_EXPIRED,
		)
	case "committed":
		return serviceError(
			connect.CodeAlreadyExists,
			delibasev1.ErrorReason_ERROR_REASON_RESERVATION_ALREADY_COMMITTED,
		)
	case "released":
		return serviceError(
			connect.CodeFailedPrecondition,
			delibasev1.ErrorReason_ERROR_REASON_RESERVATION_ALREADY_RELEASED,
		)
	default:
		return serviceError(
			connect.CodeFailedPrecondition,
			delibasev1.ErrorReason_ERROR_REASON_RESERVATION_FINALIZED,
		)
	}
}
