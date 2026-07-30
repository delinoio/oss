package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"math"
	"time"

	"connectrpc.com/connect"
	realqav1 "github.com/delinoio/oss/protos/devhud-realqa/gen/go/devhud-realqa/v1"
	"github.com/delinoio/oss/servers/devhud-realqa/internal/database/dbgen"
	"github.com/delinoio/oss/servers/devhud-realqa/internal/rqerr"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	bytesPerMiB                int64 = 1_048_576
	secondsPerUTCDay           int64 = 86_400
	byteSecondsPerMiBDay             = bytesPerMiB * secondsPerUTCDay
	transferPriceUSDMicros     int64 = 500
	storagePriceUSDMicros      int64 = 2
	minimumTransferReservation       = 24 * time.Hour
)

type BillingMeter struct {
	ID                    uuid.UUID
	PriceVersionID        uuid.UUID
	ServiceIdentityID     uuid.UUID
	Key                   string
	Unit                  string
	Precision             int32
	USDMicrosPerUnit      int64
	ReservationTTLSeconds int64
	Enabled               bool
}

type BillingMeters struct {
	Transfer BillingMeter
	Storage  BillingMeter
}

type TransferReservationRequest struct {
	OrganizationID  uuid.UUID
	TeamID          uuid.UUID
	MeterID         uuid.UUID
	MaximumUnits    int64
	ClientReference string
	IdempotencyKey  uuid.UUID
	ForwardedBearer string
}

type TransferReservation struct {
	ID                uuid.UUID
	OrganizationID    uuid.UUID
	TeamID            uuid.UUID
	MeterID           uuid.UUID
	PriceVersionID    uuid.UUID
	UserAccountID     uuid.UUID
	ServiceIdentityID uuid.UUID
	MaximumUnits      int64
	CommittedUnits    int64
	USDMicrosPerUnit  int64
	ClientReference   string
	Status            string
	CreatedAt         time.Time
	ExpiresAt         time.Time
}

type TransferCommitRequest struct {
	OrganizationID  uuid.UUID
	ReservationID   uuid.UUID
	ActualUnits     int64
	IdempotencyKey  uuid.UUID
	ForwardedBearer string
}

type TransferReleaseRequest struct {
	OrganizationID  uuid.UUID
	ReservationID   uuid.UUID
	IdempotencyKey  uuid.UUID
	ForwardedBearer string
}

type StorageAuthorizationRequest struct {
	OwnerKind         string
	OwnerID           uuid.UUID
	OrganizationID    uuid.UUID
	TeamID            uuid.UUID
	ServiceIdentityID uuid.UUID
	MeterID           uuid.UUID
	FeatureResourceID uuid.UUID
	MaximumUnits      int64
	IdempotencyKey    uuid.UUID
	ForwardedBearer   string
}

type StorageAuthorization struct {
	ID                  uuid.UUID
	AuthorizerAccountID uuid.UUID
	OwnerKind           string
	OwnerID             uuid.UUID
	OrganizationID      uuid.UUID
	TeamID              uuid.UUID
	ServiceIdentityID   uuid.UUID
	MeterID             uuid.UUID
	FeatureResourceID   uuid.UUID
	MaximumUnits        int64
	Status              string
	Revision            int64
}

type StorageAuthorizationLookupRequest struct {
	AuthorizationID uuid.UUID
	ForwardedBearer string
}

type StorageAuthorizationRevokeRequest struct {
	AuthorizationID  uuid.UUID
	ExpectedRevision int64
	IdempotencyKey   uuid.UUID
	ForwardedBearer  string
}

type AuthorizedStorageUsageRequest struct {
	AuthorizationID   uuid.UUID
	FeatureResourceID uuid.UUID
	PeriodStart       time.Time
	Units             int64
	ClientReference   string
	IdempotencyKey    uuid.UUID
}

type AuthorizedStorageFinalizationRequest struct {
	AuthorizationID   uuid.UUID
	FeatureResourceID uuid.UUID
	PeriodStart       time.Time
	ReservationID     uuid.UUID
	Units             int64
	IdempotencyKey    uuid.UUID
}

type StorageResourceDeletedRequest struct {
	AuthorizationID   uuid.UUID
	FeatureResourceID uuid.UUID
	ExpectedRevision  int64
	IdempotencyKey    uuid.UUID
}

type AuthorizedStorageReservation struct {
	TransferReservation
	AuthorizationID   uuid.UUID
	FeatureResourceID uuid.UUID
	PeriodStart       time.Time
}

type StorageBillingFailureKind string

const (
	StorageBillingFailureAuthorization StorageBillingFailureKind = "authorization"
	StorageBillingFailureAccess        StorageBillingFailureKind = "access"
	StorageBillingFailurePayment       StorageBillingFailureKind = "payment"
	StorageBillingFailureOverage       StorageBillingFailureKind = "overage"
	StorageBillingFailureUnavailable   StorageBillingFailureKind = "unavailable"
	StorageBillingFailureSecurity      StorageBillingFailureKind = "security"
	StorageBillingFailureGitHub        StorageBillingFailureKind = "github"
)

type StorageBillingFailure struct {
	Kind StorageBillingFailureKind
}

func (failure *StorageBillingFailure) Error() string {
	return "realqa storage billing: authorized usage failed"
}

// SubmissionBilling is the narrow delibase boundary. Implementations attach a
// short-lived RealQA M2M bearer and use ForwardedBearer only for this live
// authenticated call. No bearer may be retained by either side.
type SubmissionBilling interface {
	Meters(context.Context) (BillingMeters, error)
	ReserveTransfer(context.Context, TransferReservationRequest) (TransferReservation, error)
	CommitTransfer(context.Context, TransferCommitRequest) (TransferReservation, error)
	ReleaseTransfer(context.Context, TransferReleaseRequest) (TransferReservation, error)
	CreateStorageAuthorization(
		context.Context,
		StorageAuthorizationRequest,
	) (StorageAuthorization, error)
	GetStorageAuthorization(
		context.Context,
		StorageAuthorizationLookupRequest,
	) (StorageAuthorization, error)
	RevokeStorageAuthorization(
		context.Context,
		StorageAuthorizationRevokeRequest,
	) (StorageAuthorization, error)
	ReserveAuthorizedStorage(
		context.Context,
		AuthorizedStorageUsageRequest,
	) (AuthorizedStorageReservation, error)
	CommitAuthorizedStorage(
		context.Context,
		AuthorizedStorageFinalizationRequest,
	) (AuthorizedStorageReservation, error)
	ReleaseAuthorizedStorage(
		context.Context,
		AuthorizedStorageFinalizationRequest,
	) (AuthorizedStorageReservation, error)
	MarkStorageResourceDeleted(
		context.Context,
		StorageResourceDeletedRequest,
	) (StorageAuthorization, error)
}

func validateBillingMeters(meters BillingMeters) error {
	if err := validateBillingMeter(
		meters.Transfer, "realqa_image_transfer", "encoded_mib",
		transferPriceUSDMicros, true,
	); err != nil {
		return err
	}
	if meters.Transfer.ServiceIdentityID !=
		meters.Storage.ServiceIdentityID {
		return transferReservationFailed()
	}
	return validateBillingMeter(
		meters.Storage, "realqa_image_storage", "mib_day",
		storagePriceUSDMicros, false,
	)
}

func validateBillingMeter(
	meter BillingMeter,
	key string,
	unit string,
	price int64,
	transfer bool,
) error {
	if meter.ID == uuid.Nil || meter.ID.Version() != 7 ||
		meter.PriceVersionID == uuid.Nil ||
		meter.PriceVersionID.Version() != 7 ||
		meter.ServiceIdentityID == uuid.Nil ||
		meter.ServiceIdentityID.Version() != 7 ||
		meter.Key != key || meter.Unit != unit || meter.Precision != 0 ||
		meter.USDMicrosPerUnit != price || !meter.Enabled {
		return transferReservationFailed()
	}
	if transfer && meter.ReservationTTLSeconds <
		int64(minimumTransferReservation/time.Second) {
		return transferReservationFailed()
	}
	return nil
}

func ceilMiB(encodedBytes int64) (int64, error) {
	if encodedBytes < 0 {
		return 0, errors.New("realqa billing: encoded bytes cannot be negative")
	}
	if encodedBytes == 0 {
		return 0, nil
	}
	if encodedBytes > math.MaxInt64-(bytesPerMiB-1) {
		return 0, errors.New("realqa billing: encoded byte rounding overflow")
	}
	return (encodedBytes + bytesPerMiB - 1) / bytesPerMiB, nil
}

func ceilMiBDays(byteSeconds int64) (int64, error) {
	if byteSeconds < 0 {
		return 0, errors.New(
			"realqa billing: retained byte-seconds cannot be negative")
	}
	if byteSeconds == 0 {
		return 0, nil
	}
	units := byteSeconds / byteSecondsPerMiBDay
	if byteSeconds%byteSecondsPerMiBDay != 0 {
		if units == math.MaxInt64 {
			return 0, errors.New(
				"realqa billing: retained byte-second rounding overflow")
		}
		units++
	}
	return units, nil
}

func utcDayStart(value time.Time) time.Time {
	utc := value.UTC()
	return time.Date(
		utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
}

func derivedUUIDv7(root uuid.UUID, purpose string) (uuid.UUID, error) {
	if root == uuid.Nil || root.Version() != 7 || purpose == "" {
		return uuid.Nil, errors.New("realqa submission: invalid downstream identity")
	}
	digest := sha256.Sum256(append(append(
		[]byte("realqa:submission:v1:"), root[:]...), []byte(purpose)...))
	var result uuid.UUID
	copy(result[:6], root[:6])
	copy(result[6:], digest[:10])
	result[6] = (result[6] & 0x0f) | 0x70
	result[8] = (result[8] & 0x3f) | 0x80
	return result, nil
}

func validateTransferReservation(
	reservation TransferReservation,
	submission dbSubmissionBillingView,
	meters BillingMeters,
	now time.Time,
) error {
	if reservation.ID == uuid.Nil || reservation.ID.Version() != 7 ||
		reservation.OrganizationID != submission.organizationID ||
		reservation.TeamID != submission.teamID ||
		reservation.MeterID != meters.Transfer.ID ||
		reservation.PriceVersionID != meters.Transfer.PriceVersionID ||
		reservation.UserAccountID != submission.accountID ||
		reservation.ServiceIdentityID != meters.Transfer.ServiceIdentityID ||
		reservation.MaximumUnits != submission.reservedUnits ||
		reservation.CommittedUnits != 0 ||
		reservation.USDMicrosPerUnit != transferPriceUSDMicros ||
		reservation.ClientReference != "realqa-transfer:"+
			submission.submissionID.String() ||
		reservation.Status != "active" ||
		reservation.CreatedAt.IsZero() || reservation.ExpiresAt.IsZero() ||
		reservation.ExpiresAt.Sub(reservation.CreatedAt) <
			minimumTransferReservation ||
		!reservation.ExpiresAt.After(now) {
		return transferReservationFailed()
	}
	return nil
}

type dbSubmissionBillingView struct {
	accountID      uuid.UUID
	submissionID   uuid.UUID
	organizationID uuid.UUID
	teamID         uuid.UUID
	reservedUnits  int64
}

func validateStorageAuthorization(
	authorization StorageAuthorization,
	request StorageAuthorizationRequest,
	actor uuid.UUID,
) error {
	if authorization.ID == uuid.Nil || authorization.ID.Version() != 7 ||
		authorization.AuthorizerAccountID != actor ||
		authorization.OwnerKind != request.OwnerKind ||
		authorization.OwnerID != request.OwnerID ||
		authorization.OrganizationID != request.OrganizationID ||
		authorization.TeamID != request.TeamID ||
		authorization.ServiceIdentityID != request.ServiceIdentityID ||
		authorization.MeterID != request.MeterID ||
		authorization.FeatureResourceID != request.FeatureResourceID ||
		authorization.MaximumUnits != request.MaximumUnits ||
		authorization.Status != "active" || authorization.Revision <= 0 {
		return rqerr.New(
			connect.CodeFailedPrecondition,
			realqav1.ErrorReason_ERROR_REASON_STORAGE_AUTHORIZATION_SUBSTITUTION,
			realqav1.FailureClass_FAILURE_CLASS_CONFLICT,
			0,
		)
	}
	return nil
}

func transferReservationFailed() error {
	return rqerr.New(
		connect.CodeUnavailable,
		realqav1.ErrorReason_ERROR_REASON_TRANSFER_RESERVATION_FAILED,
		realqav1.FailureClass_FAILURE_CLASS_RETRYABLE,
		0,
	)
}

func storageAuthorizationFailed() error {
	return rqerr.New(
		connect.CodeUnavailable,
		realqav1.ErrorReason_ERROR_REASON_STORAGE_AUTHORIZATION_FAILED,
		realqav1.FailureClass_FAILURE_CLASS_RETRYABLE,
		0,
	)
}

func (service *Submission) ensureTransferReservation(
	ctx context.Context,
	actor caller,
	submissionID uuid.UUID,
	meters BillingMeters,
) (*realqav1.Submission, error) {
	if service.dependencies.Billing == nil {
		return service.loadSubmission(ctx, submissionID)
	}
	if validateBillingMeters(meters) != nil {
		return nil, transferReservationFailed()
	}
	forwardedBearer, ok := service.forwardedBearer(ctx)
	if !ok {
		return nil, rqerr.New(
			connect.CodeUnauthenticated,
			realqav1.ErrorReason_ERROR_REASON_REAUTHENTICATION_REQUIRED,
			realqav1.FailureClass_FAILURE_CLASS_REAUTHENTICATION_REQUIRED,
			0,
		)
	}
	var result *realqav1.Submission
	err := service.dependencies.Store.WithinTransaction(
		ctx, pgx.TxOptions{}, func(queries *dbgen.Queries) error {
			record, lockErr := queries.LockSubmissionRecord(
				ctx, toPGUUID(submissionID))
			if lockErr != nil {
				return lockErr
			}
			switch record.TransferState {
			case "reserved", "committed", "released":
				result, lockErr = loadSubmissionWithQueries(
					ctx, queries, submissionID)
				return lockErr
			case "pending":
			default:
				return transferReservationFailed()
			}
			organizationID, parseErr := fromPGUUID(
				record.PayerOrganizationID)
			if parseErr != nil {
				return transferReservationFailed()
			}
			teamID, parseErr := fromPGUUID(record.PayerTeamID)
			if parseErr != nil {
				return transferReservationFailed()
			}
			meterID, parseErr := fromPGUUID(record.TransferMeterID)
			if parseErr != nil || meterID != meters.Transfer.ID {
				return transferReservationFailed()
			}
			serviceID, parseErr := fromPGUUID(
				record.TransferServiceIdentityID)
			if parseErr != nil ||
				serviceID != meters.Transfer.ServiceIdentityID {
				return transferReservationFailed()
			}
			reserveKey, parseErr := fromPGUUID(
				record.TransferReserveIdempotencyKey)
			if parseErr != nil || record.TransferReservedUnits <= 0 {
				return transferReservationFailed()
			}
			reservation, reserveErr :=
				service.dependencies.Billing.ReserveTransfer(
					ctx, TransferReservationRequest{
						OrganizationID: organizationID,
						TeamID:         teamID,
						MeterID:        meterID,
						MaximumUnits:   record.TransferReservedUnits,
						ClientReference: "realqa-transfer:" +
							submissionID.String(),
						IdempotencyKey:  reserveKey,
						ForwardedBearer: forwardedBearer,
					})
			if reserveErr != nil {
				return transferReservationFailed()
			}
			if validateErr := validateTransferReservation(
				reservation,
				dbSubmissionBillingView{
					accountID:      actor.accountID,
					submissionID:   submissionID,
					organizationID: organizationID,
					teamID:         teamID,
					reservedUnits:  record.TransferReservedUnits,
				},
				meters,
				service.dependencies.Clock.Now().UTC(),
			); validateErr != nil {
				return validateErr
			}
			updated, updateErr := queries.SetTransferReservation(
				ctx, dbgen.SetTransferReservationParams{
					TransferReservationID:        toPGUUID(reservation.ID),
					TransferReservationCreatedAt: pgTimestamp(reservation.CreatedAt.UTC()),
					TransferReservationExpiresAt: pgTimestamp(reservation.ExpiresAt.UTC()),
					ID:                           toPGUUID(submissionID),
				})
			if updateErr != nil {
				return updateErr
			}
			if !updated.UploadDeadline.Time.After(
				service.dependencies.Clock.Now().UTC()) {
				return transferReservationFailed()
			}
			result, updateErr = loadSubmissionWithRecord(
				ctx, queries, updated)
			return updateErr
		})
	if err != nil {
		return nil, err
	}
	submissionScopeID, err := parseUUIDMessage(result.Owner.GetPersonalAccountId())
	if result.Owner.Kind ==
		realqav1.OwnerScopeKind_OWNER_SCOPE_KIND_ORGANIZATION {
		submissionScopeID, err = parseUUIDMessage(
			result.Owner.GetOrganizationId())
	}
	if err == nil {
		audit(ctx, service.dependencies, actor, "transfer_reserved",
			owner{kind: ownerKindName(result.Owner.Kind), id: submissionScopeID},
			submissionID, "allow", "success")
	}
	return result, nil
}

func (service *Submission) forwardedBearer(
	ctx context.Context,
) (string, bool) {
	if service.dependencies.ForwardedBearer == nil {
		return "", false
	}
	return service.dependencies.ForwardedBearer(ctx)
}

func ownerKindName(kind realqav1.OwnerScopeKind) string {
	if kind == realqav1.OwnerScopeKind_OWNER_SCOPE_KIND_PERSONAL {
		return "personal"
	}
	return "organization"
}
