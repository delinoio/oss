package service

import (
	"bytes"
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	realqav1 "github.com/delinoio/oss/protos/devhud-realqa/gen/go/devhud-realqa/v1"
	"github.com/delinoio/oss/servers/devhud-realqa/internal/database/dbgen"
	"github.com/delinoio/oss/servers/internal/uuidv7"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestCeilMiBAggregatesAndRoundsOnce(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		bytes int64
		units int64
	}{
		{0, 0},
		{1, 1},
		{bytesPerMiB, 1},
		{bytesPerMiB + 1, 2},
		{2*bytesPerMiB - 1, 2},
	} {
		units, err := ceilMiB(fixture.bytes)
		if err != nil || units != fixture.units {
			t.Fatalf("ceilMiB(%d) = %d, %v; want %d",
				fixture.bytes, units, err, fixture.units)
		}
	}
	// Two half-MiB verified objects cost one unit only after aggregation.
	first, second := bytesPerMiB/2, bytesPerMiB/2
	aggregate, err := ceilMiB(first + second)
	if err != nil || aggregate != 1 {
		t.Fatalf("aggregate units = %d, %v", aggregate, err)
	}
	roundedSeparately, _ := ceilMiB(first)
	otherRoundedSeparately, _ := ceilMiB(second)
	if roundedSeparately+otherRoundedSeparately != 2 {
		t.Fatal("fixture did not distinguish aggregate rounding")
	}
}

func TestDerivedDownstreamKeysRemainStableDistinctUUIDv7(t *testing.T) {
	t.Parallel()
	root := uuidv7.MustNew()
	reserve, err := derivedUUIDv7(root, "transfer-reserve")
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := derivedUUIDv7(root, "transfer-reserve")
	if err != nil {
		t.Fatal(err)
	}
	commit, err := derivedUUIDv7(root, "transfer-commit")
	if err != nil {
		t.Fatal(err)
	}
	if reserve != replayed || reserve == commit ||
		reserve.Version() != 7 || commit.Version() != 7 ||
		!bytes.Equal(reserve[:6], root[:6]) {
		t.Fatalf("derived identities = %s, %s, %s",
			root, reserve, commit)
	}
}

func TestBillingMeterContractRejectsPriceTTLAndActivationDrift(t *testing.T) {
	t.Parallel()
	serviceID := uuidv7.MustNew()
	fixture := BillingMeters{
		Transfer: BillingMeter{
			ID: uuidv7.MustNew(), PriceVersionID: uuidv7.MustNew(),
			ServiceIdentityID: serviceID,
			Key:               "realqa_image_transfer", Unit: "encoded_mib",
			Precision: 0, USDMicrosPerUnit: transferPriceUSDMicros,
			ReservationTTLSeconds: 86_400, Enabled: true,
		},
		Storage: BillingMeter{
			ID: uuidv7.MustNew(), PriceVersionID: uuidv7.MustNew(),
			ServiceIdentityID: serviceID,
			Key:               "realqa_image_storage", Unit: "mib_day",
			Precision: 0, USDMicrosPerUnit: storagePriceUSDMicros,
			Enabled: true,
		},
	}
	if err := validateBillingMeters(fixture); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*BillingMeters){
		"transfer price": func(value *BillingMeters) {
			value.Transfer.USDMicrosPerUnit++
		},
		"storage price": func(value *BillingMeters) {
			value.Storage.USDMicrosPerUnit++
		},
		"transfer ttl": func(value *BillingMeters) {
			value.Transfer.ReservationTTLSeconds--
		},
		"disabled transfer": func(value *BillingMeters) {
			value.Transfer.Enabled = false
		},
		"price version": func(value *BillingMeters) {
			value.Transfer.PriceVersionID = uuidv7.MustNew()
			value.Transfer.PriceVersionID[6] = 0x40
		},
		"service substitution": func(value *BillingMeters) {
			value.Storage.ServiceIdentityID = uuidv7.MustNew()
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := fixture
			mutate(&changed)
			if err := validateBillingMeters(changed); err == nil {
				t.Fatal("divergent billing mapping was accepted")
			}
		})
	}
}

func TestSubmitIssueRequiresFreshPublicConfirmationBeforeDependencies(
	t *testing.T,
) {
	t.Parallel()
	handler := NewSubmission(Dependencies{})
	_, err := handler.SubmitIssue(
		context.Background(),
		connect.NewRequest(&realqav1.SubmitIssueRequest{
			Issue: &realqav1.IssueSubmission{
				PublicImageConfirmation: false,
			},
			ExpectedSubmissionRevision: &realqav1.Revision{Value: 1},
		}),
	)
	requireServiceError(
		t,
		err,
		connect.CodeFailedPrecondition,
		realqav1.ErrorReason_ERROR_REASON_PUBLIC_IMAGE_CONFIRMATION_REQUIRED,
		realqav1.FailureClass_FAILURE_CLASS_USER_ACTION_REQUIRED,
	)
}

func TestValidateTransferReservationRejectsSubstitutionAndShortTTL(
	t *testing.T,
) {
	t.Parallel()
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	organizationID := uuidv7.MustNew()
	teamID := uuidv7.MustNew()
	serviceID := uuidv7.MustNew()
	meterID := uuidv7.MustNew()
	priceVersionID := uuidv7.MustNew()
	accountID := uuidv7.MustNew()
	submissionID := uuidv7.MustNew()
	meters := BillingMeters{Transfer: BillingMeter{
		ID: meterID, PriceVersionID: priceVersionID,
		ServiceIdentityID: serviceID,
	}}
	reservation := TransferReservation{
		ID: uuidv7.MustNew(), OrganizationID: organizationID,
		TeamID: teamID, MeterID: meterID, PriceVersionID: priceVersionID,
		UserAccountID: accountID, ServiceIdentityID: serviceID,
		MaximumUnits: 2, USDMicrosPerUnit: transferPriceUSDMicros,
		ClientReference: "realqa-transfer:" + submissionID.String(),
		Status:          "active", CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour),
	}
	view := dbSubmissionBillingView{
		accountID: accountID, submissionID: submissionID,
		organizationID: organizationID, teamID: teamID, reservedUnits: 2,
	}
	if err := validateTransferReservation(
		reservation, view, meters, now); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*TransferReservation){
		"reservation": func(value *TransferReservation) {
			value.ID = uuidv7.MustNew()
			value.ID[6] = 0x40
		},
		"organization": func(value *TransferReservation) {
			value.OrganizationID = uuidv7.MustNew()
		},
		"team": func(value *TransferReservation) {
			value.TeamID = uuidv7.MustNew()
		},
		"meter": func(value *TransferReservation) {
			value.MeterID = uuidv7.MustNew()
		},
		"price version": func(value *TransferReservation) {
			value.PriceVersionID = uuidv7.MustNew()
		},
		"user": func(value *TransferReservation) {
			value.UserAccountID = uuidv7.MustNew()
		},
		"service": func(value *TransferReservation) {
			value.ServiceIdentityID = uuidv7.MustNew()
		},
		"maximum": func(value *TransferReservation) {
			value.MaximumUnits++
		},
		"already committed": func(value *TransferReservation) {
			value.CommittedUnits = 1
		},
		"price": func(value *TransferReservation) {
			value.USDMicrosPerUnit++
		},
		"reference": func(value *TransferReservation) {
			value.ClientReference += "-substituted"
		},
		"status": func(value *TransferReservation) {
			value.Status = "released"
		},
		"short ttl": func(value *TransferReservation) {
			value.ExpiresAt = now.Add(24*time.Hour - time.Second)
		},
		"expired": func(value *TransferReservation) {
			value.CreatedAt = now.Add(-25 * time.Hour)
			value.ExpiresAt = now.Add(-time.Hour)
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := reservation
			mutate(&changed)
			if err := validateTransferReservation(
				changed, view, meters, now); err == nil {
				t.Fatal("substituted reservation was accepted")
			}
		})
	}
}

func TestValidateFinalTransferRequiresExactPersistedReservation(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2030, 1, 1, 0, 0, 0, 123000, time.UTC)
	expiresAt := createdAt.Add(24 * time.Hour)
	organizationID := uuidv7.MustNew()
	teamID := uuidv7.MustNew()
	serviceID := uuidv7.MustNew()
	meterID := uuidv7.MustNew()
	priceVersionID := uuidv7.MustNew()
	accountID := uuidv7.MustNew()
	submissionID := uuidv7.MustNew()
	reservationID := uuidv7.MustNew()
	record := dbgen.RealqaSubmission{
		ID:                           toPGUUID(submissionID),
		CreatedByAccountID:           toPGUUID(accountID),
		PayerOrganizationID:          toPGUUID(organizationID),
		PayerTeamID:                  toPGUUID(teamID),
		TransferReservationID:        toPGUUID(reservationID),
		TransferReservedUnits:        3,
		TransferReservationCreatedAt: pgtype.Timestamptz{Time: createdAt, Valid: true},
		TransferReservationExpiresAt: pgtype.Timestamptz{Time: expiresAt, Valid: true},
	}
	meters := BillingMeters{Transfer: BillingMeter{
		ID: meterID, PriceVersionID: priceVersionID,
		ServiceIdentityID: serviceID,
	}}
	finalized := TransferReservation{
		ID: reservationID, OrganizationID: organizationID, TeamID: teamID,
		MeterID: meterID, PriceVersionID: priceVersionID,
		UserAccountID: accountID, ServiceIdentityID: serviceID,
		MaximumUnits: 3, CommittedUnits: 2,
		USDMicrosPerUnit: transferPriceUSDMicros,
		ClientReference:  "realqa-transfer:" + submissionID.String(),
		Status:           "committed",
		CreatedAt:        createdAt,
		ExpiresAt:        expiresAt,
	}
	if err := validateFinalTransfer(
		finalized, record, meters, "committed", 2); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*TransferReservation){
		"reservation": func(value *TransferReservation) {
			value.ID = uuidv7.MustNew()
		},
		"organization": func(value *TransferReservation) {
			value.OrganizationID = uuidv7.MustNew()
		},
		"team": func(value *TransferReservation) {
			value.TeamID = uuidv7.MustNew()
		},
		"meter": func(value *TransferReservation) {
			value.MeterID = uuidv7.MustNew()
		},
		"price version": func(value *TransferReservation) {
			value.PriceVersionID = uuidv7.MustNew()
		},
		"user": func(value *TransferReservation) {
			value.UserAccountID = uuidv7.MustNew()
		},
		"service": func(value *TransferReservation) {
			value.ServiceIdentityID = uuidv7.MustNew()
		},
		"maximum": func(value *TransferReservation) {
			value.MaximumUnits++
		},
		"committed": func(value *TransferReservation) {
			value.CommittedUnits++
		},
		"price": func(value *TransferReservation) {
			value.USDMicrosPerUnit++
		},
		"reference": func(value *TransferReservation) {
			value.ClientReference += "-substituted"
		},
		"status": func(value *TransferReservation) {
			value.Status = "released"
		},
		"created at": func(value *TransferReservation) {
			value.CreatedAt = value.CreatedAt.Add(time.Second)
		},
		"expires at": func(value *TransferReservation) {
			value.ExpiresAt = value.ExpiresAt.Add(time.Second)
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := finalized
			mutate(&changed)
			if err := validateFinalTransfer(
				changed, record, meters, "committed", 2); err == nil {
				t.Fatal("substituted finalized reservation was accepted")
			}
		})
	}
}

func TestValidateStorageAuthorizationRejectsEveryBindingSubstitution(
	t *testing.T,
) {
	t.Parallel()
	actorID := uuidv7.MustNew()
	request := StorageAuthorizationRequest{
		OwnerKind: "personal", OwnerID: actorID,
		OrganizationID: uuidv7.MustNew(), TeamID: uuidv7.MustNew(),
		ServiceIdentityID: uuidv7.MustNew(), MeterID: uuidv7.MustNew(),
		FeatureResourceID: uuidv7.MustNew(), MaximumUnits: 2,
		IdempotencyKey: uuidv7.MustNew(), ForwardedBearer: "memory-only",
	}
	authorization := StorageAuthorization{
		ID: uuidv7.MustNew(), AuthorizerAccountID: actorID,
		OwnerKind: request.OwnerKind, OwnerID: request.OwnerID,
		OrganizationID: request.OrganizationID, TeamID: request.TeamID,
		ServiceIdentityID: request.ServiceIdentityID,
		MeterID:           request.MeterID,
		FeatureResourceID: request.FeatureResourceID,
		MaximumUnits:      request.MaximumUnits,
		Status:            "active",
		Revision:          1,
	}
	if err := validateStorageAuthorization(
		authorization, request, actorID); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*StorageAuthorization){
		"authorization": func(value *StorageAuthorization) {
			value.ID = uuidv7.MustNew()
			value.ID[6] = 0x40
		},
		"authorizer": func(value *StorageAuthorization) {
			value.AuthorizerAccountID = uuidv7.MustNew()
		},
		"owner kind": func(value *StorageAuthorization) {
			value.OwnerKind = "organization"
		},
		"owner": func(value *StorageAuthorization) {
			value.OwnerID = uuidv7.MustNew()
		},
		"organization": func(value *StorageAuthorization) {
			value.OrganizationID = uuidv7.MustNew()
		},
		"team": func(value *StorageAuthorization) {
			value.TeamID = uuidv7.MustNew()
		},
		"service": func(value *StorageAuthorization) {
			value.ServiceIdentityID = uuidv7.MustNew()
		},
		"meter": func(value *StorageAuthorization) {
			value.MeterID = uuidv7.MustNew()
		},
		"resource": func(value *StorageAuthorization) {
			value.FeatureResourceID = uuidv7.MustNew()
		},
		"maximum": func(value *StorageAuthorization) {
			value.MaximumUnits++
		},
		"status": func(value *StorageAuthorization) {
			value.Status = "revoked"
		},
		"revision": func(value *StorageAuthorization) {
			value.Revision = 0
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := authorization
			mutate(&changed)
			if err := validateStorageAuthorization(
				changed, request, actorID); err == nil {
				t.Fatal("substituted storage authorization was accepted")
			}
		})
	}
}
