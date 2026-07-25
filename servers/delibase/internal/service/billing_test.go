package service

import (
	"math"
	"strings"
	"testing"

	delibasev1 "github.com/delinoio/oss/protos/delibase/gen/go/delibase/v1"
	"github.com/delinoio/oss/servers/delibase/internal/reliability"
	"github.com/google/uuid"
)

func TestMemberBillingSummaryExposesOnlySharedAvailableCredit(t *testing.T) {
	t.Parallel()
	full := &delibasev1.BillingSummary{
		OrganizationId:         &delibasev1.UuidV7{Value: "organization"},
		SubscriptionStatus:     delibasev1.SubscriptionStatus_SUBSCRIPTION_STATUS_ACTIVE,
		AvailableCredit:        &delibasev1.UsdMicros{Value: cycleGrantMicros},
		HeldCredit:             &delibasev1.UsdMicros{Value: 1},
		CommittedOverage:       &delibasev1.UsdMicros{Value: 2},
		HeldOverage:            &delibasev1.UsdMicros{Value: 3},
		MonthlyOverageLimit:    &delibasev1.UsdMicros{Value: 4},
		OverageLimitConfigured: true,
		NewOverageAllowed:      true,
	}
	member := memberBillingSummary(full)
	if member.OrganizationId != full.OrganizationId ||
		member.AvailableCredit != full.AvailableCredit ||
		member.SubscriptionStatus !=
			delibasev1.SubscriptionStatus_SUBSCRIPTION_STATUS_UNSPECIFIED ||
		member.HeldCredit != nil || member.CommittedOverage != nil ||
		member.HeldOverage != nil || member.MonthlyOverageLimit != nil ||
		member.OverageLimitConfigured || member.NewOverageAllowed {
		t.Fatalf("member summary = %#v", member)
	}
}

func TestPolarSubscriptionStateMappingSupportsPastDueRecoveryAndTerminals(
	t *testing.T,
) {
	t.Parallel()
	tests := []struct {
		event  reliability.WebhookEventType
		status string
		want   string
	}{
		{reliability.WebhookOrderPaid, "", "active"},
		{reliability.WebhookSubscriptionPastDue, "active", "past_due"},
		{reliability.WebhookSubscriptionActive, "past_due", "active"},
		{reliability.WebhookSubscriptionUncanceled, "canceled", "active"},
		{reliability.WebhookSubscriptionUpdated, "active", "active"},
		{reliability.WebhookSubscriptionUpdated, "unpaid", "revoked"},
		{reliability.WebhookSubscriptionUpdated, "incomplete_expired", "revoked"},
		{reliability.WebhookSubscriptionCanceled, "active", "canceled"},
		{reliability.WebhookSubscriptionRevoked, "active", "revoked"},
	}
	for _, test := range tests {
		if got := polarSubscriptionStatus(test.event, test.status); got != test.want {
			t.Errorf("status(%q, %q) = %q, want %q",
				test.event, test.status, got, test.want)
		}
	}
}

func TestPolarBillingAuditEventTypeDistinguishesFinancialEvents(t *testing.T) {
	t.Parallel()
	tests := []struct {
		event reliability.WebhookEventType
		want  reliability.AuditEventType
	}{
		{reliability.WebhookOrderPaid, reliability.AuditSettlementRecorded},
		{reliability.WebhookRefundCreated, reliability.AuditRefundRecorded},
		{reliability.WebhookRefundUpdated, reliability.AuditRefundRecorded},
		{
			reliability.WebhookSubscriptionUpdated,
			reliability.AuditSubscriptionUpdated,
		},
	}
	for _, test := range tests {
		if got := polarBillingAuditEventType(test.event); got != test.want {
			t.Errorf("audit event(%q) = %q, want %q", test.event, got, test.want)
		}
	}
}

func TestProviderIdempotencyKeyPreservesLocalScope(t *testing.T) {
	t.Parallel()
	organizationID := uuid.MustParse("0198a000-0000-7000-8000-000000000001")
	base := providerIdempotencyKey(
		"create_subscription_checkout", "user-1", organizationID, "shared-key",
	)
	if len(base) != len("delibase:v1:")+64 ||
		!strings.HasPrefix(base, "delibase:v1:") {
		t.Fatalf("provider key = %q", base)
	}
	if base != providerIdempotencyKey(
		"create_subscription_checkout", "user-1", organizationID, "shared-key",
	) {
		t.Fatal("provider key is not deterministic")
	}
	for name, candidate := range map[string]string{
		"operation": providerIdempotencyKey(
			"create_billing_portal_session", "user-1", organizationID, "shared-key",
		),
		"subject": providerIdempotencyKey(
			"create_subscription_checkout", "user-2", organizationID, "shared-key",
		),
		"organization": providerIdempotencyKey(
			"create_subscription_checkout",
			"user-1",
			uuid.MustParse("0198a000-0000-7000-8000-000000000002"),
			"shared-key",
		),
		"caller key": providerIdempotencyKey(
			"create_subscription_checkout", "user-1", organizationID, "other-key",
		),
	} {
		if candidate == base {
			t.Errorf("%s did not change provider key", name)
		}
	}
}

func TestBillingInt64ArithmeticRejectsOverflow(t *testing.T) {
	t.Parallel()
	if _, ok := addInt64(math.MaxInt64, 1); ok {
		t.Fatal("positive overflow accepted")
	}
	if _, ok := addInt64(math.MinInt64, -1); ok {
		t.Fatal("negative overflow accepted")
	}
	if value, ok := addInt64(cycleGrantMicros, -cycleGrantMicros); !ok ||
		value != 0 {
		t.Fatalf("valid arithmetic = %d, %t", value, ok)
	}
}

func TestSettledCreditForfeiturePreservesNonCreditBalance(t *testing.T) {
	t.Parallel()
	amount, balanceAfter, ok := settledCreditForfeiture(
		cycleGrantMicros/2,
		cycleGrantMicros,
	)
	if !ok || amount != cycleGrantMicros ||
		balanceAfter != -cycleGrantMicros/2 {
		t.Fatalf(
			"forfeiture = amount %d, balance %d, valid %t",
			amount,
			balanceAfter,
			ok,
		)
	}
	if amount, balanceAfter, ok = settledCreditForfeiture(-5, 0); !ok ||
		amount != 0 || balanceAfter != -5 {
		t.Fatalf(
			"empty forfeiture = amount %d, balance %d, valid %t",
			amount,
			balanceAfter,
			ok,
		)
	}
	if _, _, ok = settledCreditForfeiture(math.MinInt64, 1); ok {
		t.Fatal("overflowing forfeiture accepted")
	}
}
