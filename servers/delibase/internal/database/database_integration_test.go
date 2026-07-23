package database

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/delinoio/oss/servers/delibase/internal/catalog"
	"github.com/delinoio/oss/servers/delibase/internal/database/dbgen"
	"github.com/delinoio/oss/servers/internal/uuidv7"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestPostgreSQLMigrationsAndTransactionRollback(t *testing.T) {
	databaseURL := os.Getenv("DELIBASE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DELIBASE_TEST_DATABASE_URL is not set; run scripts/test-postgres.sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	store, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Ping(ctx); err != nil {
		t.Fatal(err)
	}

	id, err := uuidv7.New()
	if err != nil {
		t.Fatal(err)
	}
	subject := "integration-rollback-" + id.String()
	rollback := errors.New("force rollback")
	err = store.WithinTransaction(ctx, pgx.TxOptions{
		IsoLevel: pgx.Serializable,
	}, func(queries *dbgen.Queries) error {
		_, err := queries.CreateAccount(ctx, dbgen.CreateAccountParams{
			ID: pgtype.UUID{
				Bytes: id,
				Valid: true,
			},
			LogtoSubject: subject,
			DisplayName:  "Rollback Test",
		})
		if err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("WithinTransaction() error = %v", err)
	}
	if _, err := store.Queries().GetAccountByLogtoSubject(ctx, subject); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("rolled-back account lookup error = %v", err)
	}

	// A second Open exercises ordered migration idempotency and checksum
	// validation against the same ephemeral PostgreSQL database.
	second, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	second.Close()
}

func TestPostgreSQLCatalogSyncIsIdempotentAndDisablesStaleState(t *testing.T) {
	databaseURL := os.Getenv("DELIBASE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DELIBASE_TEST_DATABASE_URL is not set; run scripts/test-postgres.sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	store, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	enabled := true
	disabled := false
	precision := 0
	specification := catalog.Specification{
		Version: 1,
		Apps: []catalog.App{{
			ID:      "0198a000-0000-7000-8000-000000000301",
			Slug:    "sync-app",
			Name:    "Sync App",
			Enabled: &enabled,
		}},
		Meters: []catalog.Meter{{
			ID:                    "0198a000-0000-7000-8000-000000000302",
			AppID:                 "0198a000-0000-7000-8000-000000000301",
			Key:                   "requests",
			Name:                  "Requests",
			UnitName:              "request",
			UnitPrecision:         &precision,
			ReservationTTLSeconds: 60,
			Enabled:               &enabled,
		}},
		Prices: []catalog.Price{{
			ID:               "0198a000-0000-7000-8000-000000000303",
			MeterID:          "0198a000-0000-7000-8000-000000000302",
			USDMicrosPerUnit: 1,
			EffectiveFrom:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		}},
		Services: []catalog.Service{{
			ID:              "0198a000-0000-7000-8000-000000000304",
			LogtoClientID:   "sync-service",
			Name:            "Sync Service",
			Enabled:         &enabled,
			AllowedMeterIDs: []string{"0198a000-0000-7000-8000-000000000302"},
		}},
		PolarMeters: []catalog.PolarMeter{{
			MeterID:      "0198a000-0000-7000-8000-000000000302",
			PolarMeterID: "sync-polar-meter",
		}},
	}
	if err := store.SyncCatalog(ctx, specification); err != nil {
		t.Fatal(err)
	}
	if err := store.SyncCatalog(ctx, specification); err != nil {
		t.Fatal(err)
	}
	priceChangeAt := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	specification.Prices[0].EffectiveUntil = &priceChangeAt
	specification.Prices = append(specification.Prices, catalog.Price{
		ID:               "0198a000-0000-7000-8000-000000000305",
		MeterID:          "0198a000-0000-7000-8000-000000000302",
		USDMicrosPerUnit: 2,
		EffectiveFrom:    priceChangeAt,
	})
	if err := store.SyncCatalog(ctx, specification); err != nil {
		t.Fatal(err)
	}
	if err := store.SyncCatalog(ctx, specification); err != nil {
		t.Fatal(err)
	}
	const (
		holdAccount = "0198a000-0000-7000-8000-000000000306"
		holdOrg     = "0198a000-0000-7000-8000-000000000307"
		holdTeam    = "0198a000-0000-7000-8000-000000000308"
		holdID      = "0198a000-0000-7000-8000-000000000309"
	)
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO accounts (id, logto_subject)
		VALUES ($1, 'catalog-sync-hold')
	`, holdAccount); err != nil {
		t.Fatal(err)
	}
	createTestOrganization(
		t,
		ctx,
		store,
		holdOrg,
		"Catalog Sync Hold",
		"catalog-sync-hold",
		holdAccount,
	)
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO teams (id, organization_id, name)
		VALUES ($1, $2, 'Catalog Sync Team')
	`, holdTeam, holdOrg); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO ledger_entries (
			id, organization_id, entry_type, amount_micros,
			balance_after_micros, source_reference
		) VALUES (
			'0198a000-0000-7000-8000-000000000313',
			$1, 'credit_grant', 2, 2, 'catalog-sync-credit'
		)
	`, holdOrg); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO usage_reservations (
			id, organization_id, team_id, team_name_snapshot, meter_id,
			price_version_id, account_id, service_identity_id, maximum_units,
			usd_micros_per_unit, maximum_cost_micros, held_credit_micros,
			held_overage_micros, client_reference, expires_at
		) VALUES (
			$1, $2, $3, 'Catalog Sync Team', $4, $5, $6, $7,
			1, 2, 2, 2, 0, 'catalog-sync-active-hold',
			transaction_timestamp() + interval '1 minute'
		)
	`, holdID, holdOrg, holdTeam, specification.Meters[0].ID,
		specification.Prices[1].ID, holdAccount, specification.Services[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := store.SyncCatalog(ctx, specification); err != nil {
		t.Fatalf("SyncCatalog() with an unchanged active allowlist = %v", err)
	}
	requireDisabledCatalogReservationFailure := func(id, clientReference string) {
		t.Helper()
		if _, err := store.pool.Exec(ctx, `
			INSERT INTO usage_reservations (
				id, organization_id, team_id, team_name_snapshot, meter_id,
				price_version_id, account_id, service_identity_id, maximum_units,
				usd_micros_per_unit, maximum_cost_micros, held_credit_micros,
				held_overage_micros, client_reference, expires_at
			) VALUES (
				$1, $2, $3, 'Catalog Sync Team', $4, $5, $6, $7,
				1, 2, 2, 2, 0, $8,
				transaction_timestamp() + interval '1 minute'
			)
		`, id, holdOrg, holdTeam, specification.Meters[0].ID,
			specification.Prices[1].ID, holdAccount, specification.Services[0].ID,
			clientReference); err == nil {
			t.Fatalf("%s authorized a new reservation", clientReference)
		}
	}
	specification.Services[0].Enabled = &disabled
	if err := store.SyncCatalog(ctx, specification); err != nil {
		t.Fatal(err)
	}
	requireDisabledCatalogReservationFailure(
		"0198a000-0000-7000-8000-000000000311",
		"disabled-service",
	)
	specification.Services[0].Enabled = &enabled
	specification.Meters[0].Enabled = &disabled
	if err := store.SyncCatalog(ctx, specification); err != nil {
		t.Fatal(err)
	}
	requireDisabledCatalogReservationFailure(
		"0198a000-0000-7000-8000-000000000312",
		"disabled-meter",
	)
	specification.Meters[0].Enabled = &enabled
	if err := store.SyncCatalog(ctx, specification); err != nil {
		t.Fatal(err)
	}
	var closedAt time.Time
	var priceVersions int
	if err := store.pool.QueryRow(ctx, `
		SELECT
			(SELECT effective_until FROM catalog_price_versions WHERE id = $1),
			(SELECT count(*) FROM catalog_price_versions WHERE meter_id = $2)
	`, specification.Prices[0].ID, specification.Meters[0].ID).Scan(
		&closedAt,
		&priceVersions,
	); err != nil {
		t.Fatal(err)
	}
	if !closedAt.Equal(priceChangeAt) || priceVersions != 2 {
		t.Fatalf("price rollover = closed at %v with %d versions", closedAt, priceVersions)
	}
	var activeMeters int
	if err := store.pool.QueryRow(
		ctx,
		"SELECT count(*) FROM catalog_meters WHERE enabled",
	).Scan(&activeMeters); err != nil || activeMeters != 1 {
		t.Fatalf("active meter count = %d, %v", activeMeters, err)
	}

	empty := catalog.Specification{
		Version:     1,
		Apps:        []catalog.App{},
		Meters:      []catalog.Meter{},
		Prices:      []catalog.Price{},
		Services:    []catalog.Service{},
		PolarMeters: []catalog.PolarMeter{},
	}
	if err := store.SyncCatalog(ctx, empty); err != nil {
		t.Fatal(err)
	}
	var activeApps, activeServices, serviceMappings, enabledServiceMappings, polarMappings int
	if err := store.pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM catalog_apps WHERE enabled),
			(SELECT count(*) FROM service_identities WHERE enabled),
			(SELECT count(*) FROM service_meter_allowlists),
			(SELECT count(*) FROM service_meter_allowlists WHERE enabled),
			(SELECT count(*) FROM polar_meter_mappings)
	`).Scan(
		&activeApps,
		&activeServices,
		&serviceMappings,
		&enabledServiceMappings,
		&polarMappings,
	); err != nil {
		t.Fatal(err)
	}
	if activeApps != 0 || activeServices != 0 ||
		serviceMappings != 1 || enabledServiceMappings != 0 || polarMappings != 1 {
		t.Fatalf(
			"stale catalog state = apps:%d services:%d service mappings:%d enabled service mappings:%d Polar mappings:%d",
			activeApps,
			activeServices,
			serviceMappings,
			enabledServiceMappings,
			polarMappings,
		)
	}
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO usage_reservations (
			id, organization_id, team_id, team_name_snapshot, meter_id,
			price_version_id, account_id, service_identity_id, maximum_units,
			usd_micros_per_unit, maximum_cost_micros, held_credit_micros,
			held_overage_micros, client_reference, expires_at
		) VALUES (
			'0198a000-0000-7000-8000-000000000310',
			$1, $2, 'Catalog Sync Team', $3, $4, $5, $6,
			1, 2, 2, 2, 0, 'disabled-catalog-mapping',
			transaction_timestamp() + interval '1 minute'
		)
	`, holdOrg, holdTeam, specification.Meters[0].ID,
		specification.Prices[1].ID, holdAccount, specification.Services[0].ID); err == nil {
		t.Fatal("disabled retained allowlist authorized a new reservation")
	}
	if _, err := store.pool.Exec(ctx, `
		UPDATE usage_reservations
		SET status = 'released',
		    finalized_at = transaction_timestamp()
		WHERE id = $1
	`, holdID); err != nil {
		t.Fatal(err)
	}
	if err := store.SyncCatalog(ctx, empty); err != nil {
		t.Fatal(err)
	}
	if err := store.pool.QueryRow(
		ctx,
		`SELECT
			(SELECT count(*) FROM service_meter_allowlists),
			(SELECT count(*) FROM polar_meter_mappings)`,
	).Scan(&serviceMappings, &polarMappings); err != nil ||
		serviceMappings != 0 || polarMappings != 0 {
		t.Fatalf(
			"finalized mappings = service:%d Polar:%d, %v",
			serviceMappings,
			polarMappings,
			err,
		)
	}
}

func TestPostgreSQLRequiresOrganizationFoundationAtCommit(t *testing.T) {
	databaseURL := os.Getenv("DELIBASE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DELIBASE_TEST_DATABASE_URL is not set; run scripts/test-postgres.sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	store, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	const (
		accountID  = "0198a000-0000-7000-8000-000000000390"
		orgID      = "0198a000-0000-7000-8000-000000000391"
		generalID  = "0198a000-0000-7000-8000-000000000392"
		customerID = "foundation-polar-customer"
	)
	transaction, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO organizations (id, name, slug)
		VALUES ($1, 'Owner Required', 'owner-required')
	`, orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO teams (id, organization_id, name, protected_general)
		VALUES ($1, $2, 'General', true)
	`, generalID, orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO polar_customers (organization_id, polar_customer_id)
		VALUES ($1, $2)
	`, orgID, customerID); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Commit(ctx); err == nil {
		t.Fatal("ownerless organization committed")
	}

	if _, err := store.pool.Exec(ctx, `
		INSERT INTO accounts (id, logto_subject)
		VALUES ($1, 'owner-required')
	`, accountID); err != nil {
		t.Fatal(err)
	}
	transaction, err = store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO organizations (id, name, slug)
		VALUES ($1, 'General Required', 'general-required')
	`, orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO organization_memberships (organization_id, account_id, role)
		VALUES ($1, $2, 'owner')
	`, orgID, accountID); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO polar_customers (organization_id, polar_customer_id)
		VALUES ($1, $2)
	`, orgID, customerID); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Commit(ctx); err == nil {
		t.Fatal("organization without a protected General team committed")
	}

	transaction, err = store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO organizations (id, name, slug)
		VALUES ($1, 'Polar Customer Required', 'polar-customer-required')
	`, orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO organization_memberships (organization_id, account_id, role)
		VALUES ($1, $2, 'owner')
	`, orgID, accountID); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO teams (id, organization_id, name, protected_general)
		VALUES ($1, $2, 'General', true)
	`, generalID, orgID); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Commit(ctx); err == nil {
		t.Fatal("organization without a Polar customer committed")
	}

	if _, err := store.pool.Exec(
		ctx,
		"UPDATE accounts SET status = 'disabled' WHERE id = $1",
		accountID,
	); err != nil {
		t.Fatal(err)
	}
	transaction, err = store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO organizations (id, name, slug)
		VALUES ($1, 'Active Owner Required', 'active-owner-required')
	`, orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO organization_memberships (organization_id, account_id, role)
		VALUES ($1, $2, 'owner')
	`, orgID, accountID); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO teams (id, organization_id, name, protected_general)
		VALUES ($1, $2, 'General', true)
	`, generalID, orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO polar_customers (organization_id, polar_customer_id)
		VALUES ($1, $2)
	`, orgID, customerID); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Commit(ctx); err == nil {
		t.Fatal("organization with only a disabled owner committed")
	}
	if _, err := store.pool.Exec(
		ctx,
		"UPDATE accounts SET status = 'active' WHERE id = $1",
		accountID,
	); err != nil {
		t.Fatal(err)
	}

	createTestOrganization(
		t,
		ctx,
		store,
		orgID,
		"Owner Required",
		"owner-required",
		accountID,
	)
	if _, err := store.pool.Exec(
		ctx,
		"UPDATE accounts SET status = 'disabled' WHERE id = $1",
		accountID,
	); err == nil {
		t.Fatal("last active organization owner was disabled")
	}
	if _, err := store.pool.Exec(
		ctx,
		"DELETE FROM polar_customers WHERE organization_id = $1",
		orgID,
	); err == nil {
		t.Fatal("committed organization lost its Polar customer")
	}
	defer func() {
		cleanupCtx := context.WithoutCancel(ctx)
		_, _ = store.pool.Exec(
			cleanupCtx,
			"DELETE FROM organizations WHERE id = $1",
			orgID,
		)
		_, _ = store.pool.Exec(
			cleanupCtx,
			"DELETE FROM accounts WHERE id = $1",
			accountID,
		)
	}()
}

func TestPostgreSQLSerializesConcurrentOwnerRemoval(t *testing.T) {
	databaseURL := os.Getenv("DELIBASE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DELIBASE_TEST_DATABASE_URL is not set; run scripts/test-postgres.sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	store, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	const (
		accountA = "0198a000-0000-7000-8000-000000000401"
		accountB = "0198a000-0000-7000-8000-000000000402"
		orgID    = "0198a000-0000-7000-8000-000000000403"
	)
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO accounts (id, logto_subject)
		VALUES ($1, 'concurrent-owner-a'), ($2, 'concurrent-owner-b')
	`, accountA, accountB); err != nil {
		t.Fatal(err)
	}
	createTestOrganization(
		t,
		ctx,
		store,
		orgID,
		"Concurrent Owners",
		"concurrent-owners",
		accountA,
	)
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO organization_memberships (organization_id, account_id, role)
		VALUES ($1, $2, 'owner')
	`, orgID, accountB); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanupCtx := context.WithoutCancel(ctx)
		_, _ = store.pool.Exec(
			cleanupCtx,
			"DELETE FROM organizations WHERE id = $1",
			orgID,
		)
		_, _ = store.pool.Exec(
			context.WithoutCancel(ctx),
			"DELETE FROM accounts WHERE id IN ($1, $2)",
			accountA,
			accountB,
		)
	}()

	first, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Rollback(context.WithoutCancel(ctx)) }()
	second, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Rollback(context.WithoutCancel(ctx)) }()

	if _, err := first.Exec(
		ctx,
		"DELETE FROM organization_memberships WHERE organization_id = $1 AND account_id = $2",
		orgID,
		accountA,
	); err != nil {
		t.Fatal(err)
	}
	secondResult := make(chan error, 1)
	go func() {
		_, err := second.Exec(
			ctx,
			"DELETE FROM organization_memberships WHERE organization_id = $1 AND account_id = $2",
			orgID,
			accountB,
		)
		secondResult <- err
	}()

	select {
	case err := <-secondResult:
		t.Fatalf("second owner removal did not serialize: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := first.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-secondResult:
		if err == nil {
			t.Fatal("concurrent removals left the organization ownerless")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := second.Rollback(ctx); err != nil {
		t.Fatal(err)
	}

	var owners int
	if err := store.pool.QueryRow(
		ctx,
		"SELECT count(*) FROM organization_memberships WHERE organization_id = $1 AND role = 'owner'",
		orgID,
	).Scan(&owners); err != nil {
		t.Fatal(err)
	}
	if owners != 1 {
		t.Fatalf("owner count = %d, want 1", owners)
	}
}

func TestPostgreSQLSerializesConcurrentTeamMoves(t *testing.T) {
	databaseURL := os.Getenv("DELIBASE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DELIBASE_TEST_DATABASE_URL is not set; run scripts/test-postgres.sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	store, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	const (
		accountID = "0198a000-0000-7000-8000-000000000410"
		orgID     = "0198a000-0000-7000-8000-000000000411"
		teamA     = "0198a000-0000-7000-8000-000000000412"
		teamB     = "0198a000-0000-7000-8000-000000000413"
	)
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO accounts (id, logto_subject)
		VALUES ($1, 'concurrent-team-owner')
	`, accountID); err != nil {
		t.Fatal(err)
	}
	createTestOrganization(
		t,
		ctx,
		store,
		orgID,
		"Concurrent Team Moves",
		"concurrent-team-moves",
		accountID,
	)
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO teams (id, organization_id, name)
		VALUES ($1, $3, 'A'), ($2, $3, 'B')
	`, teamA, teamB, orgID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = store.pool.Exec(
			context.WithoutCancel(ctx),
			"DELETE FROM organizations WHERE id = $1",
			orgID,
		)
		_, _ = store.pool.Exec(
			context.WithoutCancel(ctx),
			"DELETE FROM accounts WHERE id = $1",
			accountID,
		)
	}()

	first, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Rollback(context.WithoutCancel(ctx)) }()
	second, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Rollback(context.WithoutCancel(ctx)) }()

	if _, err := first.Exec(
		ctx,
		"UPDATE teams SET parent_team_id = $1 WHERE id = $2",
		teamB,
		teamA,
	); err != nil {
		t.Fatal(err)
	}
	secondResult := make(chan error, 1)
	go func() {
		_, err := second.Exec(
			ctx,
			"UPDATE teams SET parent_team_id = $1 WHERE id = $2",
			teamA,
			teamB,
		)
		secondResult <- err
	}()

	select {
	case err := <-secondResult:
		t.Fatalf("second team move did not serialize: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := first.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-secondResult:
		if err == nil {
			t.Fatal("concurrent team moves created a hierarchy cycle")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := second.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestPostgreSQLReservationSerializesAuthoritativeStateChanges(t *testing.T) {
	databaseURL := os.Getenv("DELIBASE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DELIBASE_TEST_DATABASE_URL is not set; run scripts/test-postgres.sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	store, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	const (
		accountID      = "0198a000-0000-7000-8000-000000000501"
		ownerAccountID = "0198a000-0000-7000-8000-000000000510"
		organizationID = "0198a000-0000-7000-8000-000000000502"
		teamID         = "0198a000-0000-7000-8000-000000000503"
		appID          = "0198a000-0000-7000-8000-000000000504"
		meterID        = "0198a000-0000-7000-8000-000000000505"
		priceID        = "0198a000-0000-7000-8000-000000000506"
		serviceID      = "0198a000-0000-7000-8000-000000000507"
		reservationID  = "0198a000-0000-7000-8000-000000000508"
		ledgerID       = "0198a000-0000-7000-8000-000000000509"
	)
	if _, err := store.pool.Exec(
		ctx,
		`INSERT INTO accounts (id, logto_subject)
		VALUES ($1, 'reservation-lock-user'), ($2, 'reservation-lock-owner')`,
		accountID,
		ownerAccountID,
	); err != nil {
		t.Fatal(err)
	}
	createTestOrganization(
		t,
		ctx,
		store,
		organizationID,
		"Reservation Locks",
		"reservation-locks",
		ownerAccountID,
	)
	setup := []struct {
		statement string
		arguments []any
	}{
		{
			"INSERT INTO organization_memberships (organization_id, account_id, role) VALUES ($1, $2, 'member')",
			[]any{organizationID, accountID},
		},
		{
			"INSERT INTO teams (id, organization_id, name) VALUES ($1, $2, 'Locked Team')",
			[]any{teamID, organizationID},
		},
		{
			"INSERT INTO team_memberships (organization_id, team_id, account_id, role) VALUES ($1, $2, $3, 'member')",
			[]any{organizationID, teamID, accountID},
		},
		{
			"INSERT INTO catalog_apps (id, slug, name, enabled) VALUES ($1, 'reservation-lock-app', 'Reservation Lock App', true)",
			[]any{appID},
		},
		{
			"INSERT INTO catalog_meters (id, app_id, meter_key, name, unit_name, reservation_ttl_seconds, enabled) VALUES ($1, $2, 'requests', 'Requests', 'request', 60, true)",
			[]any{meterID, appID},
		},
		{
			"INSERT INTO catalog_price_versions (id, meter_id, usd_micros_per_unit, effective_from) VALUES ($1, $2, 1, transaction_timestamp() - interval '1 day')",
			[]any{priceID, meterID},
		},
		{
			"INSERT INTO service_identities (id, logto_client_id, name) VALUES ($1, 'reservation-lock-service', 'Reservation Lock Service')",
			[]any{serviceID},
		},
		{
			"INSERT INTO service_meter_allowlists (service_identity_id, meter_id) VALUES ($1, $2)",
			[]any{serviceID, meterID},
		},
		{
			"INSERT INTO polar_meter_mappings (meter_id, polar_meter_id) VALUES ($1, 'reservation-lock-meter')",
			[]any{meterID},
		},
		{
			"INSERT INTO ledger_entries (id, organization_id, entry_type, amount_micros, balance_after_micros, source_reference) VALUES ($1, $2, 'credit_grant', 1, 1, 'reservation-lock-credit')",
			[]any{ledgerID, organizationID},
		},
	}
	for _, item := range setup {
		if _, err := store.pool.Exec(ctx, item.statement, item.arguments...); err != nil {
			t.Fatal(err)
		}
	}

	reservation, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reservation.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := reservation.Exec(ctx, `
		INSERT INTO usage_reservations (
			id, organization_id, team_id, team_name_snapshot, meter_id,
			price_version_id, account_id, service_identity_id, maximum_units,
			usd_micros_per_unit, maximum_cost_micros, held_credit_micros,
			held_overage_micros, client_reference, expires_at
		) VALUES (
			$1, $2, $3, 'Locked Team', $4, $5, $6, $7,
			1, 1, 1, 1, 0, 'reservation-lock',
			transaction_timestamp() + interval '1 minute'
		)
	`, reservationID, organizationID, teamID, meterID, priceID, accountID, serviceID); err != nil {
		t.Fatal(err)
	}

	updates := []struct {
		name      string
		statement string
		arguments []any
	}{
		{
			name:      "team name",
			statement: "UPDATE teams SET name = 'Renamed Team' WHERE id = $1",
			arguments: []any{teamID},
		},
		{
			name:      "team access",
			statement: "DELETE FROM team_memberships WHERE team_id = $1 AND account_id = $2",
			arguments: []any{teamID, accountID},
		},
		{
			name:      "catalog enabled state",
			statement: "UPDATE service_identities SET enabled = false WHERE id = $1",
			arguments: []any{serviceID},
		},
		{
			name:      "catalog price window",
			statement: "UPDATE catalog_price_versions SET effective_until = transaction_timestamp() WHERE id = $1",
			arguments: []any{priceID},
		},
		{
			name:      "catalog meter TTL",
			statement: "UPDATE catalog_meters SET reservation_ttl_seconds = 120 WHERE id = $1",
			arguments: []any{meterID},
		},
	}
	type updateResult struct {
		name string
		err  error
	}
	results := make(chan updateResult, len(updates))
	for _, update := range updates {
		update := update
		go func() {
			_, err := store.pool.Exec(ctx, update.statement, update.arguments...)
			results <- updateResult{name: update.name, err: err}
		}()
	}

	select {
	case result := <-results:
		t.Fatalf("%s mutation did not serialize with reservation: %v", result.name, result.err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := reservation.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for range updates {
		select {
		case result := <-results:
			if result.err != nil {
				t.Fatalf("%s mutation after reservation commit: %v", result.name, result.err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
}

func TestPostgreSQLSchemaEnforcesOrganizationBoundariesAndRetention(t *testing.T) {
	databaseURL := os.Getenv("DELIBASE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DELIBASE_TEST_DATABASE_URL is not set; run scripts/test-postgres.sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	store, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	transaction, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = transaction.Rollback(context.WithoutCancel(ctx)) }()

	const (
		accountA       = "0198a000-0000-7000-8000-000000000001"
		accountB       = "0198a000-0000-7000-8000-000000000002"
		accountC       = "0198a000-0000-7000-8000-000000000003"
		accountD       = "0198a000-0000-7000-8000-000000000004"
		historyUser    = "0198a000-0000-7000-8000-000000000005"
		orgA           = "0198a000-0000-7000-8000-000000000011"
		orgB           = "0198a000-0000-7000-8000-000000000012"
		orgC           = "0198a000-0000-7000-8000-000000000013"
		teamA          = "0198a000-0000-7000-8000-000000000021"
		teamB          = "0198a000-0000-7000-8000-000000000022"
		activeTeam     = "0198a000-0000-7000-8000-000000000023"
		generalA       = "0198a000-0000-7000-8000-000000000024"
		generalC       = "0198a000-0000-7000-8000-000000000025"
		generalB       = "0198a000-0000-7000-8000-000000000220"
		inheritedTeam  = "0198a000-0000-7000-8000-000000000140"
		appID          = "0198a000-0000-7000-8000-000000000031"
		meterID        = "0198a000-0000-7000-8000-000000000032"
		priceID        = "0198a000-0000-7000-8000-000000000033"
		serviceID      = "0198a000-0000-7000-8000-000000000034"
		meterB         = "0198a000-0000-7000-8000-000000000035"
		priceB         = "0198a000-0000-7000-8000-000000000036"
		serviceB       = "0198a000-0000-7000-8000-000000000037"
		stalePrice     = "0198a000-0000-7000-8000-000000000038"
		reserveID      = "0198a000-0000-7000-8000-000000000041"
		inviteID       = "0198a000-0000-7000-8000-000000000042"
		recordID       = "0198a000-0000-7000-8000-000000000043"
		activeHold     = "0198a000-0000-7000-8000-000000000044"
		releasedHold   = "0198a000-0000-7000-8000-000000000047"
		expiredHold    = "0198a000-0000-7000-8000-000000000048"
		inheritedHold  = "0198a000-0000-7000-8000-000000000141"
		deletionInvite = "0198a000-0000-7000-8000-000000000053"
		subA           = "0198a000-0000-7000-8000-000000000061"
		subB           = "0198a000-0000-7000-8000-000000000062"
		periodA        = "0198a000-0000-7000-8000-000000000071"
		periodB        = "0198a000-0000-7000-8000-000000000072"
		pastPeriodA    = "0198a000-0000-7000-8000-000000000221"
		linkedLedger   = "0198a000-0000-7000-8000-000000000077"
		retainedLedger = "0198a000-0000-7000-8000-000000000078"
		historyReserve = "0198a000-0000-7000-8000-000000000113"
		historyRecord  = "0198a000-0000-7000-8000-000000000114"
		auditID        = "0198a000-0000-7000-8000-000000000115"
		accountJob     = "0198a000-0000-7000-8000-000000000051"
		orgJob         = "0198a000-0000-7000-8000-000000000052"
	)
	setup := []struct {
		statement string
		arguments []any
	}{
		{"INSERT INTO accounts (id, logto_subject) VALUES ($1, 'schema-a'), ($2, 'schema-b'), ($3, 'schema-c'), ($4, 'schema-d'), ($5, 'schema-history')", []any{accountA, accountB, accountC, accountD, historyUser}},
		{"INSERT INTO organizations (id, name, slug) VALUES ($1, 'A', 'schema-a'), ($2, 'B', 'schema-b'), ($3, 'C', 'schema-c')", []any{orgA, orgB, orgC}},
		{"INSERT INTO organization_memberships (organization_id, account_id, role) VALUES ($1, $2, 'owner'), ($3, $4, 'owner'), ($5, $6, 'owner')", []any{orgA, accountA, orgB, accountB, orgC, accountD}},
		{"INSERT INTO organization_memberships (organization_id, account_id, role) VALUES ($1, $2, 'member'), ($1, $3, 'member')", []any{orgA, historyUser, accountC}},
		{"INSERT INTO teams (id, organization_id, name) VALUES ($1, $2, 'A'), ($3, $4, 'B'), ($5, $2, 'Active')", []any{teamA, orgA, teamB, orgB, activeTeam}},
		{"INSERT INTO teams (id, organization_id, parent_team_id, name) VALUES ($1, $2, $3, 'Inherited')", []any{inheritedTeam, orgA, teamA}},
		{"INSERT INTO teams (id, organization_id, name, protected_general) VALUES ($1, $2, 'General', true), ($3, $4, 'General', true), ($5, $6, 'General', true)", []any{generalA, orgA, generalC, orgC, generalB, orgB}},
		{"INSERT INTO team_memberships (organization_id, team_id, account_id, role) VALUES ($1, $2, $3, 'member')", []any{orgA, teamA, historyUser}},
		{"INSERT INTO organization_invitations (id, organization_id, token_hash, organization_role, created_by_account_id, expires_at) VALUES ($1, $2, decode(repeat('12', 32), 'hex'), 'admin', $3, transaction_timestamp() + interval '1 day')", []any{deletionInvite, orgA, accountC}},
		{"INSERT INTO catalog_apps (id, slug, name, enabled) VALUES ($1, 'schema-app', 'Schema App', true)", []any{appID}},
		{"INSERT INTO catalog_meters (id, app_id, meter_key, name, unit_name, reservation_ttl_seconds, enabled) VALUES ($1, $2, 'requests', 'Requests', 'request', 60, true), ($3, $2, 'tokens', 'Tokens', 'token', 60, true)", []any{meterID, appID, meterB}},
		{"INSERT INTO catalog_price_versions (id, meter_id, usd_micros_per_unit, effective_from) VALUES ($1, $2, 1, '2026-01-01'), ($3, $4, 2, '2026-01-01')", []any{priceID, meterID, priceB, meterB}},
		{"INSERT INTO catalog_price_versions (id, meter_id, usd_micros_per_unit, effective_from, effective_until) VALUES ($1, $2, 1, '2025-01-01', '2026-01-01')", []any{stalePrice, meterID}},
		{"INSERT INTO service_identities (id, logto_client_id, name) VALUES ($1, 'schema-service', 'Schema Service'), ($2, 'schema-service-b', 'Schema Service B')", []any{serviceID, serviceB}},
		{"INSERT INTO service_meter_allowlists (service_identity_id, meter_id) VALUES ($1, $2)", []any{serviceID, meterID}},
		{"INSERT INTO polar_meter_mappings (meter_id, polar_meter_id) VALUES ($1, 'schema-meter-a'), ($2, 'schema-meter-b')", []any{meterID, meterB}},
		{"INSERT INTO polar_customers (organization_id, polar_customer_id) VALUES ($1, 'schema-customer-a'), ($2, 'schema-customer-b'), ($3, 'schema-customer-c')", []any{orgA, orgB, orgC}},
		{"INSERT INTO subscriptions (id, organization_id, polar_subscription_id, status) VALUES ($1, $2, 'polar-a', 'active'), ($3, $4, 'polar-b', 'active')", []any{subA, orgA, subB, orgB}},
		{"INSERT INTO billing_periods (id, organization_id, subscription_id, starts_at, ends_at) VALUES ($1, $2, $3, transaction_timestamp() - interval '1 day', transaction_timestamp() + interval '1 day'), ($4, $2, $3, transaction_timestamp() - interval '3 days', transaction_timestamp() - interval '2 days'), ($5, $6, $7, '2026-01-01', '2026-02-01')", []any{periodA, orgA, subA, pastPeriodA, periodB, orgB, subB}},
	}
	for _, item := range setup {
		if _, err := transaction.Exec(ctx, item.statement, item.arguments...); err != nil {
			t.Fatal(err)
		}
	}

	requireConstraintFailure(t, ctx, transaction,
		"INSERT INTO accounts (id, logto_subject) VALUES ('550e8400-e29b-41d4-a716-446655440000', 'not-uuid-v7')",
	)
	requireConstraintFailure(t, ctx, transaction,
		"UPDATE accounts SET logto_subject = 'reassigned-subject' WHERE id = $1",
		accountC,
	)
	requireConstraintFailure(t, ctx, transaction,
		"UPDATE polar_customers SET polar_customer_id = 'rewritten-customer' WHERE organization_id = $1",
		orgA,
	)
	requireConstraintFailure(t, ctx, transaction,
		"UPDATE subscriptions SET polar_subscription_id = 'rewritten-subscription' WHERE id = $1",
		subA,
	)
	requireConstraintFailure(t, ctx, transaction,
		"UPDATE service_identities SET logto_client_id = 'rewritten-service' WHERE id = $1",
		serviceID,
	)
	requireConstraintFailure(t, ctx, transaction,
		"UPDATE organization_memberships SET account_id = $1 WHERE organization_id = $2 AND account_id = $3",
		accountB, orgA, accountC,
	)
	requireConstraintFailure(t, ctx, transaction,
		"UPDATE organization_memberships SET organization_id = $1 WHERE organization_id = $2 AND account_id = $3",
		orgB, orgA, accountC,
	)
	requireConstraintFailure(t, ctx, transaction,
		"UPDATE organization_memberships SET role = 'admin' WHERE organization_id = $1 AND account_id = $2",
		orgA, accountA,
	)
	requireConstraintFailure(t, ctx, transaction,
		"DELETE FROM organization_memberships WHERE organization_id = $1 AND account_id = $2",
		orgA, accountA,
	)
	requireConstraintFailure(t, ctx, transaction,
		"INSERT INTO organization_slug_aliases (slug, organization_id) VALUES ('schema-a', $1)",
		orgB,
	)
	if _, err := transaction.Exec(
		ctx,
		"INSERT INTO organization_slug_aliases (slug, organization_id) VALUES ('schema-reserved', $1)",
		orgA,
	); err != nil {
		t.Fatal(err)
	}
	requireConstraintFailure(t, ctx, transaction,
		"UPDATE organizations SET slug = 'schema-reserved' WHERE id = $1",
		orgB,
	)
	if _, err := transaction.Exec(
		ctx,
		"UPDATE organizations SET slug = 'schema-renamed' WHERE id = $1",
		orgA,
	); err != nil {
		t.Fatal(err)
	}
	var retainedSlugAlias bool
	if err := transaction.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM organization_slug_aliases AS alias
			JOIN organization_slug_registry AS registry USING (slug, organization_id)
			WHERE alias.slug = 'schema-a'
			  AND alias.organization_id = $1
		)
	`, orgA).Scan(&retainedSlugAlias); err != nil || !retainedSlugAlias {
		t.Fatalf("previous organization slug retained = %t, %v", retainedSlugAlias, err)
	}
	requireConstraintFailure(t, ctx, transaction,
		"DELETE FROM organization_slug_aliases WHERE slug = 'schema-a'",
	)
	requireConstraintFailure(t, ctx, transaction,
		"UPDATE organization_slug_aliases SET slug = 'rewritten-alias' WHERE slug = 'schema-a'",
	)
	requireConstraintFailure(t, ctx, transaction,
		"DELETE FROM organization_slug_registry WHERE slug = 'schema-a'",
	)
	requireConstraintFailure(t, ctx, transaction,
		"UPDATE organization_slug_registry SET organization_id = $1 WHERE slug = 'schema-a'",
		orgB,
	)
	if _, err := transaction.Exec(
		ctx,
		"UPDATE organizations SET slug = 'schema-a' WHERE id = $1",
		orgA,
	); err != nil {
		t.Fatal(err)
	}
	requireConstraintFailure(t, ctx, transaction,
		"UPDATE organizations SET slug = 'schema-a' WHERE id = $1",
		orgB,
	)
	requireConstraintFailure(t, ctx, transaction,
		"INSERT INTO team_memberships (organization_id, team_id, account_id, role) VALUES ($1, $2, $3, 'member')",
		orgA, teamA, accountB,
	)
	requireConstraintFailure(t, ctx, transaction,
		"INSERT INTO team_memberships (organization_id, team_id, account_id, role) VALUES ($1, $2, $3, 'member')",
		orgB, teamA, accountB,
	)
	requireConstraintFailure(t, ctx, transaction,
		"INSERT INTO teams (id, organization_id, name) VALUES ('0198a000-0000-7000-8000-000000000026', $1, 'A')",
		orgA,
	)
	requireConstraintFailure(t, ctx, transaction,
		"UPDATE teams SET organization_id = $1 WHERE id = $2",
		orgA, teamB,
	)
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO teams (id, organization_id, parent_team_id, name)
		VALUES (
			'0198a000-0000-7000-8000-000000000027',
			$1,
			'0198a000-0000-7000-8000-000000000027',
			'Self parent'
		)
	`, orgA)
	deferredCycle, err := transaction.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := deferredCycle.Exec(ctx, "SET CONSTRAINTS ALL DEFERRED"); err != nil {
		_ = deferredCycle.Rollback(context.WithoutCancel(ctx))
		t.Fatal(err)
	}
	if _, err := deferredCycle.Exec(ctx, `
		INSERT INTO teams (id, organization_id, parent_team_id, name)
		VALUES (
			'0198a000-0000-7000-8000-000000000111',
			$1,
			'0198a000-0000-7000-8000-000000000112',
			'Deferred cycle A'
		)
	`, orgA); err == nil {
		_ = deferredCycle.Rollback(context.WithoutCancel(ctx))
		t.Fatal("deferred team parent insert unexpectedly succeeded")
	}
	if err := deferredCycle.Rollback(context.WithoutCancel(ctx)); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO teams (id, organization_id, parent_team_id, name) VALUES
			('0198a000-0000-7000-8000-000000000101', $1, NULL, 'Level 1'),
			('0198a000-0000-7000-8000-000000000102', $1, '0198a000-0000-7000-8000-000000000101', 'Level 2'),
			('0198a000-0000-7000-8000-000000000103', $1, '0198a000-0000-7000-8000-000000000102', 'Level 3'),
			('0198a000-0000-7000-8000-000000000104', $1, '0198a000-0000-7000-8000-000000000103', 'Level 4'),
			('0198a000-0000-7000-8000-000000000105', $1, '0198a000-0000-7000-8000-000000000104', 'Level 5'),
			('0198a000-0000-7000-8000-000000000106', $1, NULL, 'Movable root'),
			('0198a000-0000-7000-8000-000000000107', $1, '0198a000-0000-7000-8000-000000000106', 'Movable child')
	`, orgA); err != nil {
		t.Fatal(err)
	}
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO teams (id, organization_id, parent_team_id, name)
		VALUES (
			'0198a000-0000-7000-8000-000000000108',
			$1,
			'0198a000-0000-7000-8000-000000000105',
			'Level 6'
		)
	`, orgA)
	requireConstraintFailure(t, ctx, transaction, `
		UPDATE teams
		SET parent_team_id = '0198a000-0000-7000-8000-000000000105'
		WHERE id = '0198a000-0000-7000-8000-000000000101'
	`)
	requireConstraintFailure(t, ctx, transaction, `
		UPDATE teams
		SET parent_team_id = '0198a000-0000-7000-8000-000000000104'
		WHERE id = '0198a000-0000-7000-8000-000000000106'
	`)
	requireConstraintFailure(t, ctx, transaction,
		"UPDATE teams SET name = 'Renamed' WHERE id = $1",
		generalA,
	)
	requireConstraintFailure(t, ctx, transaction,
		"UPDATE teams SET protected_general = false WHERE id = $1",
		generalA,
	)
	requireConstraintFailure(t, ctx, transaction,
		"DELETE FROM teams WHERE id = $1",
		generalA,
	)
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO organization_invitations (
			id, organization_id, token_hash, organization_role, target_team_id,
			team_role, created_by_account_id, expires_at
		) VALUES ($1, $2, decode(repeat('ab', 32), 'hex'), 'member', $3, 'member', $4, transaction_timestamp() + interval '1 day')
	`, inviteID, orgA, teamB, accountA)
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO organization_invitations (
			id, organization_id, token_hash, organization_role,
			created_by_account_id, expires_at
		) VALUES (
			'0198a000-0000-7000-8000-000000000109',
			$1,
			decode(repeat('cd', 32), 'hex'),
			'admin',
			$2,
			transaction_timestamp() + interval '8 days'
		)
	`, orgA, accountA)
	if _, err := transaction.Exec(ctx, `
		INSERT INTO organization_invitations (
			id, organization_id, token_hash, organization_role,
			created_by_account_id, created_at, expires_at
		) VALUES (
			'0198a000-0000-7000-8000-000000000110',
			$1,
			decode(repeat('ef', 32), 'hex'),
			'admin',
			$2,
			'2026-01-01T00:00:00Z',
			'2026-01-08T00:00:00Z'
		)
	`, orgA, accountA); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO organization_invitations (
			id, organization_id, token_hash, organization_role,
			created_by_account_id, created_at, expires_at
		) VALUES (
			'0198a000-0000-7000-8000-000000000142',
			$1,
			decode(repeat('fa', 32), 'hex'),
			'admin',
			$2,
			transaction_timestamp() + interval '1 day',
			transaction_timestamp() + interval '2 days'
		)
	`, orgA, accountA); err != nil {
		t.Fatal(err)
	}
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO organization_invitation_acceptances (invitation_id, account_id)
		VALUES ('0198a000-0000-7000-8000-000000000142', $1)
	`, accountB)
	if _, err := transaction.Exec(ctx, `
		INSERT INTO organization_invitation_acceptances (
			invitation_id, account_id, accepted_at
		) VALUES ($1, $2, '2000-01-01T00:00:00Z')
	`, deletionInvite, accountA); err != nil {
		t.Fatal(err)
	}
	var acceptanceUsesStorageTime bool
	if err := transaction.QueryRow(ctx, `
		SELECT accepted_at = transaction_timestamp()
		FROM organization_invitation_acceptances
		WHERE invitation_id = $1 AND account_id = $2
	`, deletionInvite, accountA).Scan(&acceptanceUsesStorageTime); err != nil ||
		!acceptanceUsesStorageTime {
		t.Fatalf("invitation acceptance storage timestamp = %t, %v", acceptanceUsesStorageTime, err)
	}
	if _, err := transaction.Exec(
		ctx,
		"UPDATE organization_invitations SET revoked_at = transaction_timestamp() WHERE id = $1",
		deletionInvite,
	); err != nil {
		t.Fatal(err)
	}
	requireConstraintFailure(t, ctx, transaction, `
		UPDATE organization_invitations
		SET revoked_at = NULL
		WHERE id = $1
	`, deletionInvite)
	requireConstraintFailure(t, ctx, transaction, `
		UPDATE organization_invitations
		SET expires_at = expires_at + interval '1 hour'
		WHERE id = $1
	`, deletionInvite)
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO organization_invitation_acceptances (invitation_id, account_id)
		VALUES ($1, $2)
	`, deletionInvite, accountB)
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO organization_invitation_acceptances (invitation_id, account_id)
		VALUES ('0198a000-0000-7000-8000-000000000110', $1)
	`, accountB)
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO catalog_price_versions (
			id, meter_id, usd_micros_per_unit, effective_from, effective_until
		) VALUES (
			'0198a000-0000-7000-8000-000000000037', $1, 1,
			'2026-01-15', '2026-02-15'
		)
	`, meterID)
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO billing_periods (
			id, organization_id, subscription_id, starts_at, ends_at
		) VALUES (
			'0198a000-0000-7000-8000-000000000073', $1, $2,
			transaction_timestamp() - interval '12 hours',
			transaction_timestamp() + interval '2 days'
		)
	`, orgA, subA)
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO billing_periods (
			id, organization_id, subscription_id, starts_at, ends_at
		) VALUES (
			'0198a000-0000-7000-8000-000000000074', $1, $2,
			'2026-02-01', '2026-03-01'
		)
	`, orgA, subB)
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO ledger_entries (
			id, organization_id, billing_period_id, entry_type,
			amount_micros, balance_after_micros, source_reference
		) VALUES (
			'0198a000-0000-7000-8000-000000000075', $1, $2,
			'credit_grant', 1, 1, 'cross-period'
		)
	`, orgA, periodB)
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO ledger_entries (
			id, organization_id, billing_period_id, entry_type,
			amount_micros, balance_after_micros, source_reference
		) VALUES (
			'0198a000-0000-7000-8000-000000000076', $1, $2,
			'adjustment', 1, 1, 'unsupported-operation'
		)
	`, orgA, periodA)
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO ledger_entries (
			id, organization_id, billing_period_id, entry_type,
			amount_micros, balance_after_micros, source_reference,
			actor_reference
		) VALUES (
			'0198a000-0000-7000-8000-000000000131', $1, $2,
			'credit_grant', 1, 1, 'raw-ledger-actor', 'raw-logto-subject'
		)
	`, orgA, periodA)
	ledgerOperations := []struct {
		name   string
		amount int64
	}{
		{name: "credit_grant", amount: 6},
		{name: "credit_reversal", amount: -1},
		{name: "credit_hold", amount: -2},
		{name: "credit_release", amount: 4},
		{name: "overage_hold", amount: -1},
		{name: "overage_release", amount: 4},
		{name: "credit_forfeiture", amount: -1},
	}
	var ledgerBalance int64
	for index, operation := range ledgerOperations {
		ledgerBalance += operation.amount
		if _, err := transaction.Exec(ctx, `
			INSERT INTO ledger_entries (
				id, organization_id, billing_period_id, entry_type,
				amount_micros, balance_after_micros, source_reference
			) VALUES (
				('0198a000-0000-7000-8000-' || lpad(($1 + 80)::text, 12, '0'))::uuid,
				$2, $3, $4, $5, $6, $4
			)
		`, index, orgA, periodA, operation.name, operation.amount, ledgerBalance); err != nil {
			t.Fatal(err)
		}
	}
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO ledger_entries (
			id, organization_id, billing_period_id, entry_type,
			amount_micros, balance_after_micros, source_reference
		) VALUES (
			'0198a000-0000-7000-8000-000000000136', $1, $2,
			'credit_commit', -1, 8, 'unlinked-credit-commit'
		)
	`, orgA, periodA)
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO ledger_entries (
			id, organization_id, billing_period_id, entry_type,
			amount_micros, balance_after_micros, source_reference
		) VALUES (
			'0198a000-0000-7000-8000-000000000137', $1, $2,
			'overage_commit', -1, 8, 'unlinked-overage-commit'
		)
	`, orgA, periodA)
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO ledger_entries (
			id, organization_id, billing_period_id, entry_type,
			amount_micros, balance_after_micros, source_reference
		) VALUES (
			'0198a000-0000-7000-8000-000000000143', $1, $2,
			'credit_commit', 1, 10, 'positive-credit-commit'
		)
	`, orgA, periodA)
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO ledger_entries (
			id, organization_id, billing_period_id, entry_type,
			amount_micros, balance_after_micros, source_reference
		) VALUES (
			'0198a000-0000-7000-8000-000000000144', $1, $2,
			'credit_forfeiture', 1, 10, 'positive-credit-forfeiture'
		)
	`, orgA, periodA)
	if _, err := transaction.Exec(ctx, `
		INSERT INTO ledger_entries (
			id, organization_id, billing_period_id, entry_type,
			amount_micros, balance_after_micros, source_reference
		) VALUES (
			'0198a000-0000-7000-8000-000000000124', $1, $2,
			'credit_grant', 1, 1, 'deleted-organization-credit'
		)
	`, orgB, periodB); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(
		ctx,
		"UPDATE organizations SET deleted_at = transaction_timestamp() WHERE id = $1",
		orgB,
	); err != nil {
		t.Fatal(err)
	}
	requireConstraintFailure(t, ctx, transaction,
		"UPDATE organizations SET deleted_at = NULL WHERE id = $1",
		orgB,
	)
	requireConstraintFailure(t, ctx, transaction,
		"UPDATE organizations SET deleted_at = deleted_at + interval '1 second' WHERE id = $1",
		orgB,
	)
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO usage_reservations (
			id, organization_id, team_id, team_name_snapshot, meter_id,
			price_version_id, account_id, service_identity_id, maximum_units,
			usd_micros_per_unit, maximum_cost_micros, held_credit_micros,
			held_overage_micros, client_reference, expires_at
		) VALUES (
			'0198a000-0000-7000-8000-000000000125', $1, $2, 'B', $3,
			$4, $5, $6, 1, 1, 1, 1, 0, 'deleting-organization',
			transaction_timestamp() + interval '1 minute'
		)
	`, orgB, teamB, meterID, priceID, accountB, serviceID)
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO ledger_entries (
			id, organization_id, billing_period_id, entry_type,
			amount_micros, balance_after_micros, source_reference
		) VALUES (
			'0198a000-0000-7000-8000-000000000090', $1, $2,
			'credit_grant', 1, 999, 'invalid-running-balance'
		)
	`, orgA, periodA)
	requireConstraintFailure(t, ctx, transaction, `
		UPDATE ledger_entries
		SET amount_micros = 2
		WHERE id = '0198a000-0000-7000-8000-000000000080'
	`)
	requireConstraintFailure(t, ctx, transaction, `
		DELETE FROM ledger_entries
		WHERE id = '0198a000-0000-7000-8000-000000000080'
	`)
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO usage_reservations (
			id, organization_id, team_id, team_name_snapshot, meter_id,
			price_version_id, account_id, service_identity_id, maximum_units,
			usd_micros_per_unit, maximum_cost_micros, held_credit_micros,
			held_overage_micros, client_reference, expires_at
		) VALUES (
			'0198a000-0000-7000-8000-000000000120', $1, $2, 'A', $3,
			$4, $5, $6, 5, 1, 5, 5, 0, 'unfunded-credit',
			transaction_timestamp() + interval '1 minute'
		)
	`, orgA, teamA, meterID, priceID, accountA, serviceID)
	if _, err := transaction.Exec(ctx, `
		INSERT INTO billing_periods (
			id, organization_id, subscription_id, starts_at, ends_at,
			overage_limit_micros
		) VALUES (
			'0198a000-0000-7000-8000-000000000121',
			$1, $2,
			transaction_timestamp() - interval '1 day',
			transaction_timestamp() + interval '1 day',
			10
		)
		`, orgA, subA); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(ctx, `
		UPDATE subscriptions
		SET current_period_starts_at = transaction_timestamp() - interval '1 day',
		    current_period_ends_at = transaction_timestamp() + interval '1 day'
		WHERE id = $1
	`, subA); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(ctx, `
		UPDATE subscriptions
		SET current_period_ends_at = transaction_timestamp() + interval '2 days'
		WHERE id = $1
	`, subA); err != nil {
		t.Fatal(err)
	}
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO usage_reservations (
			id, organization_id, team_id, team_name_snapshot, meter_id,
			price_version_id, account_id, service_identity_id, maximum_units,
			usd_micros_per_unit, maximum_cost_micros, held_credit_micros,
			held_overage_micros, client_reference, expires_at
		) VALUES (
			'0198a000-0000-7000-8000-000000000138', $1, $2, 'A', $3,
			$4, $5, $6, 5, 1, 5, 4, 1, 'mismatched-current-period',
			transaction_timestamp() + interval '1 minute'
		)
	`, orgA, teamA, meterID, priceID, accountA, serviceID)
	if _, err := transaction.Exec(ctx, `
		UPDATE subscriptions
		SET current_period_ends_at = transaction_timestamp() + interval '1 day'
		WHERE id = $1
	`, subA); err != nil {
		t.Fatal(err)
	}
	requireConstraintFailure(t, ctx, transaction, `
		WITH shortened_period AS (
			UPDATE billing_periods
			SET ends_at = transaction_timestamp() + interval '30 seconds'
			WHERE id = '0198a000-0000-7000-8000-000000000121'
			RETURNING id
		), shortened_subscription AS (
			UPDATE subscriptions
			SET current_period_ends_at = transaction_timestamp() + interval '30 seconds'
			WHERE id = $7
			RETURNING id
		)
		INSERT INTO usage_reservations (
			id, organization_id, team_id, team_name_snapshot, meter_id,
			price_version_id, account_id, service_identity_id, maximum_units,
			usd_micros_per_unit, maximum_cost_micros, held_credit_micros,
			held_overage_micros, client_reference, expires_at
		)
		SELECT
			'0198a000-0000-7000-8000-000000000145', $1, $2, 'A', $3,
			$4, $5, $6, 5, 1, 5, 4, 1, 'period-rollover',
			transaction_timestamp() + interval '1 minute'
		FROM shortened_period, shortened_subscription
	`, orgA, teamA, meterID, priceID, accountA, serviceID, subA)
	if _, err := transaction.Exec(
		ctx,
		"UPDATE subscriptions SET status = 'past_due' WHERE id = $1",
		subA,
	); err != nil {
		t.Fatal(err)
	}
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO usage_reservations (
			id, organization_id, team_id, team_name_snapshot, meter_id,
			price_version_id, account_id, service_identity_id, maximum_units,
			usd_micros_per_unit, maximum_cost_micros, held_credit_micros,
			held_overage_micros, client_reference, expires_at
		) VALUES (
			'0198a000-0000-7000-8000-000000000122', $1, $2, 'A', $3,
			$4, $5, $6, 5, 1, 5, 4, 1, 'inactive-subscription',
			transaction_timestamp() + interval '1 minute'
		)
	`, orgA, teamA, meterID, priceID, accountA, serviceID)
	if _, err := transaction.Exec(
		ctx,
		"UPDATE subscriptions SET status = 'active' WHERE id = $1",
		subA,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(ctx, `
		UPDATE billing_periods SET overage_limit_micros = 0
		WHERE id = '0198a000-0000-7000-8000-000000000121'
	`); err != nil {
		t.Fatal(err)
	}
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO usage_reservations (
			id, organization_id, team_id, team_name_snapshot, meter_id,
			price_version_id, account_id, service_identity_id, maximum_units,
			usd_micros_per_unit, maximum_cost_micros, held_credit_micros,
			held_overage_micros, client_reference, expires_at
		) VALUES (
			'0198a000-0000-7000-8000-000000000123', $1, $2, 'A', $3,
			$4, $5, $6, 5, 1, 5, 4, 1, 'over-limit',
			transaction_timestamp() + interval '1 minute'
		)
	`, orgA, teamA, meterID, priceID, accountA, serviceID)
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO usage_reservations (
			id, organization_id, team_id, team_name_snapshot, meter_id,
			price_version_id, account_id, service_identity_id, maximum_units,
			usd_micros_per_unit, maximum_cost_micros, held_credit_micros,
			held_overage_micros, client_reference, expires_at
		) VALUES ($1, $2, $3, 'B', $4, $5, $6, $7, 1, 1, 1, 1, 0, 'cross-team', transaction_timestamp() + interval '1 minute')
	`, reserveID, orgA, teamB, meterID, priceID, accountA, serviceID)
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO usage_reservations (
			id, organization_id, team_id, team_name_snapshot, meter_id,
			price_version_id, account_id, service_identity_id, maximum_units,
			usd_micros_per_unit, maximum_cost_micros, held_credit_micros,
			held_overage_micros, client_reference, status, expires_at, finalized_at
		) VALUES (
			'0198a000-0000-7000-8000-000000000049', $1, $2, 'B', $3,
			$4, $5, $6, 1, 1, 1, 1, 0, 'finalized-cross-team', 'released',
			transaction_timestamp() + interval '1 minute', transaction_timestamp()
		)
	`, orgA, teamB, meterID, priceID, accountA, serviceID)
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO usage_reservations (
			id, organization_id, team_id, team_name_snapshot, meter_id,
			price_version_id, account_id, service_identity_id, maximum_units,
			usd_micros_per_unit, maximum_cost_micros, held_credit_micros,
			held_overage_micros, client_reference, status, expires_at, finalized_at
		) VALUES (
			'0198a000-0000-7000-8000-000000000126', $1, $2, 'A', $3,
			$4, $5, $6, 1, 1, 1, 1, 0, 'direct-committed', 'committed',
			transaction_timestamp() + interval '1 minute', transaction_timestamp()
		)
	`, orgA, teamA, meterID, priceID, historyUser, serviceID)
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO usage_reservations (
			id, organization_id, team_id, team_name_snapshot, meter_id,
			price_version_id, account_id, service_identity_id, maximum_units,
			usd_micros_per_unit, maximum_cost_micros, held_credit_micros,
			held_overage_micros, client_reference, expires_at
		) VALUES (
			'0198a000-0000-7000-8000-000000000045', $1, $2, 'A', $3,
			$4, $5, $6, 1, 2, 2, 2, 0, 'cross-meter',
			transaction_timestamp() + interval '1 minute'
		)
	`, orgA, teamA, meterID, priceB, accountA, serviceID)
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO usage_reservations (
			id, organization_id, team_id, team_name_snapshot, meter_id,
			price_version_id, account_id, service_identity_id, maximum_units,
			usd_micros_per_unit, maximum_cost_micros, held_credit_micros,
			held_overage_micros, client_reference, expires_at
		) VALUES (
			'0198a000-0000-7000-8000-000000000046', $1, $2, 'A', $3,
			$4, $5, $6, 1, 0, 0, 0, 0, 'underpriced',
			transaction_timestamp() + interval '1 minute'
		)
	`, orgA, teamA, meterID, priceID, accountA, serviceID)
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO usage_reservations (
			id, organization_id, team_id, team_name_snapshot, meter_id,
			price_version_id, account_id, service_identity_id, maximum_units,
			usd_micros_per_unit, maximum_cost_micros, held_credit_micros,
			held_overage_micros, client_reference, expires_at
		) VALUES (
			'0198a000-0000-7000-8000-000000000116', $1, $2, 'A', $3,
			$4, $5, $6, 1, 1, 1, 1, 0, 'stale-price',
			transaction_timestamp() + interval '1 minute'
		)
	`, orgA, teamA, meterID, stalePrice, accountA, serviceID)
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO usage_reservations (
			id, organization_id, team_id, team_name_snapshot, meter_id,
			price_version_id, account_id, service_identity_id, maximum_units,
			usd_micros_per_unit, maximum_cost_micros, held_credit_micros,
			held_overage_micros, client_reference, expires_at
		) VALUES (
			'0198a000-0000-7000-8000-000000000117', $1, $2, 'Wrong team', $3,
			$4, $5, $6, 1, 1, 1, 1, 0, 'wrong-team-snapshot',
			transaction_timestamp() + interval '1 minute'
		)
	`, orgA, teamA, meterID, priceID, accountA, serviceID)
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO usage_reservations (
			id, organization_id, team_id, team_name_snapshot, meter_id,
			price_version_id, account_id, service_identity_id, maximum_units,
			usd_micros_per_unit, maximum_cost_micros, held_credit_micros,
			held_overage_micros, client_reference, expires_at
		) VALUES (
			'0198a000-0000-7000-8000-000000000118', $1, $2, 'A', $3,
			$4, $5, $6, 1, 1, 1, 1, 0, 'wrong-expiry',
			transaction_timestamp() + interval '2 minutes'
		)
	`, orgA, teamA, meterID, priceID, accountA, serviceID)
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO usage_reservations (
			id, organization_id, team_id, team_name_snapshot, meter_id,
			price_version_id, account_id, service_identity_id, maximum_units,
			usd_micros_per_unit, maximum_cost_micros, held_credit_micros,
			held_overage_micros, client_reference, expires_at
		) VALUES (
			'0198a000-0000-7000-8000-000000000119', $1, $2, 'A', $3,
			$4, $5, $6, 1, 1, 1, 1, 0, 'non-member-actor',
			transaction_timestamp() + interval '1 minute'
		)
	`, orgA, teamA, meterID, priceID, accountB, serviceID)
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO usage_reservations (
			id, organization_id, team_id, team_name_snapshot, meter_id,
			price_version_id, account_id, service_identity_id, maximum_units,
			usd_micros_per_unit, maximum_cost_micros, held_credit_micros,
			held_overage_micros, client_reference, expires_at
		) VALUES (
			'0198a000-0000-7000-8000-000000000205', $1, $2, 'A', $3,
			$4, $5, $6, 1, 1, 1, 1, 0, 'inaccessible-team',
			transaction_timestamp() + interval '1 minute'
		)
		`, orgA, teamA, meterID, priceID, accountC, serviceID)
	if _, err := transaction.Exec(
		ctx,
		"UPDATE accounts SET status = 'disabled' WHERE id = $1",
		historyUser,
	); err != nil {
		t.Fatal(err)
	}
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO usage_reservations (
			id, organization_id, team_id, team_name_snapshot, meter_id,
			price_version_id, account_id, service_identity_id, maximum_units,
			usd_micros_per_unit, maximum_cost_micros, held_credit_micros,
			held_overage_micros, client_reference, expires_at
		) VALUES (
			'0198a000-0000-7000-8000-000000000139', $1, $2, 'A', $3,
			$4, $5, $6, 1, 1, 1, 1, 0, 'disabled-account',
			transaction_timestamp() + interval '1 minute'
		)
	`, orgA, teamA, meterID, priceID, historyUser, serviceID)
	if _, err := transaction.Exec(
		ctx,
		"UPDATE accounts SET status = 'active' WHERE id = $1",
		historyUser,
	); err != nil {
		t.Fatal(err)
	}
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO usage_reservations (
			id, organization_id, team_id, team_name_snapshot, meter_id,
			price_version_id, account_id, service_identity_id, maximum_units,
			usd_micros_per_unit, maximum_cost_micros, held_credit_micros,
			held_overage_micros, client_reference, expires_at
		) VALUES (
			'0198a000-0000-7000-8000-000000000050', $1, $2, 'A', $3,
			$4, $5, $6, 1, 1, 1, 1, 0, 'disallowed-service',
			transaction_timestamp() + interval '1 minute'
		)
	`, orgA, teamA, meterID, priceID, accountA, serviceB)
	if _, err := transaction.Exec(ctx, `
		INSERT INTO usage_reservations (
			id, organization_id, team_id, team_name_snapshot, meter_id,
			price_version_id, account_id, service_identity_id, maximum_units,
			usd_micros_per_unit, maximum_cost_micros, held_credit_micros,
			held_overage_micros, client_reference, expires_at
		) VALUES (
			$1, $2, $3, 'Inherited', $4, $5, $6, $7,
			1, 1, 1, 1, 0, 'inherited-team-access',
			transaction_timestamp() + interval '1 minute'
		)
	`, inheritedHold, orgA, inheritedTeam, meterID, priceID, historyUser, serviceID); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(ctx, `
		UPDATE usage_reservations
		SET status = 'released',
		    finalized_at = transaction_timestamp()
		WHERE id = $1
	`, inheritedHold); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO usage_reservations (
			id, organization_id, team_id, team_name_snapshot, meter_id,
			price_version_id, account_id, service_identity_id, maximum_units,
			usd_micros_per_unit, maximum_cost_micros, held_credit_micros,
			held_overage_micros, client_reference, created_at, expires_at
		) VALUES ($1, $2, $3, 'A', $4, $5, $6, $7, 1, 1, 1, 1, 0, 'valid', '2099-01-01', transaction_timestamp() + interval '1 minute')
	`, reserveID, orgA, teamA, meterID, priceID, historyUser, serviceID); err != nil {
		t.Fatal(err)
	}
	var reservationUsesStorageTime bool
	if err := transaction.QueryRow(
		ctx,
		"SELECT created_at = transaction_timestamp() FROM usage_reservations WHERE id = $1",
		reserveID,
	).Scan(&reservationUsesStorageTime); err != nil || !reservationUsesStorageTime {
		t.Fatalf("reservation storage creation timestamp = %t, %v", reservationUsesStorageTime, err)
	}
	if _, err := transaction.Exec(
		ctx,
		"UPDATE accounts SET status = 'disabled' WHERE id = $1",
		historyUser,
	); err != nil {
		t.Fatal(err)
	}
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO usage_records (
			id, reservation_id, organization_id, team_id, team_name_snapshot,
			meter_id, account_id, service_identity_id, committed_units,
			total_cost_micros, credit_applied_micros, overage_applied_micros
		) VALUES (
			'0198a000-0000-7000-8000-000000000214',
			$1, $2, $3, 'A', $4, $5, $6, 1, 1, 1, 0
		)
	`, reserveID, orgA, teamA, meterID, historyUser, serviceID)
	if _, err := transaction.Exec(
		ctx,
		"UPDATE accounts SET status = 'active' WHERE id = $1",
		historyUser,
	); err != nil {
		t.Fatal(err)
	}
	requireConstraintFailure(t, ctx, transaction, `
		WITH removed_access AS (
			DELETE FROM team_memberships
			WHERE organization_id = $3
			  AND team_id = $4
			  AND account_id = $6
			RETURNING organization_id
		)
		INSERT INTO usage_records (
			id, reservation_id, organization_id, team_id, team_name_snapshot,
			meter_id, account_id, service_identity_id, committed_units,
			total_cost_micros, credit_applied_micros, overage_applied_micros
		)
		SELECT
			$1, $2, removed_access.organization_id, $4, 'A',
			$5, $6, $7, 1, 1, 1, 0
		FROM removed_access
	`, recordID, reserveID, orgA, teamA, meterID, historyUser, serviceID)
	requireConstraintFailure(t, ctx, transaction, `
		WITH started_deletion AS (
			UPDATE organizations
			SET deleted_at = transaction_timestamp()
			WHERE id = $3
			RETURNING id
		)
		INSERT INTO usage_records (
			id, reservation_id, organization_id, team_id, team_name_snapshot,
			meter_id, account_id, service_identity_id, committed_units,
			total_cost_micros, credit_applied_micros, overage_applied_micros
		)
		SELECT
			$1, $2, started_deletion.id, $4, 'A',
			$5, $6, $7, 1, 1, 1, 0
		FROM started_deletion
	`, recordID, reserveID, orgA, teamA, meterID, historyUser, serviceID)
	requireConstraintFailure(t, ctx, transaction,
		"DELETE FROM usage_reservations WHERE id = $1",
		reserveID,
	)
	requireConstraintFailure(t, ctx, transaction, `
		UPDATE usage_reservations
		SET status = 'committed',
		    finalized_at = transaction_timestamp()
		WHERE id = $1
	`, reserveID)
	requireConstraintFailure(t, ctx, transaction, `
		UPDATE usage_reservations
		SET status = 'expired',
		    finalized_at = transaction_timestamp()
		WHERE id = $1
	`, reserveID)
	if _, err := transaction.Exec(ctx, `
		INSERT INTO usage_reservations (
			id, organization_id, team_id, team_name_snapshot, meter_id,
			price_version_id, account_id, service_identity_id, maximum_units,
			usd_micros_per_unit, maximum_cost_micros, held_credit_micros,
			held_overage_micros, client_reference, expires_at
		) VALUES ($1, $2, $3, 'Active', $4, $5, $6, $7, 1, 1, 1, 1, 0, 'active', transaction_timestamp() + interval '1 minute')
	`, activeHold, orgA, activeTeam, meterID, priceID, accountA, serviceID); err != nil {
		t.Fatal(err)
	}
	requireConstraintFailure(t, ctx, transaction,
		"DELETE FROM polar_meter_mappings WHERE meter_id = $1",
		meterID,
	)
	requireConstraintFailure(t, ctx, transaction,
		"UPDATE polar_meter_mappings SET polar_meter_id = 'rewritten-meter' WHERE meter_id = $1",
		meterID,
	)
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO usage_reservations (
			id, organization_id, team_id, team_name_snapshot, meter_id,
			price_version_id, account_id, service_identity_id, maximum_units,
			usd_micros_per_unit, maximum_cost_micros, held_credit_micros,
			held_overage_micros, client_reference, status, expires_at, finalized_at
		) VALUES ($1, $2, $3, 'A', $4, $5, $6, $7, 1, 1, 1, 1, 0, 'released', 'released', transaction_timestamp() + interval '1 minute', transaction_timestamp())
	`, releasedHold, orgA, teamA, meterID, priceID, historyUser, serviceID)
	if _, err := transaction.Exec(ctx, `
		INSERT INTO usage_reservations (
			id, organization_id, team_id, team_name_snapshot, meter_id,
			price_version_id, account_id, service_identity_id, maximum_units,
			usd_micros_per_unit, maximum_cost_micros, held_credit_micros,
			held_overage_micros, client_reference, expires_at
		) VALUES ($1, $2, $3, 'A', $4, $5, $6, $7, 1, 1, 1, 1, 0, 'released', transaction_timestamp() + interval '1 minute')
	`, releasedHold, orgA, teamA, meterID, priceID, historyUser, serviceID); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(ctx, `
		UPDATE usage_reservations
		SET status = 'released'
		WHERE id = $1
	`, releasedHold); err != nil {
		t.Fatal(err)
	}
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO usage_reservations (
			id, organization_id, team_id, team_name_snapshot, meter_id,
			price_version_id, account_id, service_identity_id, maximum_units,
			usd_micros_per_unit, maximum_cost_micros, held_credit_micros,
			held_overage_micros, client_reference, status, created_at, expires_at,
			finalized_at
		) VALUES (
			$1, $2, $3, 'A', $4, $5, $6, $7, 1, 1, 1, 1, 0, 'expired',
			'expired',
			transaction_timestamp() - interval '2 minutes',
			transaction_timestamp() - interval '1 minute',
			transaction_timestamp() - interval '1 minute'
		)
	`, expiredHold, orgA, teamA, meterID, priceID, historyUser, serviceID)
	requireConstraintFailure(t, ctx, transaction, `
		UPDATE usage_reservations
		SET client_reference = 'rewritten'
		WHERE id = $1
	`, reserveID)
	requireConstraintFailure(t, ctx, transaction, `
		UPDATE usage_reservations
		SET status = 'held', finalized_at = NULL
		WHERE id = $1
	`, releasedHold)
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO usage_records (
			id, reservation_id, organization_id, team_id, team_name_snapshot,
			meter_id, account_id, service_identity_id, committed_units,
			total_cost_micros, credit_applied_micros, overage_applied_micros
		) VALUES (
			$1, $2, $3, $4, 'Wrong team', $5, $6, $7, 1, 1, 1, 0
		)
	`, recordID, reserveID, orgA, teamA, meterID, historyUser, serviceID)
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO usage_records (
			id, reservation_id, organization_id, team_id, team_name_snapshot,
			meter_id, account_id, service_identity_id, committed_units,
			total_cost_micros, credit_applied_micros, overage_applied_micros
		) VALUES ($1, $2, $3, $4, 'A', $5, $6, $7, 1, 1, 0, 1)
	`, recordID, reserveID, orgA, teamA, meterID, historyUser, serviceID)
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO usage_records (
			id, reservation_id, organization_id, team_id, team_name_snapshot,
			meter_id, account_id, service_identity_id, committed_units,
			total_cost_micros, credit_applied_micros, overage_applied_micros
		) VALUES ($1, $2, $3, $4, 'B', $5, $6, $7, 1, 1, 1, 0)
	`, recordID, reserveID, orgB, teamB, meterID, accountA, serviceID)
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO usage_records (
			id, reservation_id, organization_id, team_id, team_name_snapshot,
			meter_id, account_id, service_identity_id, committed_units,
			total_cost_micros, credit_applied_micros, overage_applied_micros
		) VALUES ($1, $2, $3, $4, 'A', $5, $6, $7, 1, 1, 1, 0)
	`, recordID, reserveID, orgA, teamA, meterB, historyUser, serviceID)
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO usage_records (
			id, reservation_id, organization_id, team_id, team_name_snapshot,
			meter_id, account_id, service_identity_id, committed_units,
			total_cost_micros, credit_applied_micros, overage_applied_micros
		) VALUES ($1, $2, $3, $4, 'A', $5, $6, $7, 1, 1, 1, 0)
	`, recordID, reserveID, orgA, teamA, meterID, accountA, serviceID)
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO usage_records (
			id, reservation_id, organization_id, team_id, team_name_snapshot,
			meter_id, account_id, service_identity_id, committed_units,
			total_cost_micros, credit_applied_micros, overage_applied_micros
		) VALUES ($1, $2, $3, $4, 'A', $5, $6, $7, 1, 1, 1, 0)
	`, recordID, reserveID, orgA, teamA, meterID, historyUser, serviceB)
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO usage_records (
			id, reservation_id, organization_id, team_id, team_name_snapshot,
			meter_id, account_id, service_identity_id, committed_units,
			total_cost_micros, credit_applied_micros, overage_applied_micros
		) VALUES ($1, $2, $3, $4, 'A', $5, $6, $7, 2, 2, 2, 0)
	`, recordID, reserveID, orgA, teamA, meterID, historyUser, serviceID)
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO usage_records (
			id, reservation_id, organization_id, team_id, team_name_snapshot,
			meter_id, account_id, service_identity_id, committed_units,
			total_cost_micros, credit_applied_micros, overage_applied_micros
		) VALUES ($1, $2, $3, $4, 'A', $5, $6, $7, 1, 0, 0, 0)
	`, recordID, reserveID, orgA, teamA, meterID, historyUser, serviceID)
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO usage_records (
			id, reservation_id, organization_id, team_id, team_name_snapshot,
			meter_id, account_id, service_identity_id, committed_units,
			total_cost_micros, credit_applied_micros, overage_applied_micros
		) VALUES ($1, $2, $3, $4, 'A', $5, $6, $7, 1, 1, 1, 0)
	`, recordID, releasedHold, orgA, teamA, meterID, historyUser, serviceID)
	unsettledUsage, err := transaction.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := unsettledUsage.Exec(ctx, `
		INSERT INTO usage_records (
			id, reservation_id, organization_id, team_id, team_name_snapshot,
			meter_id, account_id, service_identity_id, committed_units,
			total_cost_micros, credit_applied_micros, overage_applied_micros
		) VALUES ($1, $2, $3, $4, 'A', $5, $6, $7, 1, 1, 1, 0)
	`, recordID, reserveID, orgA, teamA, meterID, historyUser, serviceID); err != nil {
		t.Fatal(err)
	}
	if _, err := unsettledUsage.Exec(
		ctx,
		"SET CONSTRAINTS usage_records_require_ledger_settlement IMMEDIATE",
	); err == nil {
		_ = unsettledUsage.Rollback(context.WithoutCancel(ctx))
		t.Fatal("usage record without ledger settlement satisfied deferred constraints")
	}
	if err := unsettledUsage.Rollback(context.WithoutCancel(ctx)); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO usage_records (
			id, reservation_id, organization_id, team_id, team_name_snapshot,
			meter_id, account_id, service_identity_id, committed_units,
			total_cost_micros, credit_applied_micros, overage_applied_micros,
			committed_at
		) VALUES (
			$1, $2, $3, $4, 'A', $5, $6, $7, 1, 1, 1, 0,
			'2000-01-01T00:00:00Z'
		)
	`, recordID, reserveID, orgA, teamA, meterID, historyUser, serviceID); err != nil {
		t.Fatal(err)
	}
	var (
		reservationStatus  string
		storageCommittedAt bool
	)
	if err := transaction.QueryRow(
		ctx,
		`SELECT
			reservation.status,
			usage.committed_at = transaction_timestamp()
		FROM usage_reservations AS reservation
		JOIN usage_records AS usage ON usage.reservation_id = reservation.id
		WHERE reservation.id = $1`,
		reserveID,
	).Scan(&reservationStatus, &storageCommittedAt); err != nil ||
		reservationStatus != "held" || !storageCommittedAt {
		t.Fatalf(
			"reservation status = %q, storage commit timestamp = %t, %v",
			reservationStatus,
			storageCommittedAt,
			err,
		)
	}
	requireConstraintFailure(t, ctx, transaction, `
		UPDATE usage_reservations
		SET status = 'committed'
		WHERE id = $1
	`, reserveID)
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO ledger_entries (
			id, organization_id, billing_period_id, entry_type,
			amount_micros, balance_after_micros, reservation_id,
			usage_record_id, team_id_snapshot, team_name_snapshot,
			source_reference
		) VALUES (
			'0198a000-0000-7000-8000-000000000222',
			$1, $2, 'credit_commit', -1, 8, $3, $4, $5, 'A',
			'wrong-usage-period'
		)
	`, orgA, pastPeriodA, reserveID, recordID, teamA)
	if _, err := transaction.Exec(ctx, `
		INSERT INTO ledger_entries (
			id, organization_id, billing_period_id, entry_type,
			amount_micros, balance_after_micros, reservation_id,
			usage_record_id, team_id_snapshot, team_name_snapshot,
			source_reference, actor_reference
		) VALUES (
			$1, $2, $3, 'credit_commit', -1, 8, $4, $5, $6, 'A',
			'linked-usage', 'actor:v1:00000000000000000000000000000000'
		)
	`, linkedLedger, orgA, periodA, reserveID, recordID, teamA); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(ctx, `
		UPDATE usage_reservations
		SET status = 'committed'
		WHERE id = $1
	`, reserveID); err != nil {
		t.Fatal(err)
	}
	if err := transaction.QueryRow(
		ctx,
		"SELECT status FROM usage_reservations WHERE id = $1",
		reserveID,
	).Scan(&reservationStatus); err != nil || reservationStatus != "committed" {
		t.Fatalf("settled reservation status = %q, %v", reservationStatus, err)
	}
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO ledger_entries (
			id, organization_id, entry_type, amount_micros,
			balance_after_micros, reservation_id, usage_record_id,
			team_id_snapshot, team_name_snapshot, source_reference
		) VALUES (
			'0198a000-0000-7000-8000-000000000211',
			$1, 'credit_commit', -1, 7, $2, $3, $4, 'A',
			'excess-linked-usage'
		)
	`, orgA, reserveID, recordID, teamA)
	if _, err := transaction.Exec(
		ctx,
		"SET CONSTRAINTS usage_records_require_ledger_settlement IMMEDIATE",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(
		ctx,
		"SET CONSTRAINTS usage_records_require_ledger_settlement DEFERRED",
	); err != nil {
		t.Fatal(err)
	}
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO ledger_entries (
			id, organization_id, entry_type, amount_micros,
			balance_after_micros, reservation_id, usage_record_id,
			team_id_snapshot, team_name_snapshot, source_reference
		) VALUES (
			'0198a000-0000-7000-8000-000000000079',
			$1, 'credit_commit', -1, 7, $2, $3, $4, 'Wrong team',
			'mismatched-linked-usage'
		)
	`, orgA, reserveID, recordID, teamA)
	requireConstraintFailure(t, ctx, transaction,
		"UPDATE usage_records SET total_cost_micros = 0 WHERE id = $1",
		recordID,
	)
	requireConstraintFailure(t, ctx, transaction, "DELETE FROM teams WHERE id = $1", activeTeam)
	if _, err := transaction.Exec(ctx, "DELETE FROM teams WHERE id = $1", teamA); err != nil {
		t.Fatal(err)
	}
	var retainedTeamID string
	if err := transaction.QueryRow(
		ctx,
		"SELECT team_id::text FROM usage_records WHERE id = $1",
		recordID,
	).Scan(&retainedTeamID); err != nil || retainedTeamID != teamA {
		t.Fatalf("retained historical team = %q, %v", retainedTeamID, err)
	}
	if _, err := transaction.Exec(ctx, "DELETE FROM accounts WHERE id = $1", historyUser); err != nil {
		t.Fatal(err)
	}
	var retainedReservationAccount, retainedUsageAccount string
	if err := transaction.QueryRow(
		ctx,
		"SELECT account_id::text FROM usage_reservations WHERE id = $1",
		reserveID,
	).Scan(&retainedReservationAccount); err != nil || retainedReservationAccount != historyUser {
		t.Fatalf("retained reservation account = %q, %v", retainedReservationAccount, err)
	}
	if err := transaction.QueryRow(
		ctx,
		"SELECT account_id::text FROM usage_records WHERE id = $1",
		recordID,
	).Scan(&retainedUsageAccount); err != nil || retainedUsageAccount != historyUser {
		t.Fatalf("retained usage account = %q, %v", retainedUsageAccount, err)
	}
	var (
		retainedLedgerReservation string
		retainedLedgerUsage       string
		retainedLedgerTeam        string
		retainedLedgerTeamName    string
		retainedLedgerBalance     int64
	)
	if err := transaction.QueryRow(ctx, `
		SELECT
			reservation_id::text,
			usage_record_id::text,
			team_id_snapshot::text,
			team_name_snapshot,
			balance_after_micros
		FROM ledger_entries
		WHERE id = $1
	`, linkedLedger).Scan(
		&retainedLedgerReservation,
		&retainedLedgerUsage,
		&retainedLedgerTeam,
		&retainedLedgerTeamName,
		&retainedLedgerBalance,
	); err != nil {
		t.Fatal(err)
	}
	if retainedLedgerReservation != reserveID ||
		retainedLedgerUsage != recordID ||
		retainedLedgerTeam != teamA ||
		retainedLedgerTeamName != "A" ||
		retainedLedgerBalance != 8 {
		t.Fatalf(
			"retained ledger links = reservation:%q usage:%q team:%q/%q balance:%d",
			retainedLedgerReservation,
			retainedLedgerUsage,
			retainedLedgerTeam,
			retainedLedgerTeamName,
			retainedLedgerBalance,
		)
	}

	if _, err := transaction.Exec(
		ctx,
		"INSERT INTO deletion_jobs (id, account_id, job_type) VALUES ($1, $2, 'account')",
		accountJob, accountC,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(ctx, `
		UPDATE deletion_jobs
		SET status = 'failed',
		    attempt_count = attempt_count + 1,
		    next_attempt_at = transaction_timestamp() + interval '1 minute',
		    safe_error_class = 'provider_unavailable'
		WHERE id = $1
	`, accountJob); err != nil {
		t.Fatal(err)
	}
	requireConstraintFailure(t, ctx, transaction,
		"DELETE FROM deletion_jobs WHERE id = $1",
		accountJob,
	)
	if _, err := transaction.Exec(ctx, `
		UPDATE deletion_jobs
		SET status = 'completed',
		    completed_at = transaction_timestamp()
		WHERE id = $1
	`, accountJob); err != nil {
		t.Fatal(err)
	}
	requireConstraintFailure(t, ctx, transaction, `
		UPDATE deletion_jobs
		SET status = 'pending',
		    completed_at = NULL
		WHERE id = $1
	`, accountJob)
	requireConstraintFailure(t, ctx, transaction,
		"UPDATE deletion_jobs SET account_id = $1 WHERE id = $2",
		accountA, accountJob,
	)
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO deletion_jobs (
			id, account_id, organization_id, job_type
		) VALUES (
			'0198a000-0000-7000-8000-000000000146',
			$1,
			$2,
			'account'
		)
	`, accountC, orgA)
	if _, err := transaction.Exec(ctx, "DELETE FROM accounts WHERE id = $1", accountC); err != nil {
		t.Fatal(err)
	}
	var invitationsByDeletedCreator int
	if err := transaction.QueryRow(
		ctx,
		"SELECT count(*) FROM organization_invitations WHERE id = $1",
		deletionInvite,
	).Scan(&invitationsByDeletedCreator); err != nil || invitationsByDeletedCreator != 0 {
		t.Fatalf(
			"invitations retained for deleted creator = %d, %v",
			invitationsByDeletedCreator,
			err,
		)
	}
	var retainedAccount string
	if err := transaction.QueryRow(
		ctx,
		"SELECT account_id::text FROM deletion_jobs WHERE id = $1",
		accountJob,
	).Scan(&retainedAccount); err != nil || retainedAccount != accountC {
		t.Fatalf("retained account target = %q, %v", retainedAccount, err)
	}

	if _, err := transaction.Exec(
		ctx,
		"INSERT INTO deletion_jobs (id, organization_id, job_type) VALUES ($1, $2, 'organization')",
		orgJob, orgC,
	); err != nil {
		t.Fatal(err)
	}
	requireConstraintFailure(t, ctx, transaction,
		"UPDATE deletion_jobs SET organization_id = $1 WHERE id = $2",
		orgA, orgJob,
	)
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO deletion_jobs (
			id, account_id, organization_id, job_type
		) VALUES (
			'0198a000-0000-7000-8000-000000000147',
			$1,
			$2,
			'organization'
		)
	`, accountA, orgC)
	if _, err := transaction.Exec(ctx, `
		INSERT INTO ledger_entries (
			id, organization_id, entry_type, amount_micros,
			balance_after_micros, source_reference
		) VALUES ($1, $2, 'credit_grant', 1, 1, 'retained-organization')
	`, retainedLedger, orgC); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO usage_reservations (
			id, organization_id, team_id, team_name_snapshot, meter_id,
			price_version_id, account_id, service_identity_id, maximum_units,
			usd_micros_per_unit, maximum_cost_micros, held_credit_micros,
			held_overage_micros, client_reference, expires_at
		) VALUES (
			$1, $2, $3, 'General', $4, $5, $6, $7,
			1, 1, 1, 1, 0, 'retained-organization-history',
			transaction_timestamp() + interval '1 minute'
		)
	`, historyReserve, orgC, generalC, meterID, priceID, accountD, serviceID); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO usage_records (
			id, reservation_id, organization_id, team_id, team_name_snapshot,
			meter_id, account_id, service_identity_id, committed_units,
			total_cost_micros, credit_applied_micros, overage_applied_micros
		) VALUES ($1, $2, $3, $4, 'General', $5, $6, $7, 1, 1, 1, 0)
		`, historyRecord, historyReserve, orgC, generalC, meterID, accountD, serviceID); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO ledger_entries (
			id, organization_id, entry_type, amount_micros,
			balance_after_micros, reservation_id, usage_record_id,
			team_id_snapshot, team_name_snapshot, source_reference
		) VALUES (
			'0198a000-0000-7000-8000-000000000210',
			$1, 'credit_commit', -1, 0, $2, $3, $4, 'General',
			'retained-organization-usage'
		)
	`, orgC, historyReserve, historyRecord, generalC); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(ctx, `
		UPDATE usage_reservations
		SET status = 'committed'
		WHERE id = $1
	`, historyReserve); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(ctx, "DELETE FROM organizations WHERE id = $1", orgC); err != nil {
		t.Fatal(err)
	}
	var retainedOrganization string
	if err := transaction.QueryRow(
		ctx,
		"SELECT organization_id::text FROM deletion_jobs WHERE id = $1",
		orgJob,
	).Scan(&retainedOrganization); err != nil || retainedOrganization != orgC {
		t.Fatalf("retained organization target = %q, %v", retainedOrganization, err)
	}
	if err := transaction.QueryRow(
		ctx,
		"SELECT organization_id::text FROM ledger_entries WHERE id = $1",
		retainedLedger,
	).Scan(&retainedOrganization); err != nil || retainedOrganization != orgC {
		t.Fatalf("retained ledger organization snapshot = %q, %v", retainedOrganization, err)
	}
	if err := transaction.QueryRow(ctx, `
		SELECT reservation.organization_id::text
		FROM usage_reservations AS reservation
		JOIN usage_records AS usage ON usage.reservation_id = reservation.id
		WHERE reservation.id = $1
		  AND usage.id = $2
		  AND usage.organization_id = reservation.organization_id
	`, historyReserve, historyRecord).Scan(&retainedOrganization); err != nil ||
		retainedOrganization != orgC {
		t.Fatalf("retained usage organization snapshot = %q, %v", retainedOrganization, err)
	}

	if _, err := transaction.Exec(ctx, `
		INSERT INTO webhook_inbox (
			id, provider, provider_event_id, event_type, payload, payload_sha256
		) VALUES (
			'0198a000-0000-7000-8000-000000000201',
			'polar',
			'event-1',
			'subscription.updated',
			'{"data":{"id":"subscription-1"}}',
			decode(repeat('aa', 32), 'hex')
		)
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(ctx, `
		UPDATE webhook_inbox
		SET attempt_count = attempt_count + 1,
		    next_attempt_at = transaction_timestamp() + interval '1 minute',
		    safe_error_class = 'provider_unavailable'
		WHERE provider = 'polar' AND provider_event_id = 'event-1'
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(ctx, `
		UPDATE webhook_inbox
		SET processed_at = transaction_timestamp()
		WHERE provider = 'polar' AND provider_event_id = 'event-1'
	`); err != nil {
		t.Fatal(err)
	}
	requireConstraintFailure(t, ctx, transaction, `
		UPDATE webhook_inbox
		SET processed_at = NULL
		WHERE provider = 'polar' AND provider_event_id = 'event-1'
	`)
	requireConstraintFailure(t, ctx, transaction, `
		UPDATE webhook_inbox
		SET payload = '{"data":{"id":"rewritten"}}'
		WHERE provider = 'polar' AND provider_event_id = 'event-1'
	`)
	requireConstraintFailure(t, ctx, transaction, `
		DELETE FROM webhook_inbox
		WHERE provider = 'polar' AND provider_event_id = 'event-1'
	`)
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO webhook_inbox (
			id, provider, provider_event_id, event_type, payload, payload_sha256
		) VALUES (
			'0198a000-0000-7000-8000-000000000127',
			'polar',
			'event-scalar',
			'subscription.updated',
			'"not-an-object"',
			decode(repeat('ab', 32), 'hex')
		)
	`)
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO webhook_inbox (
			id, provider, provider_event_id, event_type, payload, payload_sha256
		) VALUES (
			'0198a000-0000-7000-8000-000000000130',
			'polar',
			'event-oversized',
			'subscription.updated',
			jsonb_build_object('data', repeat('a', 1048576)),
			decode(repeat('ac', 32), 'hex')
		)
	`)
	if _, err := transaction.Exec(ctx, `
		INSERT INTO integration_outbox (
			id, integration, operation, aggregate_type, aggregate_id, payload
		) VALUES (
			'0198a000-0000-7000-8000-000000000207',
			'polar',
			'send_usage',
			'usage_record',
			$1,
			'{"units":1}'
		)
	`, recordID); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(ctx, `
		UPDATE integration_outbox
		SET attempt_count = attempt_count + 1,
		    next_attempt_at = transaction_timestamp() + interval '1 minute',
		    safe_error_class = 'provider_unavailable'
		WHERE id = '0198a000-0000-7000-8000-000000000207'
	`); err != nil {
		t.Fatal(err)
	}
	requireConstraintFailure(t, ctx, transaction, `
		DELETE FROM integration_outbox
		WHERE id = '0198a000-0000-7000-8000-000000000207'
	`)
	if _, err := transaction.Exec(ctx, `
		UPDATE integration_outbox
		SET delivered_at = transaction_timestamp()
		WHERE id = '0198a000-0000-7000-8000-000000000207'
	`); err != nil {
		t.Fatal(err)
	}
	requireConstraintFailure(t, ctx, transaction, `
		UPDATE integration_outbox
		SET delivered_at = NULL
		WHERE id = '0198a000-0000-7000-8000-000000000207'
	`)
	requireConstraintFailure(t, ctx, transaction, `
		UPDATE integration_outbox
		SET integration = 'logto',
		    operation = 'delete_user',
		    aggregate_type = 'account',
		    aggregate_id = $1,
		    payload = '{"subject":"rewritten"}'
		WHERE id = '0198a000-0000-7000-8000-000000000207'
	`, accountA)
	if _, err := transaction.Exec(ctx, `
		INSERT INTO audit_events (
			id, event_type, actor_reference, organization_id, result, metadata
		) VALUES (
			$1,
			'schema.audit',
			'actor:v1:00000000000000000000000000000000',
			$2,
			'success',
			'{"request_id":"request-1","trace_id":"0123456789abcdef0123456789abcdef","request_method":"POST","request_procedure":"/delibase.v1.UsageService/ReserveUsage"}'
		)
	`, auditID, orgA); err != nil {
		t.Fatal(err)
	}
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO audit_events (
			id, event_type, actor_reference, organization_id, result
		) VALUES (
			'0198a000-0000-7000-8000-000000000128',
			'schema.audit',
			'raw-logto-subject',
			$1,
			'success'
		)
	`, orgA)
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO audit_events (
			id, event_type, actor_reference, organization_id, result, metadata
		) VALUES (
			'0198a000-0000-7000-8000-000000000215',
			'schema.audit',
			'',
			$1,
			'success',
			'{"request_id":"token:abc123"}'
		)
	`, orgA)
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO audit_events (
			id, event_type, actor_reference, organization_id, result, metadata
		) VALUES (
			'0198a000-0000-7000-8000-000000000129',
			'schema.audit',
			'',
			$1,
			'success',
			'{"authorization":"Bearer secret"}'
		)
	`, orgA)
	requireConstraintFailure(t, ctx, transaction,
		"UPDATE audit_events SET result = 'failure' WHERE id = $1",
		auditID,
	)
	requireConstraintFailure(t, ctx, transaction,
		"DELETE FROM audit_events WHERE id = $1",
		auditID,
	)
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO idempotency_records (
			id, caller_kind, caller_id, operation, idempotency_key,
			request_hash, expires_at
		) VALUES (
			'0198a000-0000-7000-8000-000000000202',
			'user',
			'actor',
			'create_invitation',
			'key',
			decode(repeat('bb', 32), 'hex'),
			transaction_timestamp() + interval '1 day'
		)
	`)
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO idempotency_records (
			id, caller_kind, caller_id, operation, idempotency_key,
			request_hash, expires_at
		) VALUES (
			'0198a000-0000-7000-8000-000000000203',
			'service',
			'actor',
			'commmit_usage',
			'key',
			decode(repeat('cc', 32), 'hex'),
			transaction_timestamp() + interval '1 day'
		)
	`)
	if _, err := transaction.Exec(ctx, `
		INSERT INTO idempotency_records (
			id, caller_kind, caller_id, operation, idempotency_key,
			request_hash, expires_at
		) VALUES (
			'0198a000-0000-7000-8000-000000000204',
			'service',
			'actor',
			'commit_usage',
			'key',
			decode(repeat('dd', 32), 'hex'),
			transaction_timestamp() + interval '1 day'
		)
	`); err != nil {
		t.Fatal(err)
	}
	requireConstraintFailure(t, ctx, transaction, `
		UPDATE idempotency_records
		SET request_hash = decode(repeat('ee', 32), 'hex')
		WHERE id = '0198a000-0000-7000-8000-000000000204'
	`)
	requireConstraintFailure(t, ctx, transaction, `
		DELETE FROM idempotency_records
		WHERE id = '0198a000-0000-7000-8000-000000000204'
	`)
	if _, err := transaction.Exec(ctx, `
		INSERT INTO idempotency_records (
			id, caller_kind, caller_id, operation, idempotency_key,
			request_hash, created_at, expires_at
		) VALUES (
			'0198a000-0000-7000-8000-000000000206',
			'user',
			'expired-actor',
			'create_organization',
			'expired-key',
			decode(repeat('ff', 32), 'hex'),
			'2000-01-01T00:00:00Z',
			'2000-01-02T00:00:00Z'
		)
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(ctx, `
		DELETE FROM idempotency_records
		WHERE id = '0198a000-0000-7000-8000-000000000206'
	`); err != nil {
		t.Fatal(err)
	}
}

func createTestOrganization(
	t *testing.T,
	ctx context.Context,
	store *Store,
	organizationID string,
	name string,
	slug string,
	ownerAccountID string,
) {
	t.Helper()
	transaction, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = transaction.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := transaction.Exec(ctx, `
		INSERT INTO organizations (id, name, slug)
		VALUES ($1, $2, $3)
	`, organizationID, name, slug); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO organization_memberships (organization_id, account_id, role)
		VALUES ($1, $2, 'owner')
	`, organizationID, ownerAccountID); err != nil {
		t.Fatal(err)
	}
	generalTeamID, err := uuidv7.New()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO teams (id, organization_id, name, protected_general)
		VALUES ($1, $2, 'General', true)
	`, generalTeamID.String(), organizationID); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO polar_customers (organization_id, polar_customer_id)
		VALUES ($1, $2)
	`, organizationID, "test-"+organizationID); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func requireConstraintFailure(
	t *testing.T,
	ctx context.Context,
	transaction pgx.Tx,
	statement string,
	arguments ...any,
) {
	t.Helper()
	savepoint, err := transaction.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := savepoint.Exec(ctx, statement, arguments...); err == nil {
		_ = savepoint.Rollback(context.WithoutCancel(ctx))
		t.Fatal("statement unexpectedly satisfied schema constraints")
	}
	if err := savepoint.Rollback(context.WithoutCancel(ctx)); err != nil {
		t.Fatal(err)
	}
}
