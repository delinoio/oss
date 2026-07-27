package service

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	delibasev1 "github.com/delinoio/oss/protos/delibase/gen/go/delibase/v1"
	"github.com/delinoio/oss/servers/delibase/internal/database/dbgen"
	"github.com/delinoio/oss/servers/internal/auth"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestBackgroundPurposeRemainsClosedToRealQAStorage(t *testing.T) {
	t.Parallel()
	purpose, period, err := backgroundPurposeAndPeriod(
		delibasev1.BackgroundUsagePurpose_BACKGROUND_USAGE_PURPOSE_REALQA_STORAGE,
		delibasev1.BackgroundUsagePeriod_BACKGROUND_USAGE_PERIOD_UTC_DAY,
	)
	if err != nil || purpose != backgroundPurposeRealQAStorage ||
		period != backgroundPeriodUTCDay {
		t.Fatalf("background purpose/period = %q, %q, %v", purpose, period, err)
	}
	if _, _, err = backgroundPurposeAndPeriod(
		delibasev1.BackgroundUsagePurpose(2),
		delibasev1.BackgroundUsagePeriod_BACKGROUND_USAGE_PERIOD_UTC_DAY,
	); err == nil {
		t.Fatal("Deck-like unknown purpose was accepted")
	}
}

func TestAuthorizedUsagePeriodValidation(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	base := &delibasev1.AuthorizedUsageContext{
		AuthorizationId:   usageUUID(uuid.MustParse("019c0000-0000-7000-8000-000000000001")),
		Purpose:           delibasev1.BackgroundUsagePurpose_BACKGROUND_USAGE_PURPOSE_REALQA_STORAGE,
		FeatureResourceId: usageUUID(uuid.MustParse("019c0000-0000-7000-8000-000000000002")),
		Period:            delibasev1.BackgroundUsagePeriod_BACKGROUND_USAGE_PERIOD_UTC_DAY,
		PeriodStart:       timestamppb.New(currentUTCPeriodStart(now)),
	}
	if _, err := parseAuthorizedUsageBinding(base, now, true); err != nil {
		t.Fatal(err)
	}
	previous := proto.Clone(base).(*delibasev1.AuthorizedUsageContext)
	previous.PeriodStart = timestamppb.New(
		currentUTCPeriodStart(now).AddDate(0, 0, -1),
	)
	if _, err := parseAuthorizedUsageBinding(previous, now, true); err != nil {
		t.Fatal(err)
	}
	future := proto.Clone(base).(*delibasev1.AuthorizedUsageContext)
	future.PeriodStart = timestamppb.New(
		currentUTCPeriodStart(now).AddDate(0, 0, 1),
	)
	if _, err := parseAuthorizedUsageBinding(future, now, true); err == nil {
		t.Fatal("future reserve period was accepted")
	}
	noncanonical := proto.Clone(base).(*delibasev1.AuthorizedUsageContext)
	noncanonical.PeriodStart = timestamppb.New(now)
	if _, err := parseAuthorizedUsageBinding(
		noncanonical,
		now,
		false,
	); err == nil {
		t.Fatal("noncanonical UTC period was accepted")
	}
}

func TestAuthorizedUsageRequiresM2MWithoutForwardedUser(t *testing.T) {
	t.Parallel()
	serviceID := "realqa-service"
	m2mOnly := auth.WithPrincipal(context.Background(), auth.Principal{
		M2M: &auth.M2MClaims{
			TokenClaims: auth.TokenClaims{
				Subject:  serviceID,
				ClientID: serviceID,
				Type:     auth.TokenTypeM2M,
			},
			ServiceID: serviceID,
		},
	})
	if got, err := authorizedUsageServiceClientID(m2mOnly); err != nil ||
		got != serviceID {
		t.Fatalf("M2M-only principal = %q, %v", got, err)
	}
	withUser := auth.WithPrincipal(m2mOnly, auth.Principal{
		M2M: &auth.M2MClaims{
			TokenClaims: auth.TokenClaims{
				Subject:  serviceID,
				ClientID: serviceID,
				Type:     auth.TokenTypeM2M,
			},
			ServiceID: serviceID,
		},
		User: &auth.UserClaims{
			TokenClaims: auth.TokenClaims{
				Subject: "forwarded-user",
				Type:    auth.TokenTypeUser,
			},
			UserID: "forwarded-user",
		},
	})
	if _, err := authorizedUsageServiceClientID(withUser); err == nil {
		t.Fatal("authorized usage accepted a live forwarded user")
	}
}

func TestBackgroundPersistenceErrorsRetainStableReasons(t *testing.T) {
	t.Parallel()
	requireConnectReason(
		t,
		idempotencyInsertError(
			&pgconn.PgError{
				Code:           "23505",
				ConstraintName: "idempotency_records_caller_scope_key",
			},
			delibasev1.ErrorReason_ERROR_REASON_BACKGROUND_USAGE_REPLAY_CONFLICT,
		),
		connect.CodeAborted,
		delibasev1.ErrorReason_ERROR_REASON_BACKGROUND_USAGE_REPLAY_CONFLICT,
	)
	requireConnectReason(
		t,
		databaseError(&pgconn.PgError{
			Code:    "23503",
			Message: "background authorization connection is unavailable",
		}),
		connect.CodePermissionDenied,
		delibasev1.ErrorReason_ERROR_REASON_SERVICE_METER_NOT_ALLOWED,
	)
}

func TestUsageMessagesPreserveLiveUsageAndExposeAuthorizedBinding(t *testing.T) {
	t.Parallel()
	reservationID := uuid.MustParse("019c0000-0000-7000-8000-000000000011")
	authorizationID := uuid.MustParse("019c0000-0000-7000-8000-000000000012")
	resourceID := uuid.MustParse("019c0000-0000-7000-8000-000000000013")
	periodStart := time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC)
	row := dbgen.UsageReservation{
		ID: pgtype.UUID{
			Bytes: reservationID,
			Valid: true,
		},
	}
	if message := usageReservationMessage(row); message.AuthorizedUsage != nil {
		t.Fatalf("live reservation gained authorization = %#v", message)
	}
	row.BackgroundUsageAuthorizationID = pgtype.UUID{
		Bytes: authorizationID,
		Valid: true,
	}
	row.BackgroundUsagePurpose = pgtype.Text{
		String: backgroundPurposeRealQAStorage,
		Valid:  true,
	}
	row.BackgroundFeatureResourceID = pgtype.UUID{
		Bytes: resourceID,
		Valid: true,
	}
	row.BackgroundUsagePeriod = pgtype.Text{
		String: backgroundPeriodUTCDay,
		Valid:  true,
	}
	row.BackgroundPeriodStart = pgtype.Timestamptz{
		Time:  periodStart,
		Valid: true,
	}
	message := usageReservationMessage(row)
	if message.AuthorizedUsage.GetAuthorizationId().GetValue() !=
		authorizationID.String() ||
		message.AuthorizedUsage.GetFeatureResourceId().GetValue() !=
			resourceID.String() ||
		!message.AuthorizedUsage.GetPeriodStart().AsTime().Equal(periodStart) {
		t.Fatalf("authorized reservation message = %#v", message)
	}
}
