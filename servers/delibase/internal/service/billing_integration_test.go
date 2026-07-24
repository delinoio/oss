package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/delinoio/oss/servers/delibase/internal/database"
	"github.com/delinoio/oss/servers/delibase/internal/database/dbgen"
	"github.com/delinoio/oss/servers/delibase/internal/reliability"
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
	cycle, err := store.Queries().GetPolarPaidCycle(ctx, "order_1")
	if err != nil || cycle.GrantMicros != cycleGrantMicros ||
		cycle.ReversedMicros != cycleGrantMicros/2 {
		t.Fatalf("paid cycle = %#v, %v", cycle, err)
	}
}
