// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func creditOperation() domain.ServerCreditOperation {
	return domain.ServerCreditOperation{AccessOnly: true, ID: domain.NewID(), Epoch: domain.NewID(), AttemptID: domain.NewID(), AttemptEpoch: domain.NewID(), FinishID: domain.NewID(), ConnectionID: domain.NewID(), Generation: domain.NewID(), Actor: domain.Principal{Type: domain.OwnerDevice}, Phase: domain.SubscriptionObservationSending, RequestedAt: time.Now().UTC(), CreditID: "credit_1", CreditsObservationID: domain.NewID(), EverSent: true, SendClaimed: true}
}

var creditAuth = QuotaAuthentication{Access: "synthetic-access-only", Account: "synthetic-account", Plan: "plus"}

func TestCreditExternalConsumesOnceThenReadsQuotaSeparately(t *testing.T) {
	cfg := fixtureConfig(t, "quota-credit")
	cfg.Mode = CreditProtocol
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	op := creditOperation()
	var checkpoints []domain.SubscriptionResetOutcome
	outcome, observed, consumeErr, quotaErr := c.ConsumeExternalResetCredit(ctx, op, creditAuth, func(v domain.SubscriptionResetOutcome) error {
		// The durable claim is recorded before the independent quota request.
		if _, err := os.Stat(filepath.Join(cfg.Home, "quota-read")); !os.IsNotExist(err) {
			t.Fatal("quota was read before the outcome checkpoint")
		}
		checkpoints = append(checkpoints, v)
		return nil
	})
	if consumeErr != nil || quotaErr != nil || outcome != domain.SubscriptionReset || len(checkpoints) != 1 || len(observed.Windows) != 1 {
		t.Fatal("credit profile failed", consumeErr, quotaErr, outcome, checkpoints)
	}
	raw, err := os.ReadFile(filepath.Join(cfg.Home, "credit-consume"))
	if err != nil || strings.TrimSpace(string(raw)) != string(op.ID) {
		t.Fatal("official consume did not use the original idempotency key", err, string(raw))
	}
	if _, err := os.Stat(filepath.Join(cfg.Home, "auth.json")); !os.IsNotExist(err) {
		t.Fatal("credit profile wrote managed auth file")
	}
	if _, _, replay, _ := c.ConsumeExternalResetCredit(ctx, op, creditAuth, func(domain.SubscriptionResetOutcome) error { return nil }); replay == nil {
		t.Fatal("single-use credit client sent twice")
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCreditExternalRefusesRenewalAndKeepsOutcomeWhenQuotaFails(t *testing.T) {
	t.Run("refresh", func(t *testing.T) {
		cfg := fixtureConfig(t, "quota-credit-refresh")
		cfg.Mode = CreditProtocol
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		c, err := Open(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		outcome, _, consumeErr, _ := c.ConsumeExternalResetCredit(ctx, creditOperation(), creditAuth, func(domain.SubscriptionResetOutcome) error { return nil })
		if consumeErr == nil || outcome != "" {
			t.Fatal("token renewal callback was accepted")
		}
		if _, err := os.Stat(filepath.Join(cfg.Home, "credit-refused")); err != nil {
			t.Fatal("renewal callback was not explicitly refused", err)
		}
		if _, err := os.Stat(filepath.Join(cfg.Home, "auth.json")); !os.IsNotExist(err) {
			t.Fatal("credit profile wrote managed auth file")
		}
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("quota-failure", func(t *testing.T) {
		cfg := fixtureConfig(t, "quota-credit-readfail")
		cfg.Mode = CreditProtocol
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		c, err := Open(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		outcome, observed, consumeErr, quotaErr := c.ConsumeExternalResetCredit(ctx, creditOperation(), creditAuth, func(domain.SubscriptionResetOutcome) error { return nil })
		if consumeErr != nil || outcome != domain.SubscriptionReset || quotaErr == nil || len(observed.Windows) != 0 {
			t.Fatal("quota failure erased or invented consumption", consumeErr, quotaErr, outcome)
		}
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestQuotaOnlyProfileCannotConsume(t *testing.T) {
	cfg := fixtureConfig(t, "quota-credit")
	cfg.Mode = QuotaProtocol
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	_, _, consumeErr, _ := c.ConsumeExternalResetCredit(ctx, creditOperation(), creditAuth, func(domain.SubscriptionResetOutcome) error { return nil })
	if consumeErr == nil {
		t.Fatal("read-only quota profile accepted consumption")
	}
	if _, err := os.Stat(filepath.Join(cfg.Home, "credit-consume")); !os.IsNotExist(err) {
		t.Fatal("read-only profile sent a consume request")
	}
	if _, err := c.ReadExternalQuota(ctx, domain.NewID(), creditAuth); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCreditProfileRejectsForeignModesAndUnsentOperations(t *testing.T) {
	cfg := fixtureConfig(t, "quota-credit")
	cfg.Mode = CreditProtocol
	cfg.ManagedAuthentication = true
	if c, err := Open(context.Background(), cfg); err == nil {
		c.Close()
		t.Fatal("credit profile accepted managed authentication")
	}
	cfg = fixtureConfig(t, "quota-credit")
	cfg.Mode = CreditProtocol
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	op := creditOperation()
	op.SendClaimed = false
	if _, _, consumeErr, _ := c.ConsumeExternalResetCredit(ctx, op, creditAuth, func(domain.SubscriptionResetOutcome) error { return nil }); consumeErr == nil {
		t.Fatal("consumption ran without a durable send claim")
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCreditProfileRejectsUnexpectedManagedAuthFile(t *testing.T) {
	cfg := fixtureConfig(t, "quota-credit")
	cfg.Mode = CreditProtocol
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.Home, "auth.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, consumeErr, _ := c.ConsumeExternalResetCredit(ctx, creditOperation(), creditAuth, func(domain.SubscriptionResetOutcome) error { return nil }); consumeErr == nil {
		t.Fatal("credit profile ran beside a managed auth file")
	}
	if _, err := os.Stat(filepath.Join(cfg.Home, "credit-consume")); !os.IsNotExist(err) {
		t.Fatal("credit request was sent beside a managed auth file")
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
}
