package service

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	delibasev1 "github.com/delinoio/oss/protos/delibase/gen/go/delibase/v1"
	"github.com/delinoio/oss/servers/delibase/internal/database/dbgen"
	"github.com/delinoio/oss/servers/delibase/internal/reliability"
	"github.com/delinoio/oss/servers/internal/auth"
	"github.com/delinoio/oss/servers/internal/safelog"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	reserveAuthorizedUsageOperation = "reserve_authorized_usage"
	commitAuthorizedUsageOperation  = "commit_authorized_usage"
	releaseAuthorizedUsageOperation = "release_authorized_usage"
	markBackgroundDeletedOperation  = "mark_background_usage_resource_deleted"
)

type authorizedUsageBinding struct {
	authorizationID   uuid.UUID
	purpose           string
	featureResourceID uuid.UUID
	period            string
	periodStart       time.Time
}

func (service *Usage) ReserveAuthorizedUsage(
	ctx context.Context,
	request *connect.Request[delibasev1.ReserveAuthorizedUsageRequest],
) (*connect.Response[delibasev1.ReserveAuthorizedUsageResponse], error) {
	serviceClientID, err := authorizedUsageServiceClientID(ctx)
	if err != nil {
		return nil, err
	}
	if request == nil || request.Msg == nil || request.Msg.MaximumUnits == nil {
		return nil, invalidArgument()
	}
	binding, err := parseAuthorizedUsageBinding(
		request.Msg.Context,
		service.dependencies.Clock.Now(),
		true,
	)
	if err != nil {
		return nil, err
	}
	maximumUnits := request.Msg.MaximumUnits.Value
	if maximumUnits <= 0 {
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

	var response *delibasev1.ReserveAuthorizedUsageResponse
	var loggedAuthorization dbgen.BackgroundUsageAuthorization
	err = service.dependencies.Store.WithinTransaction(
		ctx,
		pgx.TxOptions{},
		func(queries *dbgen.Queries) error {
			response = &delibasev1.ReserveAuthorizedUsageResponse{}
			serviceIdentity, transactionErr := usageServiceIdentity(
				ctx,
				queries,
				serviceClientID,
			)
			if transactionErr != nil {
				return transactionErr
			}
			callerID := usageCallerID(serviceIdentity.ID)
			digest := authorizedUsageDigest(
				serviceIdentity.ID,
				binding,
				strconv.FormatInt(maximumUnits, 10),
				clientReference,
			)
			replayed, completedAt, transactionErr := backgroundReplayForCaller(
				ctx,
				queries,
				"service",
				callerID,
				reserveAuthorizedUsageOperation,
				key,
				digest,
				response,
			)
			if transactionErr != nil {
				return transactionErr
			}
			if replayed {
				setIdempotency(
					&response.Idempotency,
					delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_RESERVE_AUTHORIZED_USAGE,
					true,
					completedAt,
				)
				return nil
			}
			authorization, transactionErr := lockCurrentBackgroundAuthorization(
				ctx,
				queries,
				binding,
				serviceIdentity.ID,
			)
			if transactionErr != nil {
				return transactionErr
			}
			replayed, completedAt, transactionErr = backgroundReplayForCaller(
				ctx,
				queries,
				"service",
				callerID,
				reserveAuthorizedUsageOperation,
				key,
				digest,
				response,
			)
			if transactionErr != nil {
				return transactionErr
			}
			if replayed {
				setIdempotency(
					&response.Idempotency,
					delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_RESERVE_AUTHORIZED_USAGE,
					true,
					completedAt,
				)
				return nil
			}
			if !serviceIdentity.Enabled {
				return backgroundAuthorizationAccessLost()
			}
			organizationID := uuid.UUID(authorization.OrganizationID.Bytes)
			if _, transactionErr = drainExpiredOrganizationReservations(
				ctx,
				service.dependencies,
				queries,
				organizationID,
			); transactionErr != nil {
				return transactionErr
			}
			periodUsage, transactionErr := queries.GetBackgroundUsagePeriodUsage(
				ctx,
				dbgen.GetBackgroundUsagePeriodUsageParams{
					PeriodStart:     pgTimestamp(binding.periodStart),
					AuthorizationID: authorization.ID,
				},
			)
			if transactionErr != nil {
				return databaseError(transactionErr)
			}
			if maximumUnits > periodUsage.RemainingUnits {
				return backgroundPeriodLimitExceeded()
			}
			team, transactionErr := queries.GetTeamInOrganization(
				ctx,
				dbgen.GetTeamInOrganizationParams{
					OrganizationID: authorization.OrganizationID,
					TeamID:         authorization.TeamID,
				},
			)
			if errors.Is(transactionErr, pgx.ErrNoRows) {
				return backgroundAuthorizationAccessLost()
			}
			if transactionErr != nil {
				return databaseError(transactionErr)
			}
			meter, transactionErr := queries.GetUsageMeterAuthorization(
				ctx,
				dbgen.GetUsageMeterAuthorizationParams{
					ServiceIdentityID: serviceIdentity.ID,
					MeterID:           authorization.MeterID,
				},
			)
			if errors.Is(transactionErr, pgx.ErrNoRows) {
				return backgroundAuthorizationAccessLost()
			}
			if transactionErr != nil {
				return databaseError(transactionErr)
			}
			maximumCost, ok := multiplyNonnegativeInt64(
				maximumUnits,
				meter.UsdMicrosPerUnit,
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
					OrganizationID:        authorization.OrganizationID,
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
			reservation, transactionErr := queries.InsertAuthorizedUsageReservation(
				ctx,
				dbgen.InsertAuthorizedUsageReservationParams{
					ID:                             pgUUID(reservationID),
					OrganizationID:                 authorization.OrganizationID,
					TeamID:                         authorization.TeamID,
					TeamNameSnapshot:               team.Name,
					MeterID:                        authorization.MeterID,
					PriceVersionID:                 meter.PriceVersionID,
					AccountID:                      authorization.AuthorizerAccountID,
					ServiceIdentityID:              serviceIdentity.ID,
					MaximumUnits:                   maximumUnits,
					UsdMicrosPerUnit:               meter.UsdMicrosPerUnit,
					MaximumCostMicros:              maximumCost,
					HeldCreditMicros:               heldCredit,
					HeldOverageMicros:              heldOverage,
					ClientReference:                clientReference,
					ReservationTtlSeconds:          meter.ReservationTtlSeconds,
					UserActorReferenceSnapshot:     authorization.ActorReference,
					BackgroundUsageAuthorizationID: authorization.ID,
					BackgroundUsagePurpose: pgtype.Text{
						String: binding.purpose,
						Valid:  true,
					},
					BackgroundFeatureResourceID: pgUUID(binding.featureResourceID),
					BackgroundUsagePeriod: pgtype.Text{
						String: binding.period,
						Valid:  true,
					},
					BackgroundPeriodStart: pgTimestamp(binding.periodStart),
				},
			)
			if transactionErr != nil {
				return databaseError(transactionErr)
			}
			actor, transactionErr := backgroundAuthorizationActor(authorization)
			if transactionErr != nil {
				return transactionErr
			}
			if transactionErr = appendUsageLedger(
				ctx,
				service.dependencies,
				queries,
				reservation,
				"credit_hold",
				-heldCredit,
				uuid.Nil,
				"reservation:"+reservationID.String()+":credit-hold",
				actor,
			); transactionErr != nil {
				return transactionErr
			}
			if transactionErr = appendUsageLedger(
				ctx,
				service.dependencies,
				queries,
				reservation,
				"overage_hold",
				-heldOverage,
				uuid.Nil,
				"reservation:"+reservationID.String()+":overage-hold",
				actor,
			); transactionErr != nil {
				return transactionErr
			}
			if transactionErr = appendUsageAudit(
				ctx,
				service.dependencies,
				queries,
				reliability.AuditReservationCreated,
				actor,
				reservation,
			); transactionErr != nil {
				return transactionErr
			}
			periodUsage, transactionErr = queries.GetBackgroundUsagePeriodUsage(
				ctx,
				dbgen.GetBackgroundUsagePeriodUsageParams{
					PeriodStart:     pgTimestamp(binding.periodStart),
					AuthorizationID: authorization.ID,
				},
			)
			if transactionErr != nil {
				return databaseError(transactionErr)
			}
			completedAt = service.dependencies.Clock.Now().UTC()
			response.Reservation = usageReservationMessage(reservation)
			response.PeriodUsage = backgroundPeriodUsageMessage(periodUsage)
			setIdempotency(
				&response.Idempotency,
				delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_RESERVE_AUTHORIZED_USAGE,
				false,
				completedAt,
			)
			_, transactionErr = persistIdempotencyForCaller(
				ctx,
				service.dependencies,
				queries,
				"service",
				callerID,
				reserveAuthorizedUsageOperation,
				key,
				digest,
				response,
			)
			loggedAuthorization = authorization
			return transactionErr
		},
	)
	if err != nil {
		return nil, databaseError(err)
	}
	logAuthorizedUsageSuccess(ctx, service.dependencies, loggedAuthorization)
	return connect.NewResponse(response), nil
}

func (service *Usage) CommitAuthorizedUsage(
	ctx context.Context,
	request *connect.Request[delibasev1.CommitAuthorizedUsageRequest],
) (*connect.Response[delibasev1.CommitAuthorizedUsageResponse], error) {
	serviceClientID, err := authorizedUsageServiceClientID(ctx)
	if err != nil {
		return nil, err
	}
	if request == nil || request.Msg == nil || request.Msg.ActualUnits == nil {
		return nil, invalidArgument()
	}
	binding, err := parseAuthorizedUsageBinding(
		request.Msg.Context,
		service.dependencies.Clock.Now(),
		false,
	)
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

	var response *delibasev1.CommitAuthorizedUsageResponse
	var committedBusinessError error
	var loggedAuthorization dbgen.BackgroundUsageAuthorization
	err = service.dependencies.Store.WithinTransaction(
		ctx,
		pgx.TxOptions{},
		func(queries *dbgen.Queries) error {
			response = &delibasev1.CommitAuthorizedUsageResponse{}
			serviceIdentity, transactionErr := usageServiceIdentity(
				ctx,
				queries,
				serviceClientID,
			)
			if transactionErr != nil {
				return transactionErr
			}
			callerID := usageCallerID(serviceIdentity.ID)
			digest := authorizedUsageDigest(
				serviceIdentity.ID,
				binding,
				reservationID.String(),
				strconv.FormatInt(actualUnits, 10),
			)
			replayed, completedAt, transactionErr := backgroundReplayForCaller(
				ctx,
				queries,
				"service",
				callerID,
				commitAuthorizedUsageOperation,
				key,
				digest,
				response,
			)
			if transactionErr != nil {
				return transactionErr
			}
			if replayed {
				setIdempotency(
					&response.Idempotency,
					delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_COMMIT_AUTHORIZED_USAGE,
					true,
					completedAt,
				)
				return nil
			}
			authorization, transactionErr := lockCurrentBackgroundAuthorization(
				ctx,
				queries,
				binding,
				serviceIdentity.ID,
			)
			if transactionErr != nil {
				return transactionErr
			}
			replayed, completedAt, transactionErr = backgroundReplayForCaller(
				ctx,
				queries,
				"service",
				callerID,
				commitAuthorizedUsageOperation,
				key,
				digest,
				response,
			)
			if transactionErr != nil {
				return transactionErr
			}
			if replayed {
				setIdempotency(
					&response.Idempotency,
					delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_COMMIT_AUTHORIZED_USAGE,
					true,
					completedAt,
				)
				return nil
			}
			organizationID := uuid.UUID(authorization.OrganizationID.Bytes)
			if _, transactionErr = drainExpiredOrganizationReservations(
				ctx,
				service.dependencies,
				queries,
				organizationID,
			); transactionErr != nil {
				return transactionErr
			}
			reservation, transactionErr := lockAuthorizedReservation(
				ctx,
				queries,
				binding,
				serviceIdentity.ID,
				reservationID,
			)
			if transactionErr != nil {
				return transactionErr
			}
			if reservation.Status != "held" {
				committedBusinessError = reservationStateError(reservation.Status)
				return nil
			}
			if actualUnits > reservation.MaximumUnits {
				return serviceError(
					connect.CodeInvalidArgument,
					delibasev1.ErrorReason_ERROR_REASON_COMMIT_UNITS_EXCEED_RESERVED,
				)
			}
			totalCost, ok := multiplyNonnegativeInt64(
				actualUnits,
				reservation.UsdMicrosPerUnit,
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
					OrganizationID:        reservation.OrganizationID,
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
			actor, transactionErr := backgroundAuthorizationActor(authorization)
			if transactionErr != nil {
				return transactionErr
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
				ctx,
				service.dependencies,
				queries,
				reservation,
				"credit_commit",
				-creditApplied,
				usageRecordID,
				"usage:"+usageRecordID.String()+":credit-commit",
				actor,
			); transactionErr != nil {
				return transactionErr
			}
			if transactionErr = appendUsageLedger(
				ctx,
				service.dependencies,
				queries,
				reservation,
				"overage_commit",
				-overageApplied,
				usageRecordID,
				"usage:"+usageRecordID.String()+":overage-commit",
				actor,
			); transactionErr != nil {
				return transactionErr
			}
			if transactionErr = releaseUsageHolds(
				ctx,
				service.dependencies,
				queries,
				reservation,
				actor,
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
				ctx,
				service.dependencies,
				queries,
				reliability.AuditReservationCommitted,
				actor,
				reservation,
			); transactionErr != nil {
				return transactionErr
			}
			periodUsage, transactionErr := queries.GetBackgroundUsagePeriodUsage(
				ctx,
				dbgen.GetBackgroundUsagePeriodUsageParams{
					PeriodStart:     pgTimestamp(binding.periodStart),
					AuthorizationID: authorization.ID,
				},
			)
			if transactionErr != nil {
				return databaseError(transactionErr)
			}
			completedAt = service.dependencies.Clock.Now().UTC()
			response.Reservation = usageReservationMessage(reservation)
			response.Commit = usageCommitMessage(
				usageRecord,
				reservation.HeldCreditMicros-creditApplied,
				reservation.HeldOverageMicros-overageApplied,
			)
			response.PeriodUsage = backgroundPeriodUsageMessage(periodUsage)
			setIdempotency(
				&response.Idempotency,
				delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_COMMIT_AUTHORIZED_USAGE,
				false,
				completedAt,
			)
			_, transactionErr = persistIdempotencyForCaller(
				ctx,
				service.dependencies,
				queries,
				"service",
				callerID,
				commitAuthorizedUsageOperation,
				key,
				digest,
				response,
			)
			loggedAuthorization = authorization
			return transactionErr
		},
	)
	if err != nil {
		return nil, databaseError(err)
	}
	if committedBusinessError != nil {
		return nil, committedBusinessError
	}
	logAuthorizedUsageSuccess(ctx, service.dependencies, loggedAuthorization)
	return connect.NewResponse(response), nil
}

func (service *Usage) ReleaseAuthorizedUsage(
	ctx context.Context,
	request *connect.Request[delibasev1.ReleaseAuthorizedUsageRequest],
) (*connect.Response[delibasev1.ReleaseAuthorizedUsageResponse], error) {
	serviceClientID, err := authorizedUsageServiceClientID(ctx)
	if err != nil {
		return nil, err
	}
	if request == nil || request.Msg == nil || request.Msg.ReservedUnits == nil ||
		request.Msg.ReservedUnits.Value <= 0 {
		return nil, invalidArgument()
	}
	binding, err := parseAuthorizedUsageBinding(
		request.Msg.Context,
		service.dependencies.Clock.Now(),
		false,
	)
	if err != nil {
		return nil, err
	}
	reservationID, err := parseUUIDv7(request.Msg.ReservationId)
	if err != nil {
		return nil, err
	}
	reservedUnits := request.Msg.ReservedUnits.Value
	key, err := validateIdempotency(request.Msg.Idempotency)
	if err != nil {
		return nil, err
	}

	var response *delibasev1.ReleaseAuthorizedUsageResponse
	var committedBusinessError error
	var loggedAuthorization dbgen.BackgroundUsageAuthorization
	err = service.dependencies.Store.WithinTransaction(
		ctx,
		pgx.TxOptions{},
		func(queries *dbgen.Queries) error {
			response = &delibasev1.ReleaseAuthorizedUsageResponse{}
			serviceIdentity, transactionErr := usageServiceIdentity(
				ctx,
				queries,
				serviceClientID,
			)
			if transactionErr != nil {
				return transactionErr
			}
			callerID := usageCallerID(serviceIdentity.ID)
			digest := authorizedUsageDigest(
				serviceIdentity.ID,
				binding,
				reservationID.String(),
				strconv.FormatInt(reservedUnits, 10),
			)
			replayed, completedAt, transactionErr := backgroundReplayForCaller(
				ctx,
				queries,
				"service",
				callerID,
				releaseAuthorizedUsageOperation,
				key,
				digest,
				response,
			)
			if transactionErr != nil {
				return transactionErr
			}
			if replayed {
				setIdempotency(
					&response.Idempotency,
					delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_RELEASE_AUTHORIZED_USAGE,
					true,
					completedAt,
				)
				return nil
			}
			authorization, transactionErr := lockCurrentBackgroundAuthorization(
				ctx,
				queries,
				binding,
				serviceIdentity.ID,
			)
			if transactionErr != nil {
				return transactionErr
			}
			replayed, completedAt, transactionErr = backgroundReplayForCaller(
				ctx,
				queries,
				"service",
				callerID,
				releaseAuthorizedUsageOperation,
				key,
				digest,
				response,
			)
			if transactionErr != nil {
				return transactionErr
			}
			if replayed {
				setIdempotency(
					&response.Idempotency,
					delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_RELEASE_AUTHORIZED_USAGE,
					true,
					completedAt,
				)
				return nil
			}
			organizationID := uuid.UUID(authorization.OrganizationID.Bytes)
			if _, transactionErr = drainExpiredOrganizationReservations(
				ctx,
				service.dependencies,
				queries,
				organizationID,
			); transactionErr != nil {
				return transactionErr
			}
			reservation, transactionErr := lockAuthorizedReservation(
				ctx,
				queries,
				binding,
				serviceIdentity.ID,
				reservationID,
			)
			if transactionErr != nil {
				return transactionErr
			}
			if reservedUnits != reservation.MaximumUnits {
				return backgroundAuthorizationSubstitution()
			}
			actor, transactionErr := backgroundAuthorizationActor(authorization)
			if transactionErr != nil {
				return transactionErr
			}
			switch reservation.Status {
			case "expired":
				response.Reservation = usageReservationMessage(reservation)
				response.Release = usageReleaseMessage(reservation)
			case "held":
				if transactionErr = releaseUsageHolds(
					ctx,
					service.dependencies,
					queries,
					reservation,
					actor,
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
					ctx,
					service.dependencies,
					queries,
					reliability.AuditReservationReleased,
					actor,
					reservation,
				); transactionErr != nil {
					return transactionErr
				}
				response.Reservation = usageReservationMessage(reservation)
				response.Release = usageReleaseMessage(reservation)
			default:
				committedBusinessError = reservationStateError(reservation.Status)
				return nil
			}
			periodUsage, transactionErr := queries.GetBackgroundUsagePeriodUsage(
				ctx,
				dbgen.GetBackgroundUsagePeriodUsageParams{
					PeriodStart:     pgTimestamp(binding.periodStart),
					AuthorizationID: authorization.ID,
				},
			)
			if transactionErr != nil {
				return databaseError(transactionErr)
			}
			completedAt = service.dependencies.Clock.Now().UTC()
			response.PeriodUsage = backgroundPeriodUsageMessage(periodUsage)
			setIdempotency(
				&response.Idempotency,
				delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_RELEASE_AUTHORIZED_USAGE,
				false,
				completedAt,
			)
			_, transactionErr = persistIdempotencyForCaller(
				ctx,
				service.dependencies,
				queries,
				"service",
				callerID,
				releaseAuthorizedUsageOperation,
				key,
				digest,
				response,
			)
			loggedAuthorization = authorization
			return transactionErr
		},
	)
	if err != nil {
		return nil, databaseError(err)
	}
	if committedBusinessError != nil {
		return nil, committedBusinessError
	}
	logAuthorizedUsageSuccess(ctx, service.dependencies, loggedAuthorization)
	return connect.NewResponse(response), nil
}

func (service *Usage) MarkBackgroundUsageResourceDeleted(
	ctx context.Context,
	request *connect.Request[delibasev1.MarkBackgroundUsageResourceDeletedRequest],
) (*connect.Response[delibasev1.MarkBackgroundUsageResourceDeletedResponse], error) {
	serviceClientID, err := authorizedUsageServiceClientID(ctx)
	if err != nil {
		return nil, err
	}
	if request == nil || request.Msg == nil || request.Msg.ExpectedRevision <= 0 {
		return nil, invalidArgument()
	}
	authorizationID, err := parseUUIDv7(request.Msg.AuthorizationId)
	if err != nil {
		return nil, err
	}
	featureResourceID, err := parseUUIDv7(request.Msg.FeatureResourceId)
	if err != nil {
		return nil, err
	}
	if request.Msg.Purpose !=
		delibasev1.BackgroundUsagePurpose_BACKGROUND_USAGE_PURPOSE_REALQA_STORAGE {
		return nil, backgroundAuthorizationSubstitution()
	}
	key, err := validateIdempotency(request.Msg.Idempotency)
	if err != nil {
		return nil, err
	}

	var response *delibasev1.MarkBackgroundUsageResourceDeletedResponse
	var loggedAuthorization dbgen.BackgroundUsageAuthorization
	var serviceActor safelog.ActorPseudonym
	err = service.dependencies.Store.WithinTransaction(
		ctx,
		pgx.TxOptions{},
		func(queries *dbgen.Queries) error {
			response = &delibasev1.MarkBackgroundUsageResourceDeletedResponse{}
			serviceIdentity, transactionErr := usageServiceIdentity(
				ctx,
				queries,
				serviceClientID,
			)
			if transactionErr != nil {
				return transactionErr
			}
			callerID := usageCallerID(serviceIdentity.ID)
			digest := requestDigest(
				authorizationID.String(),
				callerID,
				backgroundPurposeRealQAStorage,
				featureResourceID.String(),
				strconv.FormatInt(request.Msg.ExpectedRevision, 10),
			)
			replayed, completedAt, transactionErr := backgroundReplayForCaller(
				ctx,
				queries,
				"service",
				callerID,
				markBackgroundDeletedOperation,
				key,
				digest,
				response,
			)
			if transactionErr != nil {
				return transactionErr
			}
			if replayed {
				setIdempotency(
					&response.Idempotency,
					delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_MARK_BACKGROUND_USAGE_RESOURCE_DELETED,
					true,
					completedAt,
				)
				return nil
			}
			current, transactionErr := queries.GetBackgroundUsageAuthorization(
				ctx,
				pgUUID(authorizationID),
			)
			if transactionErr != nil {
				return backgroundAuthorizationNotFound(transactionErr)
			}
			if transactionErr = validateBackgroundResourceDeletionBinding(
				current,
				serviceIdentity.ID,
				featureResourceID,
			); transactionErr != nil {
				return transactionErr
			}
			if _, transactionErr = queries.LockOrganizationForBilling(
				ctx,
				current.OrganizationID,
			); transactionErr != nil {
				return backgroundAuthorizationAccessLost()
			}
			replayed, completedAt, transactionErr = backgroundReplayForCaller(
				ctx,
				queries,
				"service",
				callerID,
				markBackgroundDeletedOperation,
				key,
				digest,
				response,
			)
			if transactionErr != nil {
				return transactionErr
			}
			if replayed {
				setIdempotency(
					&response.Idempotency,
					delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_MARK_BACKGROUND_USAGE_RESOURCE_DELETED,
					true,
					completedAt,
				)
				return nil
			}
			current, transactionErr = queries.LockBackgroundUsageAuthorizationForMutation(
				ctx,
				pgUUID(authorizationID),
			)
			if transactionErr != nil {
				return backgroundAuthorizationNotFound(transactionErr)
			}
			if transactionErr = validateBackgroundResourceDeletionBinding(
				current,
				serviceIdentity.ID,
				featureResourceID,
			); transactionErr != nil {
				return transactionErr
			}
			if current.Status != "active" {
				return backgroundAuthorizationStatusInvalid()
			}
			if current.Revision != request.Msg.ExpectedRevision {
				return serviceError(
					connect.CodeAborted,
					delibasev1.ErrorReason_ERROR_REASON_RESOURCE_CONFLICT,
				)
			}
			authorization, transactionErr :=
				queries.MarkBackgroundUsageAuthorizationResourceDeleted(
					ctx,
					dbgen.MarkBackgroundUsageAuthorizationResourceDeletedParams{
						AuthorizationID:   pgUUID(authorizationID),
						ServiceIdentityID: serviceIdentity.ID,
						Purpose:           backgroundPurposeRealQAStorage,
						FeatureResourceID: pgUUID(featureResourceID),
						ExpectedRevision:  request.Msg.ExpectedRevision,
					},
				)
			if transactionErr != nil {
				return databaseError(transactionErr)
			}
			team, transactionErr := queries.GetTeamByID(ctx, authorization.TeamID)
			if transactionErr != nil {
				return databaseError(transactionErr)
			}
			serviceActor, transactionErr = actorFor(
				service.dependencies,
				serviceClientID,
			)
			if transactionErr != nil {
				return transactionErr
			}
			response.Authorization, transactionErr = backgroundAuthorizationView(
				ctx,
				queries,
				authorization,
				currentUTCPeriodStart(service.dependencies.Clock.Now()),
			)
			if transactionErr != nil {
				return transactionErr
			}
			if transactionErr = appendBackgroundAuthorizationAudit(
				ctx,
				service.dependencies,
				queries,
				reliability.AuditBackgroundAuthorizationResourceDeleted,
				serviceActor,
				authorization,
				team.Name,
				uuid.Nil,
			); transactionErr != nil {
				return transactionErr
			}
			completedAt = service.dependencies.Clock.Now().UTC()
			setIdempotency(
				&response.Idempotency,
				delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_MARK_BACKGROUND_USAGE_RESOURCE_DELETED,
				false,
				completedAt,
			)
			_, transactionErr = persistIdempotencyForCaller(
				ctx,
				service.dependencies,
				queries,
				"service",
				callerID,
				markBackgroundDeletedOperation,
				key,
				digest,
				response,
			)
			loggedAuthorization = authorization
			return transactionErr
		},
	)
	if err != nil {
		return nil, databaseError(err)
	}
	if loggedAuthorization.ID.Valid {
		logBackgroundAuthorizationOutcome(
			ctx,
			service.dependencies,
			serviceActor,
			uuid.UUID(loggedAuthorization.OrganizationID.Bytes),
			uuid.UUID(loggedAuthorization.TeamID.Bytes),
			uuid.UUID(loggedAuthorization.ServiceIdentityID.Bytes),
			uuid.UUID(loggedAuthorization.MeterID.Bytes),
			safelog.ResultSuccess,
		)
	}
	return connect.NewResponse(response), nil
}

func authorizedUsageServiceClientID(ctx context.Context) (string, error) {
	principal, ok := auth.PrincipalFromContext(ctx)
	if !ok || principal.M2M == nil || principal.User != nil {
		return "", serviceError(
			connect.CodeUnauthenticated,
			delibasev1.ErrorReason_ERROR_REASON_AUTHENTICATION_REQUIRED,
		)
	}
	serviceClientID := strings.TrimSpace(principal.M2M.ServiceID)
	if principal.M2M.Type != auth.TokenTypeM2M || serviceClientID == "" ||
		len(serviceClientID) > 255 ||
		(principal.M2M.ClientID != "" &&
			principal.M2M.ClientID != serviceClientID) ||
		(principal.M2M.Subject != "" &&
			principal.M2M.Subject != serviceClientID) {
		return "", serviceError(
			connect.CodeUnauthenticated,
			delibasev1.ErrorReason_ERROR_REASON_AUTHENTICATION_INVALID,
		)
	}
	return serviceClientID, nil
}

func parseAuthorizedUsageBinding(
	value *delibasev1.AuthorizedUsageContext,
	now time.Time,
	reserve bool,
) (authorizedUsageBinding, error) {
	if value == nil {
		return authorizedUsageBinding{}, invalidArgument()
	}
	authorizationID, err := parseUUIDv7(value.AuthorizationId)
	if err != nil {
		return authorizedUsageBinding{}, err
	}
	featureResourceID, err := parseUUIDv7(value.FeatureResourceId)
	if err != nil {
		return authorizedUsageBinding{}, err
	}
	purpose, period, err := backgroundPurposeAndPeriod(
		value.Purpose,
		value.Period,
	)
	if err != nil {
		return authorizedUsageBinding{}, err
	}
	if value.PeriodStart == nil || value.PeriodStart.CheckValid() != nil {
		return authorizedUsageBinding{}, invalidArgument()
	}
	periodStart := value.PeriodStart.AsTime().UTC()
	if !periodStart.Equal(currentUTCPeriodStart(periodStart)) {
		return authorizedUsageBinding{}, invalidArgument()
	}
	if reserve {
		today := currentUTCPeriodStart(now)
		if !periodStart.Equal(today) &&
			!periodStart.Equal(today.AddDate(0, 0, -1)) {
			return authorizedUsageBinding{}, invalidArgument()
		}
	}
	return authorizedUsageBinding{
		authorizationID:   authorizationID,
		purpose:           purpose,
		featureResourceID: featureResourceID,
		period:            period,
		periodStart:       periodStart,
	}, nil
}

func authorizedUsageDigest(
	serviceIdentityID pgtype.UUID,
	binding authorizedUsageBinding,
	parts ...string,
) []byte {
	return requestDigest(append(
		[]string{
			binding.authorizationID.String(),
			uuid.UUID(serviceIdentityID.Bytes).String(),
			binding.purpose,
			binding.featureResourceID.String(),
			binding.period,
			binding.periodStart.Format(time.RFC3339Nano),
		},
		parts...,
	)...)
}

func lockCurrentBackgroundAuthorization(
	ctx context.Context,
	queries *dbgen.Queries,
	binding authorizedUsageBinding,
	serviceIdentityID pgtype.UUID,
) (dbgen.BackgroundUsageAuthorization, error) {
	current, err := queries.GetBackgroundUsageAuthorization(
		ctx,
		pgUUID(binding.authorizationID),
	)
	if err != nil {
		return dbgen.BackgroundUsageAuthorization{},
			backgroundAuthorizationNotFound(err)
	}
	if err = validateBackgroundAuthorizationBinding(
		current,
		binding,
		serviceIdentityID,
	); err != nil {
		return dbgen.BackgroundUsageAuthorization{}, err
	}
	current, err = queries.LockBackgroundUsageAuthorizationForReserve(
		ctx,
		dbgen.LockBackgroundUsageAuthorizationForReserveParams{
			AuthorizationID:   pgUUID(binding.authorizationID),
			ServiceIdentityID: serviceIdentityID,
			Purpose:           binding.purpose,
			FeatureResourceID: pgUUID(binding.featureResourceID),
			Period:            binding.period,
		},
	)
	if errors.Is(err, pgx.ErrNoRows) {
		latest, readErr := queries.GetBackgroundUsageAuthorization(
			ctx,
			pgUUID(binding.authorizationID),
		)
		if readErr == nil {
			if validationErr := validateBackgroundAuthorizationBinding(
				latest,
				binding,
				serviceIdentityID,
			); validationErr != nil {
				return dbgen.BackgroundUsageAuthorization{}, validationErr
			}
		}
		return dbgen.BackgroundUsageAuthorization{},
			backgroundAuthorizationAccessLost()
	}
	if err != nil {
		return dbgen.BackgroundUsageAuthorization{}, databaseError(err)
	}
	return current, nil
}

func validateBackgroundAuthorizationBinding(
	authorization dbgen.BackgroundUsageAuthorization,
	binding authorizedUsageBinding,
	serviceIdentityID pgtype.UUID,
) error {
	if authorization.ServiceIdentityID != serviceIdentityID ||
		authorization.Purpose != binding.purpose ||
		authorization.FeatureResourceID != pgUUID(binding.featureResourceID) ||
		authorization.Period != binding.period {
		return backgroundAuthorizationSubstitution()
	}
	switch authorization.Status {
	case "active":
		return nil
	case "access_lost", "owner_deleted":
		return backgroundAuthorizationAccessLost()
	default:
		return backgroundAuthorizationStatusInvalid()
	}
}

func lockAuthorizedReservation(
	ctx context.Context,
	queries *dbgen.Queries,
	binding authorizedUsageBinding,
	serviceIdentityID pgtype.UUID,
	reservationID uuid.UUID,
) (dbgen.UsageReservation, error) {
	reservation, err := queries.LockAuthorizedUsageReservation(
		ctx,
		dbgen.LockAuthorizedUsageReservationParams{
			ReservationID:                  pgUUID(reservationID),
			BackgroundUsageAuthorizationID: pgUUID(binding.authorizationID),
			ServiceIdentityID:              serviceIdentityID,
			Purpose: pgtype.Text{
				String: binding.purpose,
				Valid:  true,
			},
			FeatureResourceID: pgUUID(binding.featureResourceID),
			Period: pgtype.Text{
				String: binding.period,
				Valid:  true,
			},
			PeriodStart: pgTimestamp(binding.periodStart),
		},
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return dbgen.UsageReservation{}, backgroundAuthorizationSubstitution()
	}
	if err != nil {
		return dbgen.UsageReservation{}, databaseError(err)
	}
	return reservation, nil
}

func validateBackgroundResourceDeletionBinding(
	authorization dbgen.BackgroundUsageAuthorization,
	serviceIdentityID pgtype.UUID,
	featureResourceID uuid.UUID,
) error {
	if authorization.ServiceIdentityID != serviceIdentityID ||
		authorization.Purpose != backgroundPurposeRealQAStorage ||
		authorization.FeatureResourceID != pgUUID(featureResourceID) {
		return backgroundAuthorizationSubstitution()
	}
	return nil
}

func backgroundAuthorizationActor(
	authorization dbgen.BackgroundUsageAuthorization,
) (safelog.ActorPseudonym, error) {
	if authorization.ActorReference == "" {
		return "", serviceError(connect.CodeInternal, 0)
	}
	return safelog.ActorPseudonym(authorization.ActorReference), nil
}

func logAuthorizedUsageSuccess(
	ctx context.Context,
	dependencies Dependencies,
	authorization dbgen.BackgroundUsageAuthorization,
) {
	if !authorization.ID.Valid {
		return
	}
	actor, err := backgroundAuthorizationActor(authorization)
	if err != nil {
		return
	}
	logBackgroundAuthorizationOutcome(
		ctx,
		dependencies,
		actor,
		uuid.UUID(authorization.OrganizationID.Bytes),
		uuid.UUID(authorization.TeamID.Bytes),
		uuid.UUID(authorization.ServiceIdentityID.Bytes),
		uuid.UUID(authorization.MeterID.Bytes),
		safelog.ResultSuccess,
	)
}
