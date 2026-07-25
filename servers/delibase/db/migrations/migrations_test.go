package migrations

import (
	"context"
	"os"
	"testing"
	"testing/fstest"
	"time"

	"github.com/delinoio/oss/servers/internal/uuidv7"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestEmbeddedMigrationsAreOrdered(t *testing.T) {
	t.Parallel()
	ordered, err := load(files)
	if err != nil {
		t.Fatal(err)
	}
	if len(ordered) != 10 {
		t.Fatalf("migration count = %d, want 10", len(ordered))
	}
	for index, item := range ordered {
		want := int64(index + 1)
		if item.version != want {
			t.Fatalf("migration %d version = %d, want %d", index, item.version, want)
		}
	}
}

func TestMigrationNamesAndVersionsFailClosed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source fstest.MapFS
	}{
		{
			name: "invalid filename",
			source: fstest.MapFS{
				"migration.sql": &fstest.MapFile{Data: []byte("SELECT 1;")},
			},
		},
		{
			name: "duplicate version",
			source: fstest.MapFS{
				"000001_first.sql":  &fstest.MapFile{Data: []byte("SELECT 1;")},
				"000001_second.sql": &fstest.MapFile{Data: []byte("SELECT 1;")},
			},
		},
		{
			name: "version gap",
			source: fstest.MapFS{
				"000002_second.sql": &fstest.MapFile{Data: []byte("SELECT 1;")},
			},
		},
		{
			name:   "empty set",
			source: fstest.MapFS{},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := load(test.source); err == nil {
				t.Fatal("load() succeeded")
			}
		})
	}
}

func TestPostgreSQLUsageMigrationBackfillsPrunedPolarMapping(t *testing.T) {
	databaseURL := os.Getenv("DELIBASE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DELIBASE_TEST_DATABASE_URL is not set; run scripts/test-postgres.sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = connection.Close(context.WithoutCancel(ctx))
	}()
	schema := "usage_migration_" + uuidv7.MustNew().String()[24:]
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err = connection.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(
			context.Background(),
			5*time.Second,
		)
		defer cleanupCancel()
		_, _ = connection.Exec(
			cleanupCtx,
			"DROP SCHEMA "+identifier+" CASCADE",
		)
	})
	if _, err = connection.Exec(
		ctx,
		"SET search_path TO "+identifier+", public",
	); err != nil {
		t.Fatal(err)
	}
	if _, err = connection.Exec(ctx, `
		CREATE TABLE schema_migrations (
			version bigint PRIMARY KEY,
			name text NOT NULL UNIQUE,
			checksum bytea NOT NULL CHECK (octet_length(checksum) = 32),
			applied_at timestamptz NOT NULL DEFAULT transaction_timestamp()
		)
	`); err != nil {
		t.Fatal(err)
	}
	ordered, err := load(files)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range ordered[:len(ordered)-1] {
		if err = apply(ctx, connection, item); err != nil {
			t.Fatal(err)
		}
	}

	appID := uuidv7.MustNew()
	meterID := uuidv7.MustNew()
	usageMeterID := uuidv7.MustNew()
	priceID := uuidv7.MustNew()
	usagePriceID := uuidv7.MustNew()
	serviceID := uuidv7.MustNew()
	organizationID := uuidv7.MustNew()
	teamID := uuidv7.MustNew()
	accountID := uuidv7.MustNew()
	periodID := uuidv7.MustNew()
	reservationID := uuidv7.MustNew()
	usageReservationID := uuidv7.MustNew()
	usageRecordID := uuidv7.MustNew()
	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	err = pgx.BeginFunc(ctx, connection, func(transaction pgx.Tx) error {
		if _, transactionErr := transaction.Exec(
			ctx,
			"SET LOCAL session_replication_role = replica",
		); transactionErr != nil {
			return transactionErr
		}
		statements := []struct {
			sql  string
			args []any
		}{
			{
				sql: `INSERT INTO catalog_apps (id, slug, name)
					VALUES ($1, 'migration-app', 'Migration App')`,
				args: []any{appID},
			},
			{
				sql: `INSERT INTO catalog_meters (
						id, app_id, meter_key, name, unit_name,
						reservation_ttl_seconds
					) VALUES ($1, $2, 'migration-meter', 'Migration Meter',
						'unit', 60)`,
				args: []any{meterID, appID},
			},
			{
				sql: `INSERT INTO catalog_meters (
						id, app_id, meter_key, name, unit_name,
						reservation_ttl_seconds
					) VALUES ($1, $2, 'migration-usage-meter',
						'Migration Usage Meter', 'unit', 60)`,
				args: []any{usageMeterID, appID},
			},
			{
				sql: `INSERT INTO catalog_price_versions (
						id, meter_id, usd_micros_per_unit, effective_from
					) VALUES ($1, $2, 5, $3)`,
				args: []any{priceID, meterID, createdAt.Add(-time.Hour)},
			},
			{
				sql: `INSERT INTO catalog_price_versions (
						id, meter_id, usd_micros_per_unit, effective_from
					) VALUES ($1, $2, 5, $3)`,
				args: []any{
					usagePriceID,
					usageMeterID,
					createdAt.Add(-time.Hour),
				},
			},
			{
				sql: `INSERT INTO service_identities (
						id, logto_client_id, name
					) VALUES ($1, 'migration-service', 'Migration Service')`,
				args: []any{serviceID},
			},
			{
				sql: `INSERT INTO polar_meter_mappings (
						meter_id, polar_meter_id
					) VALUES ($1, 'migration-meter-event')`,
				args: []any{meterID},
			},
			{
				sql: `INSERT INTO polar_meter_mappings (
						meter_id, polar_meter_id
					) VALUES ($1, 'migration-usage-event')`,
				args: []any{usageMeterID},
			},
			{
				sql: `INSERT INTO billing_periods (
						id, organization_id, starts_at, ends_at,
						overage_limit_micros
					) VALUES ($1, $2, $3, $4, 100)`,
				args: []any{
					periodID,
					organizationID,
					createdAt.Add(-time.Hour),
					createdAt.Add(time.Hour),
				},
			},
			{
				sql: `INSERT INTO usage_reservations (
						id, organization_id, team_id, team_name_snapshot,
						meter_id, price_version_id, account_id,
						service_identity_id, maximum_units,
						usd_micros_per_unit, maximum_cost_micros,
						held_credit_micros, held_overage_micros,
						client_reference, status, expires_at, finalized_at,
						created_at
					) VALUES (
						$1, $2, $3, 'General', $4, $5, $6, $7, 1, 5, 5,
						0, 5, 'migration reservation', 'released',
						$8, $9, $9
					)`,
				args: []any{
					reservationID,
					organizationID,
					teamID,
					meterID,
					priceID,
					accountID,
					serviceID,
					createdAt.Add(time.Minute),
					createdAt,
				},
			},
			{
				sql: `INSERT INTO usage_reservations (
						id, organization_id, team_id, team_name_snapshot,
						meter_id, price_version_id, account_id,
						service_identity_id, maximum_units,
						usd_micros_per_unit, maximum_cost_micros,
						held_credit_micros, held_overage_micros,
						client_reference, status, expires_at, finalized_at,
						created_at
					) VALUES (
						$1, $2, $3, 'General', $4, $5, $6, $7, 1, 5, 5,
						0, 5, 'migration-usage-reservation', 'released',
						$8, $9, $9
					)`,
				args: []any{
					usageReservationID,
					organizationID,
					teamID,
					usageMeterID,
					usagePriceID,
					accountID,
					serviceID,
					createdAt.Add(time.Minute),
					createdAt,
				},
			},
			{
				sql: `INSERT INTO usage_records (
						id, reservation_id, organization_id, team_id,
						team_name_snapshot, meter_id, account_id,
						service_identity_id, committed_units,
						total_cost_micros, credit_applied_micros,
						overage_applied_micros, committed_at
					) VALUES (
						$1, $2, $3, $4, 'General', $5, $6, $7,
						1, 5, 0, 5, $8
					)`,
				args: []any{
					usageRecordID,
					usageReservationID,
					organizationID,
					teamID,
					usageMeterID,
					accountID,
					serviceID,
					createdAt,
				},
			},
			{
				sql:  `DELETE FROM polar_meter_mappings WHERE meter_id = $1`,
				args: []any{meterID},
			},
		}
		for _, statement := range statements {
			if _, transactionErr := transaction.Exec(
				ctx,
				statement.sql,
				statement.args...,
			); transactionErr != nil {
				return transactionErr
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = apply(ctx, connection, ordered[len(ordered)-1]); err != nil {
		t.Fatal(err)
	}
	var eventName string
	var backfilledPeriodID uuid.UUID
	var clientReference string
	if err = connection.QueryRow(
		ctx,
		`SELECT polar_event_name_snapshot, overage_billing_period_id,
		        client_reference
		 FROM usage_reservations
		 WHERE id = $1`,
		reservationID,
	).Scan(&eventName, &backfilledPeriodID, &clientReference); err != nil {
		t.Fatal(err)
	}
	if eventName != "unknown" || backfilledPeriodID != periodID ||
		clientReference != "legacy:"+reservationID.String() {
		t.Fatalf(
			"backfilled event = %q, period = %v, client reference = %q",
			eventName,
			backfilledPeriodID,
			clientReference,
		)
	}
	var outboxAggregateID uuid.UUID
	var outboxEventName string
	var outboxOrganizationID string
	var outboxUsageRecordID string
	var outboxUnits int64
	var outboxCommittedAt time.Time
	if err = connection.QueryRow(
		ctx,
		`SELECT aggregate_id,
		        payload ->> 'event_name',
		        payload ->> 'organization_id',
		        payload ->> 'usage_record_id',
		        (payload ->> 'units')::bigint,
		        (payload ->> 'committed_at')::timestamptz
		 FROM integration_outbox
		 WHERE integration = 'polar'
		   AND operation = 'report_usage'
		   AND aggregate_id = $1`,
		usageRecordID,
	).Scan(
		&outboxAggregateID,
		&outboxEventName,
		&outboxOrganizationID,
		&outboxUsageRecordID,
		&outboxUnits,
		&outboxCommittedAt,
	); err != nil {
		t.Fatal(err)
	}
	if outboxAggregateID != usageRecordID ||
		outboxEventName != "migration-usage-event" ||
		outboxOrganizationID != organizationID.String() ||
		outboxUsageRecordID != usageRecordID.String() ||
		outboxUnits != 5 ||
		!outboxCommittedAt.Equal(createdAt) {
		t.Fatalf(
			"backfilled Polar outbox = %v %q %q %q %d %s",
			outboxAggregateID,
			outboxEventName,
			outboxOrganizationID,
			outboxUsageRecordID,
			outboxUnits,
			outboxCommittedAt,
		)
	}
}
