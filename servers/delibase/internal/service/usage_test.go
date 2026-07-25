package service

import (
	"bytes"
	"context"
	"math"
	"testing"

	"connectrpc.com/connect"
	delibasev1 "github.com/delinoio/oss/protos/delibase/gen/go/delibase/v1"
	"github.com/delinoio/oss/servers/delibase/internal/database/dbgen"
	"github.com/delinoio/oss/servers/internal/auth"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestUsageSubjectsRequireConsistentM2MAndForwardedUser(t *testing.T) {
	t.Parallel()
	_, _, err := usageSubjects(context.Background())
	requireConnectReason(
		t,
		err,
		connect.CodeUnauthenticated,
		delibasev1.ErrorReason_ERROR_REASON_AUTHENTICATION_REQUIRED,
	)
	invalid := auth.WithPrincipal(context.Background(), auth.Principal{
		User: &auth.UserClaims{
			TokenClaims: auth.TokenClaims{
				Subject: "user", Type: auth.TokenTypeUser,
			},
			UserID: "user",
		},
		M2M: &auth.M2MClaims{
			TokenClaims: auth.TokenClaims{
				Subject: "different", ClientID: "service",
				Type: auth.TokenTypeM2M,
			},
			ServiceID: "service",
		},
	})
	_, _, err = usageSubjects(invalid)
	requireConnectReason(
		t,
		err,
		connect.CodeUnauthenticated,
		delibasev1.ErrorReason_ERROR_REASON_AUTHENTICATION_INVALID,
	)
	serviceID, subject, err := usageSubjects(
		usageContext(context.Background(), "service", "user"),
	)
	if err != nil || serviceID != "service" || subject != "user" {
		t.Fatalf("subjects = %q, %q, %v", serviceID, subject, err)
	}
}

func TestUsageRequestDigestBindsForwardedSubject(t *testing.T) {
	t.Parallel()
	first := usageRequestDigest("user-a", "organization", "reservation")
	second := usageRequestDigest("user-b", "organization", "reservation")
	if bytes.Equal(first, second) {
		t.Fatal("usage request digest did not bind the forwarded subject")
	}
	if !bytes.Equal(
		first,
		usageRequestDigest("user-a", "organization", "reservation"),
	) {
		t.Fatal("usage request digest is not stable")
	}
}

func TestUsageIntegerArithmeticRejectsNegativeAndOverflow(t *testing.T) {
	t.Parallel()
	tests := []struct {
		left  int64
		right int64
		want  int64
		ok    bool
	}{
		{left: 0, right: math.MaxInt64, want: 0, ok: true},
		{left: math.MaxInt64, right: 1, want: math.MaxInt64, ok: true},
		{left: math.MaxInt64, right: 2, ok: false},
		{left: -1, right: 1, ok: false},
		{left: 1, right: -1, ok: false},
	}
	for _, test := range tests {
		got, ok := multiplyNonnegativeInt64(test.left, test.right)
		if got != test.want || ok != test.ok {
			t.Fatalf(
				"multiplyNonnegativeInt64(%d, %d) = %d, %t; want %d, %t",
				test.left,
				test.right,
				got,
				ok,
				test.want,
				test.ok,
			)
		}
	}
}

func TestDrainExpiredReservationPagesContinuesPastBatchBoundary(t *testing.T) {
	t.Parallel()
	pages := []int{int(usageExpirationBatchSize), 1}
	calls := 0
	total, err := drainExpiredReservationPages(func() (int, error) {
		count := pages[calls]
		calls++
		return count, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if total != int(usageExpirationBatchSize)+1 || calls != len(pages) {
		t.Fatalf("drained reservations = %d across %d pages", total, calls)
	}
}

func TestUsageCapacityErrorsHaveStableConnectDetails(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		capacity dbgen.GetUsageCapacityRow
		code     connect.Code
		reason   delibasev1.ErrorReason
	}{
		{
			name: "inactive",
			capacity: dbgen.GetUsageCapacityRow{
				SubscriptionStatus: "none",
			},
			code:   connect.CodeFailedPrecondition,
			reason: delibasev1.ErrorReason_ERROR_REASON_SUBSCRIPTION_INACTIVE,
		},
		{
			name: "past due",
			capacity: dbgen.GetUsageCapacityRow{
				SubscriptionStatus: "past_due",
			},
			code:   connect.CodeFailedPrecondition,
			reason: delibasev1.ErrorReason_ERROR_REASON_SUBSCRIPTION_PAST_DUE,
		},
		{
			name: "not configured",
			capacity: dbgen.GetUsageCapacityRow{
				SubscriptionStatus: "active",
				BillingPeriodID: pgtype.UUID{
					Bytes: [16]byte{1}, Valid: true,
				},
			},
			code:   connect.CodeFailedPrecondition,
			reason: delibasev1.ErrorReason_ERROR_REASON_OVERAGE_NOT_CONFIGURED,
		},
		{
			name: "disabled",
			capacity: dbgen.GetUsageCapacityRow{
				SubscriptionStatus:     "active",
				BillingPeriodID:        pgtype.UUID{Bytes: [16]byte{1}, Valid: true},
				OverageLimitConfigured: true,
			},
			code:   connect.CodeFailedPrecondition,
			reason: delibasev1.ErrorReason_ERROR_REASON_OVERAGE_DISABLED,
		},
		{
			name: "exhausted",
			capacity: dbgen.GetUsageCapacityRow{
				SubscriptionStatus:          "active",
				BillingPeriodID:             pgtype.UUID{Bytes: [16]byte{1}, Valid: true},
				OverageLimitConfigured:      true,
				RequestedOverageLimitMicros: 1,
			},
			code:   connect.CodeResourceExhausted,
			reason: delibasev1.ErrorReason_ERROR_REASON_OVERAGE_LIMIT_EXHAUSTED,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			requireConnectReason(
				t,
				usageCapacityError(test.capacity),
				test.code,
				test.reason,
			)
		})
	}
}
