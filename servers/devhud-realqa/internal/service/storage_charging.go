package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"strconv"
	"time"

	"connectrpc.com/connect"
	realqav1 "github.com/delinoio/oss/protos/devhud-realqa/gen/go/devhud-realqa/v1"
	"github.com/delinoio/oss/servers/devhud-realqa/internal/database/dbgen"
	"github.com/delinoio/oss/servers/devhud-realqa/internal/rqerr"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	storageChargingBatchLimit = 100
	storageGracePeriod        = 30 * 24 * time.Hour
)

func storageClientReference(
	authorizationID uuid.UUID,
	periodStart time.Time,
) string {
	return "realqa-storage:" + authorizationID.String() + ":" +
		periodStart.UTC().Format("2006-01-02")
}

func storageSettlementDigest(
	binding dbgen.RealqaStorageAuthorizationBinding,
	periodStart time.Time,
	units int64,
) ([]byte, error) {
	authorizationID, err := fromPGUUID(binding.AuthorizationID)
	if err != nil {
		return nil, err
	}
	submissionID, err := fromPGUUID(binding.SubmissionID)
	if err != nil {
		return nil, err
	}
	serviceID, err := fromPGUUID(binding.ServiceIdentityID)
	if err != nil {
		return nil, err
	}
	if !periodStart.Equal(utcDayStart(periodStart)) || units < 0 {
		return nil, errors.New("realqa storage billing: invalid UTC checkpoint")
	}
	material := "realqa-storage-settlement:v1:" +
		authorizationID.String() + ":" + serviceID.String() +
		":REALQA_STORAGE:" + submissionID.String() +
		":UTC_DAY:" + periodStart.Format(time.RFC3339) + ":" +
		strconv.FormatInt(units, 10)
	digest := sha256.Sum256([]byte(material))
	return digest[:], nil
}

func storageSettlementKeys(
	authorizationID uuid.UUID,
	periodStart time.Time,
) (uuid.UUID, uuid.UUID, uuid.UUID, error) {
	period := periodStart.UTC().Format("2006-01-02")
	reserve, err := derivedUUIDv7(
		authorizationID, "storage:"+period+":reserve")
	if err != nil {
		return uuid.Nil, uuid.Nil, uuid.Nil, err
	}
	commit, err := derivedUUIDv7(
		authorizationID, "storage:"+period+":commit")
	if err != nil {
		return uuid.Nil, uuid.Nil, uuid.Nil, err
	}
	release, err := derivedUUIDv7(
		authorizationID, "storage:"+period+":release")
	return reserve, commit, release, err
}

// ProcessStorageCharging checkpoints and settles completed UTC days. Every
// downstream operation has a stable authorization/day key, so concurrent
// workers may replay but cannot create a second charge.
func (service *Submission) ProcessStorageCharging(
	ctx context.Context,
	batchLimit int,
) (int, error) {
	if service.dependencies.Store == nil ||
		service.dependencies.Billing == nil {
		return 0, nil
	}
	if batchLimit <= 0 || batchLimit > storageChargingBatchLimit {
		batchLimit = storageChargingBatchLimit
	}
	if err := service.blockDisconnectedGitHubStorage(
		ctx, batchLimit); err != nil {
		return 0, err
	}
	today := utcDayStart(service.dependencies.Clock.Now())
	processed := 0
	var processingErr error
	for processed < batchLimit {
		candidate, err := service.dependencies.Store.Queries().
			GetOldestStorageBillingPeriod(ctx, pgTimestamp(today))
		if errors.Is(err, pgx.ErrNoRows) {
			break
		}
		if err != nil {
			return processed, err
		}
		authorizationID, err := fromPGUUID(candidate.AuthorizationID)
		if err != nil {
			return processed, err
		}
		periodStart, err := time.Parse(time.RFC3339, candidate.PeriodStart)
		if err != nil || !periodStart.Equal(utcDayStart(periodStart)) ||
			!periodStart.Before(today) {
			return processed, errors.New(
				"realqa storage billing: invalid persisted UTC period")
		}
		if err = service.processStoragePeriod(
			ctx, authorizationID, periodStart, today); err != nil {
			processingErr = err
			break
		}
		processed++
	}
	if err := service.expireStorageGrace(ctx, batchLimit); err != nil {
		return processed, errors.Join(processingErr, err)
	}
	if err := service.processStorageClosures(
		ctx, today, batchLimit); err != nil {
		return processed, errors.Join(processingErr, err)
	}
	return processed, processingErr
}

func (service *Submission) processStoragePeriod(
	ctx context.Context,
	authorizationID uuid.UUID,
	periodStart time.Time,
	today time.Time,
) error {
	queries := service.dependencies.Store.Queries()
	binding, err := queries.GetStorageAuthorizationBinding(
		ctx, toPGUUID(authorizationID))
	if err != nil {
		return err
	}
	periodEnd := periodStart.Add(24 * time.Hour)
	byteSeconds, err := queries.CalculateStorageByteSeconds(
		ctx, dbgen.CalculateStorageByteSecondsParams{
			PeriodEnd:       pgTimestamp(periodEnd),
			PeriodStart:     pgTimestamp(periodStart),
			AuthorizationID: toPGUUID(authorizationID),
		})
	if err != nil {
		return err
	}
	units, err := ceilMiBDays(byteSeconds)
	if err != nil {
		return err
	}
	digest, err := storageSettlementDigest(binding, periodStart, units)
	if err != nil {
		return err
	}
	reserveKey, commitKey, releaseKey, err :=
		storageSettlementKeys(authorizationID, periodStart)
	if err != nil {
		return err
	}
	state := "pending"
	if units == 0 {
		state = "settled_zero"
	}
	settlement, err := queries.CreateStorageDailySettlement(
		ctx, dbgen.CreateStorageDailySettlementParams{
			AuthorizationID:       toPGUUID(authorizationID),
			PeriodStart:           pgTimestamp(periodStart),
			ByteSeconds:           byteSeconds,
			Units:                 units,
			State:                 state,
			RequestDigest:         digest,
			ReserveIdempotencyKey: toPGUUID(reserveKey),
			CommitIdempotencyKey:  toPGUUID(commitKey),
			ReleaseIdempotencyKey: toPGUUID(releaseKey),
		})
	if errors.Is(err, pgx.ErrNoRows) {
		settlement, err = queries.GetStorageDailySettlement(
			ctx, dbgen.GetStorageDailySettlementParams{
				AuthorizationID: toPGUUID(authorizationID),
				PeriodStart:     pgTimestamp(periodStart),
			})
	}
	if err != nil {
		return err
	}
	if units > binding.MaximumUnits {
		return service.startStorageGrace(
			ctx, binding, periodStart,
			StorageBillingFailureSecurity, true)
	}
	if settlement.ByteSeconds != byteSeconds ||
		settlement.Units != units ||
		!bytes.Equal(settlement.RequestDigest, digest) ||
		settlement.ReserveIdempotencyKey != toPGUUID(reserveKey) ||
		settlement.CommitIdempotencyKey != toPGUUID(commitKey) ||
		settlement.ReleaseIdempotencyKey != toPGUUID(releaseKey) {
		return service.startStorageGrace(
			ctx, binding, periodStart,
			StorageBillingFailureSecurity, true)
	}
	switch settlement.State {
	case "committed", "released", "settled_zero", "grace_skipped":
		return nil
	case "pending":
		// A completed day older than yesterday is no longer reservable by
		// delibase. Grace begins at that missed day's start and is never
		// reconstructed or back-billed.
		if !periodStart.Equal(today.Add(-24 * time.Hour)) {
			return service.startStorageGrace(
				ctx, binding, periodStart,
				StorageBillingFailureUnavailable, true)
		}
		return service.reserveAndCommitStorage(
			ctx, binding, settlement, periodStart, periodEnd)
	case "reserved":
		return service.commitReservedStorage(
			ctx, binding, settlement, periodStart, periodEnd)
	default:
		return errors.New("realqa storage billing: invalid settlement state")
	}
}

func (service *Submission) reserveAndCommitStorage(
	ctx context.Context,
	binding dbgen.RealqaStorageAuthorizationBinding,
	settlement dbgen.RealqaStorageDailySettlement,
	periodStart time.Time,
	periodEnd time.Time,
) error {
	authorizationID, _ := fromPGUUID(binding.AuthorizationID)
	submissionID, _ := fromPGUUID(binding.SubmissionID)
	reserveKey, err := fromPGUUID(settlement.ReserveIdempotencyKey)
	if err != nil {
		return err
	}
	meters, err := service.dependencies.Billing.Meters(ctx)
	if err != nil {
		if graceErr := service.startStorageGrace(
			ctx, binding, periodEnd,
			StorageBillingFailureUnavailable, false,
		); graceErr != nil {
			return graceErr
		}
		return errors.New(
			"realqa storage billing: meter lookup unavailable")
	}
	if validateBillingMeters(meters) != nil ||
		toPGUUID(meters.Storage.ID) != binding.MeterID ||
		toPGUUID(meters.Storage.ServiceIdentityID) !=
			binding.ServiceIdentityID {
		return service.startStorageGrace(
			ctx, binding, periodStart,
			StorageBillingFailureSecurity, true)
	}
	reserved, err := service.dependencies.Billing.ReserveAuthorizedStorage(
		ctx, AuthorizedStorageUsageRequest{
			AuthorizationID:   authorizationID,
			FeatureResourceID: submissionID,
			PeriodStart:       periodStart,
			Units:             settlement.Units,
			ClientReference: storageClientReference(
				authorizationID, periodStart),
			IdempotencyKey: reserveKey,
		})
	if err != nil {
		if storageFailureKind(err) == StorageBillingFailureUnavailable {
			if graceErr := service.startStorageGrace(
				ctx, binding, periodEnd,
				StorageBillingFailureUnavailable, false,
			); graceErr != nil {
				return graceErr
			}
			return err
		}
		return service.startStorageGrace(
			ctx, binding, periodStart, storageFailureKind(err), true)
	}
	if err = validateAuthorizedStorageReservation(
		reserved, binding, meters.Storage, settlement,
		periodStart, "active", 0); err != nil {
		return service.startStorageGrace(
			ctx, binding, periodStart,
			StorageBillingFailureSecurity, true)
	}
	persisted, err := service.dependencies.Store.Queries().
		SetStorageDailyReservation(
			ctx, dbgen.SetStorageDailyReservationParams{
				ReservationID:        toPGUUID(reserved.ID),
				ReservationCreatedAt: pgTimestamp(reserved.CreatedAt),
				ReservationExpiresAt: pgTimestamp(reserved.ExpiresAt),
				ReservationPriceVersionID: toPGUUID(
					reserved.PriceVersionID),
				AuthorizationID: binding.AuthorizationID,
				PeriodStart:     settlement.PeriodStart,
			})
	reservationStored := err == nil
	if errors.Is(err, pgx.ErrNoRows) {
		persisted, err = service.dependencies.Store.Queries().
			GetStorageDailySettlement(
				ctx, dbgen.GetStorageDailySettlementParams{
					AuthorizationID: binding.AuthorizationID,
					PeriodStart:     settlement.PeriodStart,
				})
	}
	if err != nil {
		return err
	}
	if persisted.ReservationID == toPGUUID(reserved.ID) {
		switch persisted.State {
		case "committed", "released":
			return nil
		case "reserved":
			if reservationStored {
				audit(ctx, service.dependencies, caller{},
					"storage_daily_reserved",
					owner{
						kind: binding.OwnerKind,
						id:   uuid.UUID(binding.OwnerID.Bytes),
					},
					authorizationID, "allow", "success")
			}
			return service.commitReservedStorage(
				ctx, binding, persisted, periodStart, periodEnd)
		}
	}
	if persisted.State != "reserved" ||
		persisted.ReservationID != toPGUUID(reserved.ID) {
		// The local checkpoint changed after the downstream hold. Release
		// that exact hold instead of leaking credit; the stable release key
		// makes response-loss replay safe.
		return service.releaseRacedStorageReservation(
			ctx, binding, settlement, reserved, periodStart)
	}
	return errors.New(
		"realqa storage billing: invalid persisted reservation state")
}

func (service *Submission) commitReservedStorage(
	ctx context.Context,
	binding dbgen.RealqaStorageAuthorizationBinding,
	settlement dbgen.RealqaStorageDailySettlement,
	periodStart time.Time,
	periodEnd time.Time,
) error {
	authorizationID, err := fromPGUUID(binding.AuthorizationID)
	if err != nil {
		return err
	}
	submissionID, err := fromPGUUID(binding.SubmissionID)
	if err != nil {
		return err
	}
	reservationID, err := fromPGUUID(settlement.ReservationID)
	if err != nil {
		return err
	}
	commitKey, err := fromPGUUID(settlement.CommitIdempotencyKey)
	if err != nil {
		return err
	}
	meter, err := persistedStorageReservationMeter(binding, settlement)
	if err != nil {
		if graceErr := service.startStorageGrace(
			ctx, binding, periodEnd,
			StorageBillingFailureSecurity, false,
		); graceErr != nil {
			return graceErr
		}
		return errors.New(
			"realqa storage billing: invalid persisted reservation meter")
	}
	committed, err := service.dependencies.Billing.CommitAuthorizedStorage(
		ctx, AuthorizedStorageFinalizationRequest{
			AuthorizationID:   authorizationID,
			FeatureResourceID: submissionID,
			PeriodStart:       periodStart,
			ReservationID:     reservationID,
			Units:             settlement.Units,
			IdempotencyKey:    commitKey,
		})
	if err != nil {
		if storageFailureKind(err) == StorageBillingFailureUnavailable {
			if graceErr := service.startStorageGrace(
				ctx, binding, periodEnd,
				StorageBillingFailureUnavailable, false,
			); graceErr != nil {
				return graceErr
			}
			return err
		}
		if graceErr := service.startStorageGrace(
			ctx, binding, periodEnd, storageFailureKind(err), false,
		); graceErr != nil {
			return graceErr
		}
		return err
	}
	if err = validateAuthorizedStorageReservation(
		committed, binding, meter, settlement,
		periodStart, "committed", settlement.Units); err != nil {
		if graceErr := service.startStorageGrace(
			ctx, binding, periodEnd,
			StorageBillingFailureSecurity, false,
		); graceErr != nil {
			return graceErr
		}
		return err
	}
	committedNow, err := service.persistCommittedStorage(
		ctx, binding, settlement,
		service.dependencies.Clock.Now().UTC())
	if err != nil {
		return err
	}
	if committedNow {
		audit(ctx, service.dependencies, caller{}, "storage_daily_committed",
			owner{kind: binding.OwnerKind, id: uuid.UUID(binding.OwnerID.Bytes)},
			authorizationID, "allow", "success")
	}
	return nil
}

func (service *Submission) persistCommittedStorage(
	ctx context.Context,
	binding dbgen.RealqaStorageAuthorizationBinding,
	settlement dbgen.RealqaStorageDailySettlement,
	recoveredAt time.Time,
) (bool, error) {
	committed := false
	err := service.dependencies.Store.WithinTransaction(
		ctx, pgx.TxOptions{}, func(queries *dbgen.Queries) error {
			resourceOwner := owner{
				kind: binding.OwnerKind,
				id:   uuid.UUID(binding.OwnerID.Bytes),
			}
			if _, err := lockOwnerScope(
				ctx, queries, resourceOwner); err != nil {
				return err
			}
			payerOwner := owner{
				kind: "organization",
				id:   uuid.UUID(binding.OrganizationID.Bytes),
			}
			if payerOwner != resourceOwner {
				if _, err := lockOwnerScope(
					ctx, queries, payerOwner); err != nil {
					return err
				}
			}
			current, err := queries.LockCurrentStorageAuthorizationBinding(
				ctx, binding.SubmissionID)
			if err != nil {
				return err
			}
			if _, err := queries.CommitStorageDailySettlement(
				ctx, dbgen.CommitStorageDailySettlementParams{
					AuthorizationID: binding.AuthorizationID,
					PeriodStart:     settlement.PeriodStart,
					ReservationID:   settlement.ReservationID,
				}); err != nil {
				if !errors.Is(err, pgx.ErrNoRows) {
					return err
				}
			} else {
				committed = true
			}
			if current.AuthorizationID != binding.AuthorizationID {
				return nil
			}
			recovery, err := queries.GetActiveStorageRecovery(
				ctx, binding.SubmissionID)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			if err != nil || recovery.AuthorizationID !=
				binding.AuthorizationID ||
				!storageCommitResolvesRecovery(recovery.Reason) {
				return err
			}
			if _, err = queries.ResolveStorageRecovery(
				ctx, dbgen.ResolveStorageRecoveryParams{
					RecoveredAt:        pgTimestamp(recoveredAt),
					TargetSubmissionID: binding.SubmissionID,
				}); err != nil {
				return err
			}
			_, err = queries.BeginRetainedSubmissionStorage(
				ctx, dbgen.BeginRetainedSubmissionStorageParams{
					StartsAt:     pgTimestamp(recoveredAt),
					SubmissionID: binding.SubmissionID,
				})
			return err
		})
	return committed, err
}

func persistedStorageReservationMeter(
	binding dbgen.RealqaStorageAuthorizationBinding,
	settlement dbgen.RealqaStorageDailySettlement,
) (BillingMeter, error) {
	meterID, meterErr := fromPGUUID(binding.MeterID)
	priceVersionID, priceErr := fromPGUUID(
		settlement.ReservationPriceVersionID)
	serviceIdentityID, serviceErr := fromPGUUID(binding.ServiceIdentityID)
	if meterErr != nil || priceErr != nil || serviceErr != nil {
		return BillingMeter{}, errors.New(
			"realqa storage billing: invalid persisted reservation meter")
	}
	return BillingMeter{
		ID:                meterID,
		PriceVersionID:    priceVersionID,
		ServiceIdentityID: serviceIdentityID,
	}, nil
}

func storageCommitResolvesRecovery(reason string) bool {
	switch reason {
	case "billing_unavailable", "payment_required", "overage_required":
		return true
	default:
		return false
	}
}

func (service *Submission) releaseReservedStorage(
	ctx context.Context,
	binding dbgen.RealqaStorageAuthorizationBinding,
	settlement dbgen.RealqaStorageDailySettlement,
	periodStart time.Time,
) error {
	authorizationID, err := fromPGUUID(binding.AuthorizationID)
	if err != nil {
		return err
	}
	submissionID, err := fromPGUUID(binding.SubmissionID)
	if err != nil {
		return err
	}
	reservationID, err := fromPGUUID(settlement.ReservationID)
	if err != nil {
		return err
	}
	releaseKey, err := fromPGUUID(settlement.ReleaseIdempotencyKey)
	if err != nil {
		return err
	}
	meters, err := service.dependencies.Billing.Meters(ctx)
	if err != nil || validateBillingMeters(meters) != nil {
		return errors.New(
			"realqa storage billing: release meter unavailable")
	}
	released, err := service.dependencies.Billing.ReleaseAuthorizedStorage(
		ctx, AuthorizedStorageFinalizationRequest{
			AuthorizationID:   authorizationID,
			FeatureResourceID: submissionID,
			PeriodStart:       periodStart,
			ReservationID:     reservationID,
			Units:             settlement.Units,
			IdempotencyKey:    releaseKey,
		})
	if err != nil {
		return err
	}
	if err = validateAuthorizedStorageReservation(
		released, binding, meters.Storage, settlement,
		periodStart, "released", 0); err != nil {
		return err
	}
	_, err = service.dependencies.Store.Queries().
		ReleaseStorageDailySettlement(
			ctx, dbgen.ReleaseStorageDailySettlementParams{
				AuthorizationID: binding.AuthorizationID,
				PeriodStart:     settlement.PeriodStart,
				ReservationID:   settlement.ReservationID,
			})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	audit(ctx, service.dependencies, caller{}, "storage_daily_released",
		owner{kind: binding.OwnerKind, id: uuid.UUID(binding.OwnerID.Bytes)},
		authorizationID, "allow", "success")
	return nil
}

func (service *Submission) releaseRacedStorageReservation(
	ctx context.Context,
	binding dbgen.RealqaStorageAuthorizationBinding,
	settlement dbgen.RealqaStorageDailySettlement,
	reserved AuthorizedStorageReservation,
	periodStart time.Time,
) error {
	authorizationID, _ := fromPGUUID(binding.AuthorizationID)
	submissionID, _ := fromPGUUID(binding.SubmissionID)
	releaseKey, err := fromPGUUID(settlement.ReleaseIdempotencyKey)
	if err != nil {
		return err
	}
	released, err := service.dependencies.Billing.ReleaseAuthorizedStorage(
		ctx, AuthorizedStorageFinalizationRequest{
			AuthorizationID:   authorizationID,
			FeatureResourceID: submissionID,
			PeriodStart:       periodStart,
			ReservationID:     reserved.ID,
			Units:             settlement.Units,
			IdempotencyKey:    releaseKey,
		})
	if err != nil || released.Status != "released" ||
		released.ID != reserved.ID {
		return errors.New(
			"realqa storage billing: raced reservation release failed")
	}
	_, _ = service.dependencies.Store.Queries().
		ReleaseStorageDailySettlement(
			ctx, dbgen.ReleaseStorageDailySettlementParams{
				AuthorizationID: binding.AuthorizationID,
				PeriodStart:     settlement.PeriodStart,
				ReservationID:   toPGUUID(reserved.ID),
			})
	audit(ctx, service.dependencies, caller{}, "storage_daily_released",
		owner{kind: binding.OwnerKind, id: uuid.UUID(binding.OwnerID.Bytes)},
		authorizationID, "allow", "success")
	return nil
}

func validateAuthorizedStorageReservation(
	value AuthorizedStorageReservation,
	binding dbgen.RealqaStorageAuthorizationBinding,
	meter BillingMeter,
	settlement dbgen.RealqaStorageDailySettlement,
	periodStart time.Time,
	status string,
	committedUnits int64,
) error {
	authorizationID, authorizationErr := fromPGUUID(binding.AuthorizationID)
	submissionID, submissionErr := fromPGUUID(binding.SubmissionID)
	organizationID, organizationErr := fromPGUUID(binding.OrganizationID)
	teamID, teamErr := fromPGUUID(binding.TeamID)
	accountID, accountErr := fromPGUUID(binding.AuthorizerAccountID)
	reservationID := value.ID
	if settlement.ReservationID.Valid {
		reservationID, _ = fromPGUUID(settlement.ReservationID)
	}
	if authorizationErr != nil || submissionErr != nil ||
		organizationErr != nil || teamErr != nil || accountErr != nil ||
		value.ID == uuid.Nil || value.ID.Version() != 7 ||
		(settlement.ReservationID.Valid && value.ID != reservationID) ||
		value.AuthorizationID != authorizationID ||
		value.FeatureResourceID != submissionID ||
		!value.PeriodStart.Equal(periodStart) ||
		value.OrganizationID != organizationID ||
		value.TeamID != teamID ||
		value.MeterID != meter.ID ||
		value.PriceVersionID != meter.PriceVersionID ||
		value.UserAccountID != accountID ||
		value.ServiceIdentityID != meter.ServiceIdentityID ||
		value.MaximumUnits != settlement.Units ||
		value.CommittedUnits != committedUnits ||
		value.USDMicrosPerUnit != storagePriceUSDMicros ||
		value.ClientReference != storageClientReference(
			authorizationID, periodStart) ||
		value.Status != status ||
		value.CreatedAt.IsZero() || value.ExpiresAt.IsZero() ||
		!value.ExpiresAt.After(value.CreatedAt) {
		return errors.New(
			"realqa storage billing: authorized reservation substitution")
	}
	if settlement.ReservationCreatedAt.Valid &&
		!value.CreatedAt.Truncate(time.Microsecond).Equal(
			settlement.ReservationCreatedAt.Time) {
		return errors.New(
			"realqa storage billing: reservation creation changed")
	}
	if settlement.ReservationExpiresAt.Valid &&
		!value.ExpiresAt.Truncate(time.Microsecond).Equal(
			settlement.ReservationExpiresAt.Time) {
		return errors.New(
			"realqa storage billing: reservation expiry changed")
	}
	return nil
}

func storageFailureKind(err error) StorageBillingFailureKind {
	var failure *StorageBillingFailure
	if errors.As(err, &failure) {
		return failure.Kind
	}
	return StorageBillingFailureUnavailable
}

func storageRecoveryReason(kind StorageBillingFailureKind) string {
	switch kind {
	case StorageBillingFailureAuthorization:
		return "authorization_revoked"
	case StorageBillingFailureAccess:
		return "authorization_access_lost"
	case StorageBillingFailurePayment:
		return "payment_required"
	case StorageBillingFailureOverage:
		return "overage_required"
	case StorageBillingFailureSecurity:
		return "security_conflict"
	case StorageBillingFailureGitHub:
		return "github_disconnected"
	default:
		return "billing_unavailable"
	}
}

// HandleGitHubConnectionDeletion cuts off storage at the disconnect instant
// before trying the human-authorized revoke. A missing/failed revoke therefore
// cannot extend billable accrual or permit new submissions, and rebind exposes
// the durable recovery path without retaining the caller's bearer.
func (service *Submission) HandleGitHubConnectionDeletion(
	ctx context.Context,
	connectionID uuid.UUID,
	forwardedBearer string,
) error {
	if service.dependencies.Store == nil ||
		service.dependencies.Billing == nil {
		return nil
	}
	bindings, err := service.dependencies.Store.Queries().
		ListCurrentStorageBindingsForGitHubConnection(
			ctx, toPGUUID(connectionID))
	if err != nil {
		return err
	}
	cutoff := service.dependencies.Clock.Now().UTC()
	for _, binding := range bindings {
		if err = service.startStorageGrace(
			ctx, binding, cutoff, StorageBillingFailureGitHub, false,
		); err != nil {
			return err
		}
		if err = service.settleStorageCutoff(
			ctx, binding, cutoff); err != nil {
			return err
		}
		if forwardedBearer == "" {
			continue
		}
		authorizationID, parseErr := fromPGUUID(binding.AuthorizationID)
		if parseErr != nil {
			return parseErr
		}
		current, revokeErr :=
			service.dependencies.Billing.GetStorageAuthorization(
				ctx, StorageAuthorizationLookupRequest{
					AuthorizationID: authorizationID,
					ForwardedBearer: forwardedBearer,
				})
		if revokeErr != nil ||
			validateBoundStorageAuthorization(
				current, binding, false) != nil {
			continue
		}
		if current.Status == "active" {
			key, keyErr := derivedUUIDv7(
				authorizationID, "github-connection-revoke")
			if keyErr != nil {
				return keyErr
			}
			current, revokeErr =
				service.dependencies.Billing.RevokeStorageAuthorization(
					ctx, StorageAuthorizationRevokeRequest{
						AuthorizationID:  authorizationID,
						ExpectedRevision: current.Revision,
						IdempotencyKey:   key,
						ForwardedBearer:  forwardedBearer,
					})
			if revokeErr != nil ||
				validateBoundStorageAuthorization(
					current, binding, false) != nil {
				continue
			}
		}
		if current.Status == "revoked" ||
			current.Status == "access_lost" ||
			current.Status == "resource_deleted" ||
			current.Status == "owner_deleted" {
			_, _ = service.dependencies.Store.Queries().
				UpdateStorageAuthorizationStatus(
					ctx, dbgen.UpdateStorageAuthorizationStatusParams{
						Status:                current.Status,
						AuthorizationRevision: current.Revision,
						AuthorizationID:       binding.AuthorizationID,
					})
		}
	}
	return nil
}

// settleStorageCutoff materializes the one partial UTC day whose half-open
// retained intervals end at a grace/rebind cutoff. The daily keys and digest
// are identical to the rollover worker, and the synthetic next-midnight
// boundary only permits the already-current day through the same path.
func (service *Submission) settleStorageCutoff(
	ctx context.Context,
	binding dbgen.RealqaStorageAuthorizationBinding,
	cutoff time.Time,
) error {
	cutoff = cutoff.UTC()
	periodStart := utcDayStart(cutoff)
	if !cutoff.After(periodStart) {
		return nil
	}
	authorizationID, err := fromPGUUID(binding.AuthorizationID)
	if err != nil {
		return err
	}
	if err = service.processStoragePeriod(
		ctx, authorizationID, periodStart,
		periodStart.Add(24*time.Hour)); err != nil {
		return err
	}
	settlement, err := service.dependencies.Store.Queries().
		GetStorageDailySettlement(
			ctx, dbgen.GetStorageDailySettlementParams{
				AuthorizationID: binding.AuthorizationID,
				PeriodStart:     pgTimestamp(periodStart),
			})
	if err != nil {
		return err
	}
	switch settlement.State {
	case "committed", "released", "settled_zero", "grace_skipped":
		return nil
	default:
		return errors.New(
			"realqa storage billing: cutoff settlement is unresolved")
	}
}

func (service *Submission) blockDisconnectedGitHubStorage(
	ctx context.Context,
	batchLimit int,
) error {
	disconnected, err := service.dependencies.Store.Queries().
		ListOpenStorageBindingsForDisconnectedGitHub(
			ctx, int32(batchLimit))
	if err != nil {
		return err
	}
	for _, row := range disconnected {
		binding := row.RealqaStorageAuthorizationBinding
		cutoff := row.GithubDisconnectedAt.Time.UTC()
		if err = service.startStorageGrace(
			ctx, binding, cutoff, StorageBillingFailureGitHub, false,
		); err != nil {
			return err
		}
		if err = service.settleStorageCutoff(
			ctx, binding, cutoff); err != nil {
			continue
		}
	}
	return nil
}

// HandleLifecycleAuthorizationDeletion blocks grants authored or paid by a
// deleted lifecycle subject when the feature resource belongs to somebody
// else. Owner-scoped resources use the immediate resource-deletion path.
func (service *Submission) HandleLifecycleAuthorizationDeletion(
	ctx context.Context,
	scope owner,
	cutoff time.Time,
) error {
	var (
		bindings []dbgen.RealqaStorageAuthorizationBinding
		err      error
	)
	switch scope.kind {
	case "personal":
		bindings, err = service.dependencies.Store.Queries().
			ListCurrentStorageBindingsForDeletedAuthorizer(
				ctx, toPGUUID(scope.id))
	case "organization":
		bindings, err = service.dependencies.Store.Queries().
			ListCurrentStorageBindingsForDeletedPayer(
				ctx, toPGUUID(scope.id))
	default:
		return errors.New(
			"realqa storage billing: invalid lifecycle scope")
	}
	if err != nil {
		return err
	}
	for _, binding := range bindings {
		if err = service.startStorageGrace(
			ctx, binding, cutoff,
			StorageBillingFailureAccess, false,
		); err != nil {
			return err
		}
	}
	return nil
}

func (service *Submission) startStorageGrace(
	ctx context.Context,
	binding dbgen.RealqaStorageAuthorizationBinding,
	graceStart time.Time,
	kind StorageBillingFailureKind,
	skipUnreserved bool,
) error {
	graceStart = graceStart.UTC()
	recoveryStartedAt := service.dependencies.Clock.Now().UTC()
	recoveryID, err := newID(service.dependencies)
	if err != nil {
		return err
	}
	authorizationID, err := fromPGUUID(binding.AuthorizationID)
	if err != nil {
		return err
	}
	applied := false
	err = service.dependencies.Store.WithinTransaction(
		ctx, pgx.TxOptions{}, func(queries *dbgen.Queries) error {
			resourceOwner := owner{
				kind: binding.OwnerKind,
				id:   uuid.UUID(binding.OwnerID.Bytes),
			}
			if _, lockErr := lockOwnerScope(
				ctx, queries, resourceOwner); lockErr != nil {
				return lockErr
			}
			payerOwner := owner{
				kind: "organization",
				id:   uuid.UUID(binding.OrganizationID.Bytes),
			}
			if payerOwner != resourceOwner {
				if _, lockErr := lockOwnerScope(
					ctx, queries, payerOwner); lockErr != nil {
					return lockErr
				}
			}
			current, lockErr := queries.LockCurrentStorageAuthorizationBinding(
				ctx, binding.SubmissionID)
			if lockErr != nil {
				return lockErr
			}
			if current.AuthorizationID != binding.AuthorizationID {
				// An old accepted reservation may still finish after rebind,
				// but it must not place the replacement mapping into grace.
				return nil
			}
			if skipUnreserved {
				_, _ = queries.SkipStorageDailySettlementForGrace(
					ctx, dbgen.SkipStorageDailySettlementForGraceParams{
						AuthorizationID: binding.AuthorizationID,
						PeriodStart:     pgTimestamp(utcDayStart(graceStart)),
					})
			}
			if _, lockErr = queries.CutoffStorageAuthorizationAccrual(
				ctx, dbgen.CutoffStorageAuthorizationAccrualParams{
					Cutoff:          pgTimestamp(graceStart),
					AuthorizationID: binding.AuthorizationID,
				}); lockErr != nil {
				return lockErr
			}
			if _, lockErr = queries.CloseStorageRetentionForSubmission(
				ctx, dbgen.CloseStorageRetentionForSubmissionParams{
					Cutoff:       pgTimestamp(graceStart),
					SubmissionID: binding.SubmissionID,
				}); lockErr != nil {
				return lockErr
			}
			reason := storageRecoveryReason(kind)
			recoveryGraceStartedAt := recoveryStartedAt
			active, activeErr := queries.GetActiveStorageRecovery(
				ctx, binding.SubmissionID)
			if activeErr == nil &&
				active.AuthorizationID == binding.AuthorizationID &&
				active.Reason == "billing_unavailable" &&
				reason != "billing_unavailable" {
				recoveryGraceStartedAt = active.GraceStartedAt.Time
				if _, lockErr = queries.SupersedeStorageRecovery(
					ctx, dbgen.SupersedeStorageRecoveryParams{
						RecoveredAt:     pgTimestamp(recoveryStartedAt),
						SubmissionID:    binding.SubmissionID,
						AuthorizationID: binding.AuthorizationID,
					}); lockErr != nil {
					return lockErr
				}
			} else if activeErr != nil &&
				!errors.Is(activeErr, pgx.ErrNoRows) {
				return activeErr
			}
			if _, lockErr = queries.CreateStorageRecovery(
				ctx, dbgen.CreateStorageRecoveryParams{
					ID:              toPGUUID(recoveryID),
					SubmissionID:    binding.SubmissionID,
					AuthorizationID: binding.AuthorizationID,
					Reason:          reason,
					GraceStartedAt:  pgTimestamp(recoveryGraceStartedAt),
				}); lockErr != nil {
				return lockErr
			}
			_, lockErr = queries.SetSubmissionStorageBillingGrace(
				ctx, binding.SubmissionID)
			applied = lockErr == nil
			return lockErr
		})
	if err != nil {
		return err
	}
	if !applied {
		return nil
	}
	audit(ctx, service.dependencies, caller{},
		"storage_billing_grace_started",
		owner{kind: binding.OwnerKind, id: uuid.UUID(binding.OwnerID.Bytes)},
		authorizationID, "deny", "failure")
	service.dependencies.Logger.WarnContext(
		ctx,
		"RealQA storage billing entered grace",
		"event", "storage_billing_grace_started",
		"failure_kind", storageRecoveryReason(kind),
	)
	return nil
}

func (service *Submission) expireStorageGrace(
	ctx context.Context,
	batchLimit int,
) error {
	now := service.dependencies.Clock.Now().UTC()
	rows, err := service.dependencies.Store.Queries().
		ListExpiredStorageRecoveries(
			ctx, dbgen.ListExpiredStorageRecoveriesParams{
				Cutoff: pgTimestamp(now), BatchLimit: int32(batchLimit),
			})
	if err != nil {
		return err
	}
	for _, recovery := range rows {
		if err = service.expireStorageRecovery(ctx, recovery, now); err != nil {
			return err
		}
	}
	return nil
}

func (service *Submission) expireStorageRecovery(
	ctx context.Context,
	recovery dbgen.RealqaStorageRecovery,
	now time.Time,
) error {
	submissionID, err := fromPGUUID(recovery.SubmissionID)
	if err != nil {
		return err
	}
	// Tombstoning and object deletion intentionally precede delibase closure,
	// so an outage can never extend public readability.
	_, err = service.deleteBillingExpiredAssets(
		ctx, submissionID, &storageRecoveryExpiryClaim{
			ID: recovery.ID, Cutoff: now,
		})
	return err
}

func (service *Submission) processStorageClosures(
	ctx context.Context,
	completedThrough time.Time,
	batchLimit int,
) error {
	rows, err := service.dependencies.Store.Queries().
		ListStorageClosureCandidates(
			ctx, dbgen.ListStorageClosureCandidatesParams{
				CompletedThrough: pgTimestamp(completedThrough),
				BatchLimit:       int32(batchLimit),
			})
	if err != nil {
		return err
	}
	for _, binding := range rows {
		authorizationID, parseErr := fromPGUUID(binding.AuthorizationID)
		if parseErr != nil {
			return parseErr
		}
		submissionID, parseErr := fromPGUUID(binding.SubmissionID)
		if parseErr != nil {
			return parseErr
		}
		key, parseErr := derivedUUIDv7(
			authorizationID, "storage-resource-deleted")
		if parseErr != nil {
			return parseErr
		}
		closed, closeErr :=
			service.dependencies.Billing.MarkStorageResourceDeleted(
				ctx, StorageResourceDeletedRequest{
					AuthorizationID:   authorizationID,
					FeatureResourceID: submissionID,
					ExpectedRevision:  binding.AuthorizationRevision,
					IdempotencyKey:    key,
				})
		if closeErr != nil {
			continue
		}
		if validateBoundStorageAuthorization(
			closed, binding, false) != nil ||
			!storageClosureStatusAllowed(
				closed.Status, binding.ClosureOwnerDeletedAllowed) {
			continue
		}
		if _, closeErr = service.dependencies.Store.Queries().
			CompleteStorageAuthorizationClosure(
				ctx, dbgen.CompleteStorageAuthorizationClosureParams{
					Status:                closed.Status,
					AuthorizationRevision: closed.Revision,
					AuthorizationID:       binding.AuthorizationID,
				}); closeErr != nil && !errors.Is(closeErr, pgx.ErrNoRows) {
			return closeErr
		}
		audit(ctx, service.dependencies, caller{},
			"storage_authorization_closed",
			owner{
				kind: binding.OwnerKind,
				id:   uuid.UUID(binding.OwnerID.Bytes),
			},
			authorizationID, "allow", "success")
	}
	return nil
}

func validateBoundStorageAuthorization(
	value StorageAuthorization,
	binding dbgen.RealqaStorageAuthorizationBinding,
	requireActive bool,
) error {
	authorizationID, authorizationErr := fromPGUUID(binding.AuthorizationID)
	authorizerID, authorizerErr := fromPGUUID(binding.AuthorizerAccountID)
	ownerID, ownerErr := fromPGUUID(binding.OwnerID)
	organizationID, organizationErr := fromPGUUID(binding.OrganizationID)
	teamID, teamErr := fromPGUUID(binding.TeamID)
	serviceID, serviceErr := fromPGUUID(binding.ServiceIdentityID)
	meterID, meterErr := fromPGUUID(binding.MeterID)
	submissionID, submissionErr := fromPGUUID(binding.SubmissionID)
	if authorizationErr != nil || authorizerErr != nil || ownerErr != nil ||
		organizationErr != nil || teamErr != nil || serviceErr != nil ||
		meterErr != nil || submissionErr != nil ||
		value.ID != authorizationID ||
		value.AuthorizerAccountID != authorizerID ||
		value.OwnerKind != binding.OwnerKind || value.OwnerID != ownerID ||
		value.OrganizationID != organizationID || value.TeamID != teamID ||
		value.ServiceIdentityID != serviceID || value.MeterID != meterID ||
		value.FeatureResourceID != submissionID ||
		value.MaximumUnits != binding.MaximumUnits ||
		value.Revision < binding.AuthorizationRevision ||
		(requireActive && value.Status != "active") {
		return errors.New(
			"realqa storage billing: authorization substitution")
	}
	return nil
}

func storageClosureStatusAllowed(
	status string,
	ownerDeletedAllowed bool,
) bool {
	return status == "resource_deleted" || status == "revoked" ||
		status == "access_lost" ||
		(ownerDeletedAllowed && status == "owner_deleted")
}

// RunStorageCharging starts one immediate pass and then repeats. Errors are
// recorded with credential-safe structured fields and retried on the next
// pass; they do not stop grace-expiry object deletion in later passes.
func (service *Submission) RunStorageCharging(
	ctx context.Context,
	interval time.Duration,
) {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	run := func() {
		if _, err := service.ProcessStorageCharging(
			ctx, storageChargingBatchLimit); err != nil &&
			!errors.Is(err, context.Canceled) {
			service.dependencies.Logger.ErrorContext(
				ctx,
				"RealQA recurring storage charging failed",
				"event", "storage_charging_failure",
			)
		}
	}
	run()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

func storageBillingGraceError() error {
	return rqerr.New(
		connect.CodeFailedPrecondition,
		realqav1.ErrorReason_ERROR_REASON_STORAGE_BILLING_GRACE,
		realqav1.FailureClass_FAILURE_CLASS_USER_ACTION_REQUIRED,
		0,
	)
}
