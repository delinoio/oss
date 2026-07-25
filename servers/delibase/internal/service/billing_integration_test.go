package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	delibasev1 "github.com/delinoio/oss/protos/delibase/gen/go/delibase/v1"
	"github.com/delinoio/oss/servers/delibase/internal/contracts"
	"github.com/delinoio/oss/servers/delibase/internal/database"
	"github.com/delinoio/oss/servers/delibase/internal/database/dbgen"
	"github.com/delinoio/oss/servers/delibase/internal/reliability"
	"github.com/delinoio/oss/servers/internal/safelog"
	"github.com/delinoio/oss/servers/internal/uuidv7"
	"github.com/jackc/pgx/v5"
)

func TestPostgreSQLPolarPaidCycleAndRefundEffectsAreExactOnce(t *testing.T) {
	databaseURL := os.Getenv("DELIBASE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DELIBASE_TEST_DATABASE_URL is not set; run scripts/test-postgres.sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	accountID := uuidv7.MustNew()
	organizationID := uuidv7.MustNew()
	teamID := uuidv7.MustNew()
	err = store.WithinTransaction(ctx, pgx.TxOptions{}, func(queries *dbgen.Queries) error {
		if _, err := queries.CreateAccount(ctx, dbgen.CreateAccountParams{
			ID: pgUUID(accountID), LogtoSubject: "billing-" + accountID.String(),
			DisplayName: "Billing Owner",
		}); err != nil {
			return err
		}
		if _, err := queries.CreateOrganization(ctx, dbgen.CreateOrganizationParams{
			ID: pgUUID(organizationID), Name: "Billing Integration",
			Slug: "billing-" + organizationID.String()[24:],
		}); err != nil {
			return err
		}
		if _, err := queries.CreatePolarCustomer(ctx, dbgen.CreatePolarCustomerParams{
			OrganizationID:  pgUUID(organizationID),
			PolarCustomerID: "customer_" + organizationID.String(),
		}); err != nil {
			return err
		}
		if _, err := queries.CreateOrganizationMembership(
			ctx, dbgen.CreateOrganizationMembershipParams{
				OrganizationID: pgUUID(organizationID),
				AccountID:      pgUUID(accountID), Role: "owner",
			},
		); err != nil {
			return err
		}
		if _, err := queries.CreateGeneralTeam(ctx, dbgen.CreateGeneralTeamParams{
			ID: pgUUID(teamID), OrganizationID: pgUUID(organizationID),
		}); err != nil {
			return err
		}
		_, err := queries.CreateTeamMembership(
			ctx, dbgen.CreateTeamMembershipParams{
				OrganizationID: pgUUID(organizationID), TeamID: pgUUID(teamID),
				AccountID: pgUUID(accountID), Role: "admin",
			},
		)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	const productID = "product_monthly_10_usd"
	if err := store.SyncPolarCatalog(ctx, productID, "production"); err != nil {
		t.Fatal(err)
	}
	if err := store.SyncPolarCatalog(ctx, productID, "sandbox"); err == nil {
		t.Fatal("Polar catalog accepted a provider environment change")
	}
	ids := defaultIDGenerator{}
	handler := NewPolarWebhookProcessor(store, ids)
	now := time.Now().UTC().Truncate(time.Second)
	previousPeriodStart := now.Add(-62 * 24 * time.Hour)
	previousPeriodEnd := now.Add(-31 * 24 * time.Hour)
	currentPeriodStart := now.Add(-time.Hour)
	currentPeriodEnd := currentPeriodStart.Add(31 * 24 * time.Hour)
	canceledPayload, _ := json.Marshal(polarBillingEvent{
		Type:       string(reliability.WebhookSubscriptionCanceled),
		EventAt:    now.Add(-3 * time.Hour),
		ObjectID:   "subscription_1",
		CustomerID: "customer_" + organizationID.String(),
		ExternalID: organizationID.String(), SubscriptionID: "subscription_1",
		ProductID: productID, CurrentPeriodStart: currentPeriodStart,
		CurrentPeriodEnd: currentPeriodEnd,
	})
	if err := handler(ctx, reliability.Item{
		ID:        uuidv7.MustNew(),
		HandlerID: reliability.HandlerPolarSubscriptionCanceled,
		Payload:   canceledPayload,
	}); err != nil {
		t.Fatal(err)
	}
	paidPayload, _ := json.Marshal(polarBillingEvent{
		Type: string(reliability.WebhookOrderPaid), EventAt: now.Add(-4 * time.Hour),
		ObjectID: "order_1", OrderID: "order_1",
		CustomerID: "customer_" + organizationID.String(),
		ExternalID: organizationID.String(), SubscriptionID: "subscription_1",
		ProductID: productID, Currency: "usd",
		BillingReason: "subscription_cycle", Paid: true,
		CurrentPeriodStart: previousPeriodStart, CurrentPeriodEnd: previousPeriodEnd,
	})
	paidItem := reliability.Item{
		ID: uuidv7.MustNew(), HandlerID: reliability.HandlerPolarOrderPaid,
		Payload: paidPayload,
	}
	if err := handler(ctx, paidItem); err != nil {
		t.Fatal(err)
	}
	if err := handler(ctx, paidItem); err != nil {
		t.Fatal(err)
	}
	uncanceledPayload, _ := json.Marshal(polarBillingEvent{
		Type:       string(reliability.WebhookSubscriptionUpdated),
		EventAt:    now.Add(-2 * time.Hour),
		ObjectID:   "subscription_1",
		CustomerID: "customer_" + organizationID.String(),
		ExternalID: organizationID.String(), SubscriptionID: "subscription_1",
		ProductID: productID, Status: "active",
		CurrentPeriodStart: currentPeriodStart, CurrentPeriodEnd: currentPeriodEnd,
	})
	if err := handler(ctx, reliability.Item{
		ID:        uuidv7.MustNew(),
		HandlerID: reliability.HandlerPolarSubscriptionUpdated,
		Payload:   uncanceledPayload,
	}); err != nil {
		t.Fatal(err)
	}
	subscription, err := store.Queries().GetSubscriptionByPolarID(
		ctx, "subscription_1",
	)
	if err != nil || subscription.Status != "active" {
		t.Fatalf("uncanceled subscription = %#v, %v", subscription, err)
	}
	currentPaidPayload, _ := json.Marshal(polarBillingEvent{
		Type: string(reliability.WebhookOrderPaid), EventAt: now.Add(-time.Hour),
		ObjectID: "order_2", OrderID: "order_2",
		CustomerID: "customer_" + organizationID.String(),
		ExternalID: organizationID.String(), SubscriptionID: "subscription_1",
		ProductID: productID, Currency: "usd",
		BillingReason: "subscription_cycle", Paid: true,
		CurrentPeriodStart: currentPeriodStart, CurrentPeriodEnd: currentPeriodEnd,
	})
	if err := handler(ctx, reliability.Item{
		ID:        uuidv7.MustNew(),
		HandlerID: reliability.HandlerPolarOrderPaid,
		Payload:   currentPaidPayload,
	}); err != nil {
		t.Fatal(err)
	}
	otherOrganizationID := uuidv7.MustNew()
	otherTeamID := uuidv7.MustNew()
	err = store.WithinTransaction(ctx, pgx.TxOptions{}, func(queries *dbgen.Queries) error {
		if _, transactionErr := queries.CreateOrganization(
			ctx,
			dbgen.CreateOrganizationParams{
				ID:   pgUUID(otherOrganizationID),
				Name: "Other Billing Integration",
				Slug: "other-billing-" + otherOrganizationID.String()[24:],
			},
		); transactionErr != nil {
			return transactionErr
		}
		if _, transactionErr := queries.CreatePolarCustomer(
			ctx,
			dbgen.CreatePolarCustomerParams{
				OrganizationID:  pgUUID(otherOrganizationID),
				PolarCustomerID: "customer_" + otherOrganizationID.String(),
			},
		); transactionErr != nil {
			return transactionErr
		}
		if _, transactionErr := queries.CreateOrganizationMembership(
			ctx,
			dbgen.CreateOrganizationMembershipParams{
				OrganizationID: pgUUID(otherOrganizationID),
				AccountID:      pgUUID(accountID),
				Role:           "owner",
			},
		); transactionErr != nil {
			return transactionErr
		}
		if _, transactionErr := queries.CreateGeneralTeam(
			ctx,
			dbgen.CreateGeneralTeamParams{
				ID:             pgUUID(otherTeamID),
				OrganizationID: pgUUID(otherOrganizationID),
			},
		); transactionErr != nil {
			return transactionErr
		}
		_, transactionErr := queries.CreateTeamMembership(
			ctx,
			dbgen.CreateTeamMembershipParams{
				OrganizationID: pgUUID(otherOrganizationID),
				TeamID:         pgUUID(otherTeamID),
				AccountID:      pgUUID(accountID),
				Role:           "admin",
			},
		)
		return transactionErr
	})
	if err != nil {
		t.Fatal(err)
	}
	mismatchedCustomerPayload, _ := json.Marshal(polarBillingEvent{
		Type:               string(reliability.WebhookOrderPaid),
		EventAt:            now,
		ObjectID:           "order_mismatched_customer",
		OrderID:            "order_mismatched_customer",
		CustomerID:         "customer_" + otherOrganizationID.String(),
		ExternalID:         otherOrganizationID.String(),
		SubscriptionID:     "subscription_1",
		ProductID:          productID,
		Currency:           "usd",
		BillingReason:      "subscription_cycle",
		Paid:               true,
		CurrentPeriodStart: currentPeriodStart,
		CurrentPeriodEnd:   currentPeriodEnd,
	})
	if err := handler(ctx, reliability.Item{
		ID:        uuidv7.MustNew(),
		HandlerID: reliability.HandlerPolarOrderPaid,
		Payload:   mismatchedCustomerPayload,
	}); !errors.Is(err, reliability.ErrInvalidInput) {
		t.Fatalf("mismatched subscription customer error = %v", err)
	}
	invalidProductPayload, _ := json.Marshal(polarBillingEvent{
		Type:               string(reliability.WebhookSubscriptionActive),
		EventAt:            now,
		ObjectID:           "subscription_wrong_product",
		CustomerID:         "customer_" + organizationID.String(),
		ExternalID:         organizationID.String(),
		SubscriptionID:     "subscription_wrong_product",
		ProductID:          "product_other",
		CurrentPeriodStart: currentPeriodStart,
		CurrentPeriodEnd:   currentPeriodEnd,
	})
	if err := handler(ctx, reliability.Item{
		ID:        uuidv7.MustNew(),
		HandlerID: reliability.HandlerPolarSubscriptionActive,
		Payload:   invalidProductPayload,
	}); !errors.Is(err, reliability.ErrInvalidInput) {
		t.Fatalf("non-catalog subscription error = %v", err)
	}
	summary, err := store.Queries().GetBillingSummary(ctx, pgUUID(organizationID))
	if err != nil || summary.AvailableCreditMicros != 2*cycleGrantMicros {
		t.Fatalf("paid summary = %#v, %v", summary, err)
	}
	err = store.WithinTransaction(
		ctx, pgx.TxOptions{}, func(queries *dbgen.Queries) error {
			ledgerID, transactionErr := ids.New()
			if transactionErr != nil {
				return transactionErr
			}
			_, transactionErr = queries.InsertBillingLedgerEntry(
				ctx,
				dbgen.InsertBillingLedgerEntryParams{
					ID:                 pgUUID(ledgerID),
					OrganizationID:     pgUUID(organizationID),
					EntryType:          "credit_forfeiture",
					AmountMicros:       -2 * cycleGrantMicros,
					BalanceAfterMicros: 0,
					SourceReference:    "test-consumed-rollover-credit",
				},
			)
			return transactionErr
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	refundPayload, _ := json.Marshal(polarBillingEvent{
		Type: string(reliability.WebhookRefundUpdated), EventAt: now,
		ObjectID: "refund_1", OrderID: "order_1", Status: "succeeded",
		Currency: "usd", AmountMicros: cycleGrantMicros / 2,
	})
	refundItem := reliability.Item{
		ID: uuidv7.MustNew(), HandlerID: reliability.HandlerPolarRefundUpdated,
		Payload: refundPayload,
	}
	if err := handler(ctx, refundItem); err != nil {
		t.Fatal(err)
	}
	if err := handler(ctx, refundItem); err != nil {
		t.Fatal(err)
	}
	summary, err = store.Queries().GetBillingSummary(ctx, pgUUID(organizationID))
	currentCycle, currentCycleErr := store.Queries().GetPolarPaidCycle(ctx, "order_2")
	if err != nil || currentCycleErr != nil || summary.AvailableCreditMicros != 0 ||
		summary.CommittedOverageMicros != cycleGrantMicros/2 ||
		summary.BillingPeriodID != currentCycle.BillingPeriodID {
		t.Fatalf("refund summary = %#v, %v", summary, err)
	}
	if _, err = store.Queries().UpdateOrganizationOverageLimit(
		ctx,
		dbgen.UpdateOrganizationOverageLimitParams{
			OverageLimitMicros: cycleGrantMicros / 2,
			OrganizationID:     pgUUID(organizationID),
		},
	); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Queries().UpdateCurrentBillingPeriodOverageLimit(
		ctx,
		dbgen.UpdateCurrentBillingPeriodOverageLimitParams{
			OverageLimitMicros: cycleGrantMicros / 2,
			OrganizationID:     pgUUID(organizationID),
		},
	); err != nil {
		t.Fatal(err)
	}
	summary, err = store.Queries().GetBillingSummary(ctx, pgUUID(organizationID))
	if err != nil || summary.NewOverageAllowed {
		t.Fatalf("exhausted overage summary = %#v, %v", summary, err)
	}
	cycle, err := store.Queries().GetPolarPaidCycle(ctx, "order_1")
	if err != nil || cycle.GrantMicros != cycleGrantMicros ||
		cycle.ReversedMicros != cycleGrantMicros/2 {
		t.Fatalf("paid cycle = %#v, %v", cycle, err)
	}
	pastDuePayload, _ := json.Marshal(polarBillingEvent{
		Type:       string(reliability.WebhookSubscriptionPastDue),
		EventAt:    now.Add(2 * time.Minute),
		ObjectID:   "subscription_1",
		CustomerID: "customer_" + organizationID.String(),
		ExternalID: organizationID.String(), SubscriptionID: "subscription_1",
		ProductID: productID, CurrentPeriodStart: currentPeriodStart,
		CurrentPeriodEnd: currentPeriodEnd,
	})
	if err := handler(ctx, reliability.Item{
		ID:        uuidv7.MustNew(),
		HandlerID: reliability.HandlerPolarSubscriptionPastDue,
		Payload:   pastDuePayload,
	}); err != nil {
		t.Fatal(err)
	}
	replacementPeriodStart := now.Add(-30 * time.Minute)
	replacementPeriodEnd := replacementPeriodStart.Add(31 * 24 * time.Hour)
	replacementPayload, _ := json.Marshal(polarBillingEvent{
		Type: string(reliability.WebhookOrderPaid), EventAt: now.Add(3 * time.Minute),
		ObjectID: "order_replacement", OrderID: "order_replacement",
		CustomerID: "customer_" + organizationID.String(),
		ExternalID: organizationID.String(), SubscriptionID: "subscription_2",
		ProductID: productID, Currency: "usd",
		BillingReason: "subscription_create", Paid: true,
		CurrentPeriodStart: replacementPeriodStart,
		CurrentPeriodEnd:   replacementPeriodEnd,
	})
	if err := handler(ctx, reliability.Item{
		ID:        uuidv7.MustNew(),
		HandlerID: reliability.HandlerPolarOrderPaid,
		Payload:   replacementPayload,
	}); err != nil {
		t.Fatal(err)
	}
	replacementCycle, replacementCycleErr := store.Queries().GetPolarPaidCycle(
		ctx, "order_replacement",
	)
	activePeriod, activePeriodErr := store.Queries().GetCurrentActiveBillingPeriod(
		ctx, pgUUID(organizationID),
	)
	if replacementCycleErr != nil || activePeriodErr != nil ||
		replacementCycle.BillingPeriodID != activePeriod.ID ||
		!activePeriod.StartsAt.Time.Equal(replacementPeriodStart) ||
		!activePeriod.EndsAt.Time.Equal(replacementPeriodEnd) {
		t.Fatalf(
			"replacement cycle = %#v, %v; active period = %#v, %v",
			replacementCycle,
			replacementCycleErr,
			activePeriod,
			activePeriodErr,
		)
	}
	if _, err = store.Queries().MarkOrganizationDeleted(
		ctx, pgUUID(organizationID),
	); err != nil {
		t.Fatal(err)
	}
	deletedOrganizationRefundPayload, _ := json.Marshal(polarBillingEvent{
		Type: string(reliability.WebhookRefundUpdated), EventAt: now.Add(time.Minute),
		ObjectID: "refund_deleted_organization", OrderID: "order_2",
		Status: "succeeded", Currency: "usd", AmountMicros: cycleGrantMicros / 2,
	})
	if err := handler(ctx, reliability.Item{
		ID:        uuidv7.MustNew(),
		HandlerID: reliability.HandlerPolarRefundUpdated,
		Payload:   deletedOrganizationRefundPayload,
	}); err != nil {
		t.Fatal(err)
	}
	deletedOrganizationCycle, err := store.Queries().GetPolarPaidCycle(ctx, "order_2")
	if err != nil || deletedOrganizationCycle.ReversedMicros != cycleGrantMicros/2 {
		t.Fatalf(
			"deleted organization paid cycle = %#v, %v",
			deletedOrganizationCycle,
			err,
		)
	}
}

func TestPostgreSQLSubscriptionCheckoutSerializesDistinctKeys(t *testing.T) {
	databaseURL := os.Getenv("DELIBASE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DELIBASE_TEST_DATABASE_URL is not set; run scripts/test-postgres.sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	accountID := uuidv7.MustNew()
	organizationID := uuidv7.MustNew()
	teamID := uuidv7.MustNew()
	subject := "checkout-" + accountID.String()
	err = store.WithinTransaction(ctx, pgx.TxOptions{}, func(queries *dbgen.Queries) error {
		if _, transactionErr := queries.CreateAccount(
			ctx,
			dbgen.CreateAccountParams{
				ID:           pgUUID(accountID),
				LogtoSubject: subject,
				DisplayName:  "Checkout Owner",
			},
		); transactionErr != nil {
			return transactionErr
		}
		if _, transactionErr := queries.CreateOrganization(
			ctx,
			dbgen.CreateOrganizationParams{
				ID:   pgUUID(organizationID),
				Name: "Checkout Integration",
				Slug: "checkout-" + organizationID.String()[24:],
			},
		); transactionErr != nil {
			return transactionErr
		}
		if _, transactionErr := queries.CreatePolarCustomer(
			ctx,
			dbgen.CreatePolarCustomerParams{
				OrganizationID:  pgUUID(organizationID),
				PolarCustomerID: "customer_" + organizationID.String(),
			},
		); transactionErr != nil {
			return transactionErr
		}
		if _, transactionErr := queries.CreateOrganizationMembership(
			ctx,
			dbgen.CreateOrganizationMembershipParams{
				OrganizationID: pgUUID(organizationID),
				AccountID:      pgUUID(accountID),
				Role:           "owner",
			},
		); transactionErr != nil {
			return transactionErr
		}
		if _, transactionErr := queries.CreateGeneralTeam(
			ctx,
			dbgen.CreateGeneralTeamParams{
				ID:             pgUUID(teamID),
				OrganizationID: pgUUID(organizationID),
			},
		); transactionErr != nil {
			return transactionErr
		}
		_, transactionErr := queries.CreateTeamMembership(
			ctx,
			dbgen.CreateTeamMembershipParams{
				OrganizationID: pgUUID(organizationID),
				TeamID:         pgUUID(teamID),
				AccountID:      pgUUID(accountID),
				Role:           "admin",
			},
		)
		return transactionErr
	})
	if err != nil {
		t.Fatal(err)
	}
	pseudonymizer, err := safelog.NewPseudonymizer(bytes.Repeat([]byte{0x25}, 32))
	if err != nil {
		t.Fatal(err)
	}
	provider := newBlockingCheckoutPolar()
	billing := NewBilling(Dependencies{
		Store:         store,
		Polar:         provider,
		IDs:           defaultIDGenerator{},
		Pseudonymizer: pseudonymizer,
	})
	userContext := authenticatedContext(ctx, subject)
	request := func(key string) *connect.Request[delibasev1.CreateSubscriptionCheckoutRequest] {
		return connect.NewRequest(&delibasev1.CreateSubscriptionCheckoutRequest{
			OrganizationId: &delibasev1.UuidV7{Value: organizationID.String()},
			SuccessUrl:     "https://deli.dev/billing/success",
			CancelUrl:      "https://deli.dev/billing/cancel",
			Idempotency:    idempotency(key),
		})
	}
	type checkoutResult struct {
		response *connect.Response[delibasev1.CreateSubscriptionCheckoutResponse]
		err      error
	}
	results := make(chan checkoutResult, 2)
	go func() {
		response, createErr := billing.CreateSubscriptionCheckout(
			userContext, request("checkout-first"),
		)
		results <- checkoutResult{response: response, err: createErr}
	}()
	select {
	case <-provider.started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	go func() {
		response, createErr := billing.CreateSubscriptionCheckout(
			userContext, request("checkout-second"),
		)
		results <- checkoutResult{response: response, err: createErr}
	}()
	close(provider.release)

	first := <-results
	second := <-results
	var succeeded *connect.Response[delibasev1.CreateSubscriptionCheckoutResponse]
	var rejected error
	for _, result := range []checkoutResult{first, second} {
		if result.err == nil {
			if succeeded != nil {
				t.Fatal("both checkout requests succeeded")
			}
			succeeded = result.response
		} else {
			rejected = result.err
		}
	}
	if succeeded == nil || rejected == nil {
		t.Fatalf("checkout results = %#v, %#v", first, second)
	}
	requireConnectReason(
		t,
		rejected,
		connect.CodeAlreadyExists,
		delibasev1.ErrorReason_ERROR_REASON_RESOURCE_CONFLICT,
	)
	if provider.Calls() != 1 {
		t.Fatalf("Polar checkout calls = %d, want 1", provider.Calls())
	}

	replayed, err := billing.CreateSubscriptionCheckout(
		userContext, request("checkout-first"),
	)
	if err != nil || !replayed.Msg.Idempotency.Replayed ||
		replayed.Msg.CheckoutUrl != succeeded.Msg.CheckoutUrl {
		t.Fatalf("checkout replay = %#v, %v", replayed, err)
	}
	if provider.Calls() != 1 {
		t.Fatalf("Polar checkout replay calls = %d, want 1", provider.Calls())
	}

	summary, err := billing.GetBillingSummary(
		userContext,
		connect.NewRequest(&delibasev1.GetBillingSummaryRequest{
			OrganizationId: &delibasev1.UuidV7{Value: organizationID.String()},
		}),
	)
	if err != nil ||
		summary.Msg.Summary.SubscriptionStatus !=
			delibasev1.SubscriptionStatus_SUBSCRIPTION_STATUS_CHECKOUT_PENDING {
		t.Fatalf("pending checkout summary = %#v, %v", summary, err)
	}

	historyOrganizationID := uuidv7.MustNew()
	historyTeamID := uuidv7.MustNew()
	pastDueSubscriptionID := uuidv7.MustNew()
	canceledSubscriptionID := uuidv7.MustNew()
	now := time.Now().UTC()
	err = store.WithinTransaction(ctx, pgx.TxOptions{}, func(queries *dbgen.Queries) error {
		if _, transactionErr := queries.CreateOrganization(
			ctx,
			dbgen.CreateOrganizationParams{
				ID:   pgUUID(historyOrganizationID),
				Name: "Billing History Integration",
				Slug: "billing-history-" + historyOrganizationID.String()[24:],
			},
		); transactionErr != nil {
			return transactionErr
		}
		if _, transactionErr := queries.CreatePolarCustomer(
			ctx,
			dbgen.CreatePolarCustomerParams{
				OrganizationID:  pgUUID(historyOrganizationID),
				PolarCustomerID: "customer_" + historyOrganizationID.String(),
			},
		); transactionErr != nil {
			return transactionErr
		}
		if _, transactionErr := queries.CreateOrganizationMembership(
			ctx,
			dbgen.CreateOrganizationMembershipParams{
				OrganizationID: pgUUID(historyOrganizationID),
				AccountID:      pgUUID(accountID),
				Role:           "owner",
			},
		); transactionErr != nil {
			return transactionErr
		}
		if _, transactionErr := queries.CreateGeneralTeam(
			ctx,
			dbgen.CreateGeneralTeamParams{
				ID:             pgUUID(historyTeamID),
				OrganizationID: pgUUID(historyOrganizationID),
			},
		); transactionErr != nil {
			return transactionErr
		}
		if _, transactionErr := queries.InsertSubscription(
			ctx,
			dbgen.InsertSubscriptionParams{
				ID:                  pgUUID(pastDueSubscriptionID),
				OrganizationID:      pgUUID(historyOrganizationID),
				PolarSubscriptionID: "subscription_" + pastDueSubscriptionID.String(),
				Status:              "past_due",
				ProviderEventAt:     pgTimestamp(now.Add(-2 * time.Hour)),
			},
		); transactionErr != nil {
			return transactionErr
		}
		if _, transactionErr := queries.InsertSubscription(
			ctx,
			dbgen.InsertSubscriptionParams{
				ID:                  pgUUID(canceledSubscriptionID),
				OrganizationID:      pgUUID(historyOrganizationID),
				PolarSubscriptionID: "subscription_" + canceledSubscriptionID.String(),
				Status:              "canceled",
				ProviderEventAt:     pgTimestamp(now.Add(-time.Hour)),
			},
		); transactionErr != nil {
			return transactionErr
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	summary, err = billing.GetBillingSummary(
		userContext,
		connect.NewRequest(&delibasev1.GetBillingSummaryRequest{
			OrganizationId: &delibasev1.UuidV7{Value: historyOrganizationID.String()},
		}),
	)
	if err != nil ||
		summary.Msg.Summary.SubscriptionStatus !=
			delibasev1.SubscriptionStatus_SUBSCRIPTION_STATUS_CANCELED {
		t.Fatalf("newest inactive subscription summary = %#v, %v", summary, err)
	}
}

type blockingCheckoutPolar struct {
	mu      sync.Mutex
	calls   int
	started chan struct{}
	release chan struct{}
}

func newBlockingCheckoutPolar() *blockingCheckoutPolar {
	return &blockingCheckoutPolar{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (provider *blockingCheckoutPolar) CreateCheckout(
	ctx context.Context,
	_ contracts.CheckoutRequest,
) (contracts.Checkout, error) {
	provider.mu.Lock()
	provider.calls++
	call := provider.calls
	if call == 1 {
		close(provider.started)
	}
	provider.mu.Unlock()
	select {
	case <-provider.release:
	case <-ctx.Done():
		return contracts.Checkout{}, ctx.Err()
	}
	return contracts.Checkout{
		ID:        "checkout_" + strconv.Itoa(call),
		URL:       "https://polar.sh/checkout/test",
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	}, nil
}

func (*blockingCheckoutPolar) CreatePortalSession(
	context.Context,
	contracts.PortalRequest,
) (contracts.PortalSession, error) {
	return contracts.PortalSession{}, errors.New("unexpected portal call")
}

func (provider *blockingCheckoutPolar) Calls() int {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return provider.calls
}
