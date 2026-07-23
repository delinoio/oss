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
	var activeApps, activeServices, serviceMappings, polarMappings int
	if err := store.pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM catalog_apps WHERE enabled),
			(SELECT count(*) FROM service_identities WHERE enabled),
			(SELECT count(*) FROM service_meter_allowlists),
			(SELECT count(*) FROM polar_meter_mappings)
	`).Scan(
		&activeApps,
		&activeServices,
		&serviceMappings,
		&polarMappings,
	); err != nil {
		t.Fatal(err)
	}
	if activeApps != 0 || activeServices != 0 ||
		serviceMappings != 0 || polarMappings != 0 {
		t.Fatalf(
			"stale catalog state = apps:%d services:%d service mappings:%d Polar mappings:%d",
			activeApps,
			activeServices,
			serviceMappings,
			polarMappings,
		)
	}
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
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO organizations (id, name, slug)
		VALUES ($1, 'Concurrent Owners', 'concurrent-owners')
	`, orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO organization_memberships (organization_id, account_id, role)
		VALUES ($1, $2, 'owner'), ($1, $3, 'owner')
	`, orgID, accountA, accountB); err != nil {
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
		accountA     = "0198a000-0000-7000-8000-000000000001"
		accountB     = "0198a000-0000-7000-8000-000000000002"
		accountC     = "0198a000-0000-7000-8000-000000000003"
		accountD     = "0198a000-0000-7000-8000-000000000004"
		historyUser  = "0198a000-0000-7000-8000-000000000005"
		orgA         = "0198a000-0000-7000-8000-000000000011"
		orgB         = "0198a000-0000-7000-8000-000000000012"
		orgC         = "0198a000-0000-7000-8000-000000000013"
		teamA        = "0198a000-0000-7000-8000-000000000021"
		teamB        = "0198a000-0000-7000-8000-000000000022"
		activeTeam   = "0198a000-0000-7000-8000-000000000023"
		generalA     = "0198a000-0000-7000-8000-000000000024"
		generalC     = "0198a000-0000-7000-8000-000000000025"
		appID        = "0198a000-0000-7000-8000-000000000031"
		meterID      = "0198a000-0000-7000-8000-000000000032"
		priceID      = "0198a000-0000-7000-8000-000000000033"
		serviceID    = "0198a000-0000-7000-8000-000000000034"
		meterB       = "0198a000-0000-7000-8000-000000000035"
		priceB       = "0198a000-0000-7000-8000-000000000036"
		serviceB     = "0198a000-0000-7000-8000-000000000037"
		reserveID    = "0198a000-0000-7000-8000-000000000041"
		inviteID     = "0198a000-0000-7000-8000-000000000042"
		recordID     = "0198a000-0000-7000-8000-000000000043"
		activeHold   = "0198a000-0000-7000-8000-000000000044"
		releasedHold = "0198a000-0000-7000-8000-000000000047"
		expiredHold  = "0198a000-0000-7000-8000-000000000048"
		subA         = "0198a000-0000-7000-8000-000000000061"
		subB         = "0198a000-0000-7000-8000-000000000062"
		periodA      = "0198a000-0000-7000-8000-000000000071"
		periodB      = "0198a000-0000-7000-8000-000000000072"
		accountJob   = "0198a000-0000-7000-8000-000000000051"
		orgJob       = "0198a000-0000-7000-8000-000000000052"
	)
	setup := []struct {
		statement string
		arguments []any
	}{
		{"INSERT INTO accounts (id, logto_subject) VALUES ($1, 'schema-a'), ($2, 'schema-b'), ($3, 'schema-c'), ($4, 'schema-d'), ($5, 'schema-history')", []any{accountA, accountB, accountC, accountD, historyUser}},
		{"INSERT INTO organizations (id, name, slug) VALUES ($1, 'A', 'schema-a'), ($2, 'B', 'schema-b'), ($3, 'C', 'schema-c')", []any{orgA, orgB, orgC}},
		{"INSERT INTO organization_memberships (organization_id, account_id, role) VALUES ($1, $2, 'owner'), ($3, $4, 'owner'), ($5, $6, 'owner')", []any{orgA, accountA, orgB, accountB, orgC, accountD}},
		{"INSERT INTO organization_memberships (organization_id, account_id, role) VALUES ($1, $2, 'member')", []any{orgA, historyUser}},
		{"INSERT INTO teams (id, organization_id, name) VALUES ($1, $2, 'A'), ($3, $4, 'B'), ($5, $2, 'Active')", []any{teamA, orgA, teamB, orgB, activeTeam}},
		{"INSERT INTO teams (id, organization_id, name, protected_general) VALUES ($1, $2, 'General', true), ($3, $4, 'General', true)", []any{generalA, orgA, generalC, orgC}},
		{"INSERT INTO catalog_apps (id, slug, name) VALUES ($1, 'schema-app', 'Schema App')", []any{appID}},
		{"INSERT INTO catalog_meters (id, app_id, meter_key, name, unit_name, reservation_ttl_seconds) VALUES ($1, $2, 'requests', 'Requests', 'request', 60), ($3, $2, 'tokens', 'Tokens', 'token', 60)", []any{meterID, appID, meterB}},
		{"INSERT INTO catalog_price_versions (id, meter_id, usd_micros_per_unit, effective_from) VALUES ($1, $2, 1, '2026-01-01'), ($3, $4, 2, '2026-01-01')", []any{priceID, meterID, priceB, meterB}},
		{"INSERT INTO service_identities (id, logto_client_id, name) VALUES ($1, 'schema-service', 'Schema Service'), ($2, 'schema-service-b', 'Schema Service B')", []any{serviceID, serviceB}},
		{"INSERT INTO service_meter_allowlists (service_identity_id, meter_id) VALUES ($1, $2)", []any{serviceID, meterID}},
		{"INSERT INTO subscriptions (id, organization_id, polar_subscription_id, status) VALUES ($1, $2, 'polar-a', 'active'), ($3, $4, 'polar-b', 'active')", []any{subA, orgA, subB, orgB}},
		{"INSERT INTO billing_periods (id, organization_id, subscription_id, starts_at, ends_at) VALUES ($1, $2, $3, '2026-01-01', '2026-02-01'), ($4, $5, $6, '2026-01-01', '2026-02-01')", []any{periodA, orgA, subA, periodB, orgB, subB}},
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
			'2026-01-15', '2026-02-15'
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
			amount_micros, source_reference
		) VALUES (
			'0198a000-0000-7000-8000-000000000075', $1, $2,
			'credit_grant', 1, 'cross-period'
		)
	`, orgA, periodB)
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO ledger_entries (
			id, organization_id, billing_period_id, entry_type,
			amount_micros, source_reference
		) VALUES (
			'0198a000-0000-7000-8000-000000000076', $1, $2,
			'adjustment', 1, 'unsupported-operation'
		)
	`, orgA, periodA)
	ledgerOperations := []string{
		"credit_grant",
		"credit_reversal",
		"credit_hold",
		"credit_commit",
		"credit_release",
		"overage_hold",
		"overage_commit",
		"overage_release",
		"credit_forfeiture",
	}
	for index, operation := range ledgerOperations {
		if _, err := transaction.Exec(ctx, `
			INSERT INTO ledger_entries (
				id, organization_id, billing_period_id, entry_type,
				amount_micros, source_reference
			) VALUES (
				('0198a000-0000-7000-8000-' || lpad(($1 + 80)::text, 12, '0'))::uuid,
				$2, $3, $4, 1, $4
			)
		`, index, orgA, periodA, operation); err != nil {
			t.Fatal(err)
		}
	}
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
			$4, $5, $6, 2, 1, 1, 1, 0, 'underpriced',
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
		) VALUES ($1, $2, $3, 'A', $4, $5, $6, $7, 1, 1, 1, 1, 0, 'valid', transaction_timestamp() + interval '1 minute')
	`, reserveID, orgA, teamA, meterID, priceID, historyUser, serviceID); err != nil {
		t.Fatal(err)
	}
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
	if _, err := transaction.Exec(ctx, `
		INSERT INTO usage_reservations (
			id, organization_id, team_id, team_name_snapshot, meter_id,
			price_version_id, account_id, service_identity_id, maximum_units,
			usd_micros_per_unit, maximum_cost_micros, held_credit_micros,
			held_overage_micros, client_reference, status, expires_at, finalized_at
		) VALUES ($1, $2, $3, 'A', $4, $5, $6, $7, 1, 1, 1, 1, 0, 'released', 'released', transaction_timestamp() + interval '1 minute', transaction_timestamp())
	`, releasedHold, orgA, teamA, meterID, priceID, historyUser, serviceID); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO usage_reservations (
			id, organization_id, team_id, team_name_snapshot, meter_id,
			price_version_id, account_id, service_identity_id, maximum_units,
			usd_micros_per_unit, maximum_cost_micros, held_credit_micros,
			held_overage_micros, client_reference, expires_at
		) VALUES ($1, $2, $3, 'A', $4, $5, $6, $7, 1, 1, 1, 1, 0, 'expired', transaction_timestamp() - interval '1 minute')
	`, expiredHold, orgA, teamA, meterID, priceID, historyUser, serviceID); err != nil {
		t.Fatal(err)
	}
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
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO usage_records (
			id, reservation_id, organization_id, team_id, team_name_snapshot,
			meter_id, account_id, service_identity_id, committed_units,
			total_cost_micros, credit_applied_micros, overage_applied_micros
		) VALUES ($1, $2, $3, $4, 'A', $5, $6, $7, 1, 1, 1, 0)
	`, recordID, expiredHold, orgA, teamA, meterID, historyUser, serviceID)
	if _, err := transaction.Exec(ctx, `
		UPDATE usage_reservations
		SET status = 'expired', finalized_at = transaction_timestamp()
		WHERE id = $1
	`, expiredHold); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO usage_records (
			id, reservation_id, organization_id, team_id, team_name_snapshot,
			meter_id, account_id, service_identity_id, committed_units,
			total_cost_micros, credit_applied_micros, overage_applied_micros
		) VALUES ($1, $2, $3, $4, 'A', $5, $6, $7, 1, 1, 1, 0)
	`, recordID, reserveID, orgA, teamA, meterID, historyUser, serviceID); err != nil {
		t.Fatal(err)
	}
	var reservationStatus string
	if err := transaction.QueryRow(
		ctx,
		"SELECT status FROM usage_reservations WHERE id = $1",
		reserveID,
	).Scan(&reservationStatus); err != nil || reservationStatus != "committed" {
		t.Fatalf("reservation status = %q, %v", reservationStatus, err)
	}
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

	if _, err := transaction.Exec(
		ctx,
		"INSERT INTO deletion_jobs (id, account_id, job_type) VALUES ($1, $2, 'account')",
		accountJob, accountC,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(ctx, "DELETE FROM accounts WHERE id = $1", accountC); err != nil {
		t.Fatal(err)
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

	if _, err := transaction.Exec(ctx, `
		INSERT INTO webhook_inbox (
			id, provider, provider_event_id, event_type, payload_sha256
		) VALUES (
			'0198a000-0000-7000-8000-000000000201',
			'polar',
			'event-1',
			'subscription.updated',
			decode(repeat('aa', 32), 'hex')
		)
	`); err != nil {
		t.Fatal(err)
	}
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
