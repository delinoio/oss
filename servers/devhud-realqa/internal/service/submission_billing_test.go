package service

import (
	"bytes"
	"context"
	"errors"
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

func TestCeilMiBDaysAggregatesByteSecondsAndRoundsOnce(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		name        string
		byteSeconds int64
		units       int64
	}{
		{"zero", 0, 0},
		{"one byte second", 1, 1},
		{"half creation day", byteSecondsPerMiBDay / 2, 1},
		{"exact day", byteSecondsPerMiBDay, 1},
		{"one byte second over", byteSecondsPerMiBDay + 1, 2},
		{"two exact days", 2 * byteSecondsPerMiBDay, 2},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			units, err := ceilMiBDays(fixture.byteSeconds)
			if err != nil || units != fixture.units {
				t.Fatalf("ceilMiBDays(%d) = %d, %v; want %d",
					fixture.byteSeconds, units, err, fixture.units)
			}
		})
	}
	// Two half-MiB images retained for half a day are aggregated into one
	// half MiB-day checkpoint and rounded once for the authorization/day.
	halfImageHalfDay := (bytesPerMiB / 2) * (secondsPerUTCDay / 2)
	aggregate, err := ceilMiBDays(2 * halfImageHalfDay)
	if err != nil || aggregate != 1 {
		t.Fatalf("aggregate partial-day units = %d, %v", aggregate, err)
	}
	first, _ := ceilMiBDays(halfImageHalfDay)
	second, _ := ceilMiBDays(halfImageHalfDay)
	if first+second != 2 {
		t.Fatal("fixture did not distinguish per-image from daily rounding")
	}
	if _, err = ceilMiBDays(-1); err == nil {
		t.Fatal("negative retained byte-seconds were accepted")
	}
}

func TestUTCStorageBoundariesIgnoreCallerOffset(t *testing.T) {
	t.Parallel()
	west := time.FixedZone("west", -8*60*60)
	east := time.FixedZone("east", 14*60*60)
	for _, fixture := range []struct {
		value time.Time
		want  time.Time
	}{
		{
			time.Date(2030, 1, 1, 20, 30, 0, 0, west),
			time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC),
		},
		{
			time.Date(2030, 1, 2, 1, 30, 0, 0, east),
			time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC),
		},
	} {
		if got := utcDayStart(fixture.value); !got.Equal(fixture.want) ||
			got.Location() != time.UTC {
			t.Fatalf("utcDayStart(%s) = %s; want %s",
				fixture.value, got, fixture.want)
		}
	}
}

func TestStorageSettlementIdentityBindsAuthorizationDayAndDigest(t *testing.T) {
	t.Parallel()
	authorizationID := uuidv7.MustNew()
	binding := dbgen.RealqaStorageAuthorizationBinding{
		AuthorizationID:     toPGUUID(authorizationID),
		SubmissionID:        toPGUUID(uuidv7.MustNew()),
		ServiceIdentityID:   toPGUUID(uuidv7.MustNew()),
		AuthorizerAccountID: toPGUUID(uuidv7.MustNew()),
		OwnerID:             toPGUUID(uuidv7.MustNew()),
		OrganizationID:      toPGUUID(uuidv7.MustNew()),
		TeamID:              toPGUUID(uuidv7.MustNew()),
		MeterID:             toPGUUID(uuidv7.MustNew()),
	}
	period := time.Date(2030, 4, 5, 0, 0, 0, 0, time.UTC)
	digest, err := storageSettlementDigest(binding, period, 3)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := storageSettlementDigest(binding, period, 3)
	if err != nil || !bytes.Equal(digest, replayed) {
		t.Fatal("exact storage settlement digest did not replay")
	}
	changedUnits, _ := storageSettlementDigest(binding, period, 4)
	changedBinding := binding
	changedBinding.ServiceIdentityID = toPGUUID(uuidv7.MustNew())
	changedService, _ := storageSettlementDigest(changedBinding, period, 3)
	if bytes.Equal(digest, changedUnits) ||
		bytes.Equal(digest, changedService) {
		t.Fatal("storage settlement digest permitted substitution")
	}
	if _, err = storageSettlementDigest(
		binding, period.Add(time.Second), 3); err == nil {
		t.Fatal("non-midnight period was accepted")
	}
	reserve, commit, release, err := storageSettlementKeys(
		authorizationID, period)
	if err != nil {
		t.Fatal(err)
	}
	replayedReserve, _, _, _ := storageSettlementKeys(
		authorizationID, period)
	nextReserve, _, _, _ := storageSettlementKeys(
		authorizationID, period.Add(24*time.Hour))
	if reserve != replayedReserve || reserve == commit ||
		reserve == release || commit == release ||
		reserve == nextReserve ||
		reserve.Version() != 7 || commit.Version() != 7 ||
		release.Version() != 7 {
		t.Fatal("daily reserve/commit/release identities were not exact")
	}
}

func TestValidateAuthorizedStorageReservationRejectsDigestSubstitution(
	t *testing.T,
) {
	t.Parallel()
	period := time.Date(2030, 4, 5, 0, 0, 0, 0, time.UTC)
	authorizationID := uuidv7.MustNew()
	submissionID := uuidv7.MustNew()
	organizationID := uuidv7.MustNew()
	teamID := uuidv7.MustNew()
	serviceID := uuidv7.MustNew()
	meterID := uuidv7.MustNew()
	priceID := uuidv7.MustNew()
	accountID := uuidv7.MustNew()
	reservationID := uuidv7.MustNew()
	created := period.Add(time.Minute)
	expires := created.Add(time.Hour)
	binding := dbgen.RealqaStorageAuthorizationBinding{
		AuthorizationID:     toPGUUID(authorizationID),
		SubmissionID:        toPGUUID(submissionID),
		OrganizationID:      toPGUUID(organizationID),
		TeamID:              toPGUUID(teamID),
		ServiceIdentityID:   toPGUUID(serviceID),
		MeterID:             toPGUUID(meterID),
		AuthorizerAccountID: toPGUUID(accountID),
	}
	meter := BillingMeter{
		ID: meterID, PriceVersionID: priceID,
		ServiceIdentityID: serviceID,
	}
	settlement := dbgen.RealqaStorageDailySettlement{
		AuthorizationID: binding.AuthorizationID,
		PeriodStart:     pgTimestamp(period),
		Units:           2,
	}
	reservation := AuthorizedStorageReservation{
		TransferReservation: TransferReservation{
			ID: reservationID, OrganizationID: organizationID,
			TeamID: teamID, MeterID: meterID, PriceVersionID: priceID,
			UserAccountID: accountID, ServiceIdentityID: serviceID,
			MaximumUnits: 2, USDMicrosPerUnit: storagePriceUSDMicros,
			ClientReference: storageClientReference(
				authorizationID, period),
			Status: "active", CreatedAt: created, ExpiresAt: expires,
		},
		AuthorizationID:   authorizationID,
		FeatureResourceID: submissionID,
		PeriodStart:       period,
	}
	if err := validateAuthorizedStorageReservation(
		reservation, binding, meter, settlement, period,
		"active", 0); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*AuthorizedStorageReservation){
		"authorization": func(value *AuthorizedStorageReservation) {
			value.AuthorizationID = uuidv7.MustNew()
		},
		"resource": func(value *AuthorizedStorageReservation) {
			value.FeatureResourceID = uuidv7.MustNew()
		},
		"period": func(value *AuthorizedStorageReservation) {
			value.PeriodStart = value.PeriodStart.Add(24 * time.Hour)
		},
		"meter": func(value *AuthorizedStorageReservation) {
			value.MeterID = uuidv7.MustNew()
		},
		"price": func(value *AuthorizedStorageReservation) {
			value.USDMicrosPerUnit++
		},
		"units": func(value *AuthorizedStorageReservation) {
			value.MaximumUnits++
		},
		"reference": func(value *AuthorizedStorageReservation) {
			value.ClientReference += "-changed"
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := reservation
			mutate(&changed)
			if err := validateAuthorizedStorageReservation(
				changed, binding, meter, settlement, period,
				"active", 0); err == nil {
				t.Fatal("substituted authorized reservation was accepted")
			}
		})
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

func TestPersistedStorageReservationMeterAndRecoveryReasons(t *testing.T) {
	t.Parallel()
	meterID := uuidv7.MustNew()
	priceVersionID := uuidv7.MustNew()
	serviceIdentityID := uuidv7.MustNew()
	meter, err := persistedStorageReservationMeter(
		dbgen.RealqaStorageAuthorizationBinding{
			MeterID:           toPGUUID(meterID),
			ServiceIdentityID: toPGUUID(serviceIdentityID),
		},
		dbgen.RealqaStorageDailySettlement{
			ReservationPriceVersionID: toPGUUID(priceVersionID),
		},
	)
	if err != nil || meter.ID != meterID ||
		meter.PriceVersionID != priceVersionID ||
		meter.ServiceIdentityID != serviceIdentityID {
		t.Fatalf("persisted reservation meter = %#v, %v", meter, err)
	}
	if _, err = persistedStorageReservationMeter(
		dbgen.RealqaStorageAuthorizationBinding{
			MeterID:           toPGUUID(meterID),
			ServiceIdentityID: toPGUUID(serviceIdentityID),
		},
		dbgen.RealqaStorageDailySettlement{},
	); err == nil {
		t.Fatal("missing persisted reservation price version was accepted")
	}
	for _, reason := range []string{
		"billing_unavailable", "payment_required", "overage_required",
	} {
		if !storageCommitResolvesRecovery(reason) {
			t.Fatalf("successful commit did not resolve %q", reason)
		}
	}
	for _, reason := range []string{
		"authorization_revoked", "authorization_access_lost",
		"github_disconnected", "security_conflict",
	} {
		if storageCommitResolvesRecovery(reason) {
			t.Fatalf("successful commit incorrectly resolved %q", reason)
		}
	}
}

func TestStorageRebindSourceLookupKeepsOutagesRetryable(t *testing.T) {
	t.Parallel()
	authorization := StorageAuthorization{
		ID:                  uuidv7.MustNew(),
		AuthorizerAccountID: uuidv7.MustNew(),
		OwnerKind:           "organization",
		OwnerID:             uuidv7.MustNew(),
		OrganizationID:      uuidv7.MustNew(),
		TeamID:              uuidv7.MustNew(),
		ServiceIdentityID:   uuidv7.MustNew(),
		MeterID:             uuidv7.MustNew(),
		FeatureResourceID:   uuidv7.MustNew(),
		MaximumUnits:        1,
		Status:              "active",
		Revision:            1,
	}
	binding := dbgen.RealqaStorageAuthorizationBinding{
		AuthorizationID:       toPGUUID(authorization.ID),
		SubmissionID:          toPGUUID(authorization.FeatureResourceID),
		AuthorizerAccountID:   toPGUUID(authorization.AuthorizerAccountID),
		OwnerKind:             authorization.OwnerKind,
		OwnerID:               toPGUUID(authorization.OwnerID),
		OrganizationID:        toPGUUID(authorization.OrganizationID),
		TeamID:                toPGUUID(authorization.TeamID),
		ServiceIdentityID:     toPGUUID(authorization.ServiceIdentityID),
		MeterID:               toPGUUID(authorization.MeterID),
		MaximumUnits:          authorization.MaximumUnits,
		AuthorizationRevision: authorization.Revision,
	}
	requireServiceError(
		t,
		validateStorageRebindSource(
			StorageAuthorization{}, binding,
			errors.New("fixture delibase outage")),
		connect.CodeUnavailable,
		realqav1.ErrorReason_ERROR_REASON_STORAGE_AUTHORIZATION_FAILED,
		realqav1.FailureClass_FAILURE_CLASS_RETRYABLE,
	)
	substituted := authorization
	substituted.MeterID = uuidv7.MustNew()
	requireServiceError(
		t,
		validateStorageRebindSource(substituted, binding, nil),
		connect.CodeFailedPrecondition,
		realqav1.ErrorReason_ERROR_REASON_STORAGE_AUTHORIZATION_SUBSTITUTION,
		realqav1.FailureClass_FAILURE_CLASS_CONFLICT,
	)
	if err := validateStorageRebindSource(
		authorization, binding, nil); err != nil {
		t.Fatalf("exact rebind source rejected: %v", err)
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

func TestIssueBodyCleanupIsSubmissionBoundAndBestEffort(t *testing.T) {
	t.Parallel()
	updater := &recordingIssueUpdater{
		err: errors.New("provider unavailable"),
	}
	handler := NewSubmission(Dependencies{IssueUpdater: updater})
	handler.bestEffortIssueUpdate(
		context.Background(),
		dbgen.RealqaSubmission{
			ProviderIssueID: pgtype.Text{
				String: "issue-757", Valid: true,
			},
		},
		[]dbgen.RealqaAsset{
			{PublicID: pgtype.Text{String: "public-one", Valid: true}},
			{PublicID: pgtype.Text{String: "public-two", Valid: true}},
			{},
		},
	)
	if updater.issueID != "issue-757" ||
		len(updater.publicIDs) != 2 ||
		updater.publicIDs[0] != "public-one" ||
		updater.publicIDs[1] != "public-two" {
		t.Fatalf("best-effort update = %q, %#v",
			updater.issueID, updater.publicIDs)
	}
}

type recordingIssueUpdater struct {
	issueID   string
	publicIDs []string
	err       error
}

func (updater *recordingIssueUpdater) RemoveImageReferences(
	_ context.Context,
	issueID string,
	publicIDs []string,
) error {
	updater.issueID = issueID
	updater.publicIDs = append([]string(nil), publicIDs...)
	return updater.err
}
