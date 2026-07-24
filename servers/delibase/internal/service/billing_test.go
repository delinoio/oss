package service

import (
	"math"
	"testing"

	delibasev1 "github.com/delinoio/oss/protos/delibase/gen/go/delibase/v1"
	"github.com/delinoio/oss/servers/delibase/internal/reliability"
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
		{reliability.WebhookSubscriptionPastDue, "active", "past_due"},
		{reliability.WebhookSubscriptionActive, "past_due", "active"},
		{reliability.WebhookSubscriptionUpdated, "active", "active"},
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
