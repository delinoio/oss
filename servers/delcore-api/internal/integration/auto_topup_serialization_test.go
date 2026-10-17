//go:build integration

// SPDX-License-Identifier: Apache-2.0
package integration

import (
	"context"
	"sync"
	"testing"
	"time"

	pb "github.com/delinoio/oss/protos/gen/go/delcore/v1"
	"github.com/delinoio/oss/protos/gen/go/delcore/v1/delcorev1connect"
	"github.com/delinoio/oss/servers/delcore-api/internal/postgres"
	"github.com/delinoio/oss/servers/delcore-api/internal/worker"
	"github.com/jackc/pgx/v5"
)

// Automatic top-up settings and automatic top-up job creation are serialized by
// the transaction lock that postgres.Store.Transaction takes before any state
// transition. These tests pin that invariant from three sides: the lock itself,
// every settings writer that must end queued charges (disable and cancel)
// racing a maintenance pass that is deliberately held between choosing the
// tenant and queueing the charge, and an unhindered stress run. They fail if a
// settings change can commit between the maintenance selection and the insert.

const slowAutoTopUpQueueing = `
CREATE FUNCTION slow_auto_topup_insert() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	PERFORM pg_sleep(0.4);
	RETURN NEW;
END
$$;
CREATE TRIGGER slow_auto_topup_insert BEFORE INSERT ON jobs FOR EACH ROW WHEN (NEW.kind='auto_topup') EXECUTE FUNCTION slow_auto_topup_insert();`

// unattemptedAutoTopUps counts queued automatic charges that would still reach the provider.
func unattemptedAutoTopUps(t *testing.T, db *postgres.Store, tenant string) int {
	t.Helper()
	var count int
	if e := db.Pool.QueryRow(context.Background(), "SELECT count(*) FROM jobs WHERE tenant_id=$1 AND kind='auto_topup' AND state IN ('pending','running','reconcile')", tenant).Scan(&count); e != nil {
		t.Fatal(e)
	}
	return count
}

func enableAutoTopUp(t *testing.T, client delcorev1connect.DelcoreServiceClient, tenant string) {
	t.Helper()
	if _, e := client.SetAutoTopUp(context.Background(), req(&pb.SetAutoTopUpRequest{RequestKey: key(), TenantId: tenant, Enabled: true, Threshold: "5", Amount: "10", MonthlyCap: "100"}, "owner")); e != nil {
		t.Fatal(e)
	}
}

// waitForSleepingInsert blocks until some session is inside the widened insert.
func waitForSleepingInsert(t *testing.T, db *postgres.Store) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var sleeping int
		if e := db.Pool.QueryRow(context.Background(), "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event='PgSleep'").Scan(&sleeping); e != nil {
			t.Fatal(e)
		}
		if sleeping > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("maintenance never reached the automatic top-up insert")
}

func TestStoreTransactionsSerializeStateTransitions(t *testing.T) {
	db, _, _ := setup(t)
	ctx := context.Background()
	holding := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	// A failed assertion must not leave the first transaction holding its connection.
	defer unblock()
	first := make(chan error, 1)
	go func() {
		first <- db.Transaction(ctx, func(pgx.Tx) error {
			close(holding)
			<-release
			return nil
		})
	}()
	<-holding
	entered := make(chan struct{})
	second := make(chan error, 1)
	go func() {
		second <- db.Transaction(ctx, func(pgx.Tx) error {
			close(entered)
			return nil
		})
	}()
	select {
	case <-entered:
		t.Fatal("a second state transition started while another transaction was open")
	case <-time.After(300 * time.Millisecond):
	}
	unblock()
	if e := <-first; e != nil {
		t.Fatal(e)
	}
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the waiting transaction never started after the first one committed")
	}
	if e := <-second; e != nil {
		t.Fatal(e)
	}
}

func TestSettingsChangesWaitForAutoTopupQueueing(t *testing.T) {
	for _, tc := range []struct {
		name     string
		change   func(t *testing.T, c *settingsChange) error
		suspends bool
	}{
		{"disable", func(t *testing.T, c *settingsChange) error {
			_, e := c.client.SetAutoTopUp(context.Background(), req(&pb.SetAutoTopUpRequest{RequestKey: key(), TenantId: c.tenant, Enabled: false}, "owner"))
			return e
		}, false},
		{"cancel subscription", func(t *testing.T, c *settingsChange) error {
			_, e := c.client.CancelSubscription(context.Background(), req(&pb.CancelSubscriptionRequest{RequestKey: key(), TenantId: c.tenant}, "owner"))
			return e
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, client, cfg := setup(t)
			ctx := context.Background()
			_, tenant := bootstrap(t, client)
			if _, e := db.Pool.Exec(ctx, "UPDATE tenants SET external_subscription_id='sub_race',period_end=now()+interval '10 days' WHERE id=$1", tenant); e != nil {
				t.Fatal(e)
			}
			enableAutoTopUp(t, client, tenant)
			if _, e := db.Pool.Exec(ctx, slowAutoTopUpQueueing); e != nil {
				t.Fatal(e)
			}
			w := &worker.Worker{DB: db, Config: cfg}
			maintained := make(chan error, 1)
			go func() { maintained <- w.Maintain(ctx, time.Now()) }()
			// Maintenance has chosen the tenant and now holds its charge half-queued.
			waitForSleepingInsert(t, db)
			if e := tc.change(t, &settingsChange{client: client, tenant: tenant}); e != nil {
				t.Fatal("settings change failed", e)
			}
			if e := <-maintained; e != nil {
				t.Fatal("maintenance failed", e)
			}
			if pending := unattemptedAutoTopUps(t, db, tenant); pending != 0 {
				t.Fatal("a charge queued by the racing maintenance pass survived the settings change", pending)
			}
			var total int
			var suspended bool
			var configured bool
			if e := db.Pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM jobs WHERE tenant_id=$1 AND kind='auto_topup' AND state='done'),auto_suspended,auto_amount IS NOT NULL FROM tenants WHERE id=$1", tenant).Scan(&total, &suspended, &configured); e != nil {
				t.Fatal(e)
			}
			if total != 1 {
				t.Fatal("the racing pass should have queued exactly one charge that the settings change then stopped", total)
			}
			if tc.suspends && !suspended {
				t.Fatal("cancellation did not suspend automatic top-ups")
			}
			if !tc.suspends && configured {
				t.Fatal("disable left the settings configured")
			}
			// A later pass must not queue anything for the changed tenant.
			if e := w.Maintain(ctx, time.Now()); e != nil {
				t.Fatal(e)
			}
			if pending := unattemptedAutoTopUps(t, db, tenant); pending != 0 {
				t.Fatal("a later maintenance pass queued a charge after the settings change", pending)
			}
		})
	}
}

type settingsChange struct {
	client delcorev1connect.DelcoreServiceClient
	tenant string
}

// Without a widened window the same race is still decided by the lock alone:
// whichever side runs second sees the other's committed state.
func TestConcurrentDisableAndMaintenanceNeverLeaveQueuedCharge(t *testing.T) {
	db, client, cfg := setup(t)
	ctx := context.Background()
	_, owner := bootstrap(t, client)
	const tenants = 12
	ids := []string{owner}
	for len(ids) < tenants {
		org, e := client.CreateTenant(ctx, req(&pb.CreateTenantRequest{RequestKey: key(), Name: "Race organization"}, "owner"))
		if e != nil {
			t.Fatal(e)
		}
		ids = append(ids, org.Msg.Tenant.Id)
	}
	for _, id := range ids {
		if _, e := db.Pool.Exec(ctx, "UPDATE tenants SET external_subscription_id='sub_'||left(id::text,8),period_end=now()+interval '10 days' WHERE id=$1", id); e != nil {
			t.Fatal(e)
		}
		enableAutoTopUp(t, client, id)
	}
	w := &worker.Worker{DB: db, Config: cfg}
	var wg sync.WaitGroup
	errs := make(chan error, tenants+1)
	start := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		errs <- w.Maintain(ctx, time.Now())
	}()
	for _, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, e := client.SetAutoTopUp(ctx, req(&pb.SetAutoTopUpRequest{RequestKey: key(), TenantId: id, Enabled: false}, "owner"))
			errs <- e
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	for _, id := range ids {
		if pending := unattemptedAutoTopUps(t, db, id); pending != 0 {
			t.Fatal("disabled tenant kept a queued automatic charge", id, pending)
		}
	}
	// Nothing queued after the fact either.
	if e := w.Maintain(ctx, time.Now()); e != nil {
		t.Fatal(e)
	}
	for _, id := range ids {
		if pending := unattemptedAutoTopUps(t, db, id); pending != 0 {
			t.Fatal("maintenance queued a charge for a disabled tenant", id, pending)
		}
	}
}
