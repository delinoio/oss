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
	"github.com/jackc/pgx/v5/pgtype"
)

func TestEmbeddedMigrationsAreOrdered(t *testing.T) {
	t.Parallel()
	ordered, err := load(files)
	if err != nil {
		t.Fatal(err)
	}
	if len(ordered) != 14 {
		t.Fatalf("migration count = %d, want 14", len(ordered))
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

func TestPostgreSQLUsageMigrationPreservesLegacyState(t *testing.T) {
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
	// Seed the schema immediately before the usage-service migration so this
	// remains a reproducibility check for its legacy backfill as later ordered
	// migrations are appended.
	const usageServiceMigrationIndex = 9
	for _, item := range ordered[:usageServiceMigrationIndex] {
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
	ambiguousUsageRecordID := uuidv7.MustNew()
	creditOnlyUsageRecordID := uuidv7.MustNew()
	creditOnlyOutboxID := uuidv7.MustNew()
	duplicateReservationAID := uuidv7.MustNew()
	duplicateReservationBID := uuidv7.MustNew()
	auditedAuthorizationID := uuidv7.MustNew()
	unknownAuthorizationID := uuidv7.MustNew()
	auditedDeletedTeamID := uuidv7.MustNew()
	unknownDeletedTeamID := uuidv7.MustNew()
	auditedFeatureResourceID := uuidv7.MustNew()
	unknownFeatureResourceID := uuidv7.MustNew()
	teamDeletedAuditID := uuidv7.MustNew()
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
							meter_id, polar_meter_id, created_at
						) VALUES ($1, 'migration-meter-event', $2)`,
				args: []any{meterID, createdAt.Add(-2 * time.Hour)},
			},
			{
				sql: `INSERT INTO polar_meter_mappings (
							meter_id, polar_meter_id, created_at
						) VALUES ($1, 'migration-usage-event', $2)`,
				args: []any{usageMeterID, createdAt.Add(-2 * time.Hour)},
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
					ambiguousUsageRecordID,
					reservationID,
					organizationID,
					teamID,
					meterID,
					accountID,
					serviceID,
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
							5, 0, 'duplicate-reference', 'released',
							$8, $9, $9
						)`,
				args: []any{
					duplicateReservationAID,
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
							5, 0, 'duplicate-reference', 'released',
							$8, $9, $9
						)`,
				args: []any{
					duplicateReservationBID,
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
							1, 5, 5, 0, $8
						)`,
				args: []any{
					creditOnlyUsageRecordID,
					duplicateReservationAID,
					organizationID,
					teamID,
					usageMeterID,
					accountID,
					serviceID,
					createdAt,
				},
			},
			{
				sql: `INSERT INTO integration_outbox (
							id, integration, operation, aggregate_type,
							aggregate_id, payload, created_at,
							idempotency_key, actor_reference
						) VALUES (
							$1, 'polar', 'report_usage', 'usage_record',
							$2,
							jsonb_build_object(
								'event_name', 'migration-usage-event',
								'organization_id', $3::uuid::text,
								'usage_record_id', $2::uuid::text,
								'units', 0,
								'committed_at', $4::timestamptz
							),
							$4, 'legacy-credit-only-usage', ''
						)`,
				args: []any{
					creditOnlyOutboxID,
					creditOnlyUsageRecordID,
					organizationID,
					createdAt,
				},
			},
			{
				sql:  `DELETE FROM polar_meter_mappings WHERE meter_id = $1`,
				args: []any{meterID},
			},
			{
				sql: `INSERT INTO polar_meter_mappings (
							meter_id, polar_meter_id, created_at
						) VALUES ($1, 'replacement-meter-event', $2)`,
				args: []any{meterID, createdAt.Add(time.Minute)},
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
	if err = apply(ctx, connection, ordered[usageServiceMigrationIndex]); err == nil {
		t.Fatal("usage migration accepted a replacement historical Polar mapping")
	}
	err = pgx.BeginFunc(ctx, connection, func(transaction pgx.Tx) error {
		if _, transactionErr := transaction.Exec(
			ctx,
			"SET LOCAL session_replication_role = replica",
		); transactionErr != nil {
			return transactionErr
		}
		_, transactionErr := transaction.Exec(
			ctx,
			"DELETE FROM usage_records WHERE id = $1",
			ambiguousUsageRecordID,
		)
		return transactionErr
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = apply(ctx, connection, ordered[usageServiceMigrationIndex]); err == nil {
		t.Fatal("usage migration accepted a credit-only Polar outbox event")
	}
	err = pgx.BeginFunc(ctx, connection, func(transaction pgx.Tx) error {
		if _, transactionErr := transaction.Exec(
			ctx,
			"SET LOCAL session_replication_role = replica",
		); transactionErr != nil {
			return transactionErr
		}
		_, transactionErr := transaction.Exec(
			ctx,
			"DELETE FROM integration_outbox WHERE id = $1",
			creditOnlyOutboxID,
		)
		return transactionErr
	})
	if err != nil {
		t.Fatal(err)
	}
	err = pgx.BeginFunc(ctx, connection, func(transaction pgx.Tx) error {
		if _, transactionErr := transaction.Exec(
			ctx,
			"SET LOCAL session_replication_role = replica",
		); transactionErr != nil {
			return transactionErr
		}
		_, transactionErr := transaction.Exec(
			ctx,
			`UPDATE polar_meter_mappings
			 SET polar_meter_id = 'migration/usage-event'
			 WHERE meter_id = $1`,
			usageMeterID,
		)
		return transactionErr
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = apply(ctx, connection, ordered[usageServiceMigrationIndex]); err == nil {
		t.Fatal("usage migration accepted an invalid persisted Polar mapping")
	}
	err = pgx.BeginFunc(ctx, connection, func(transaction pgx.Tx) error {
		if _, transactionErr := transaction.Exec(
			ctx,
			"SET LOCAL session_replication_role = replica",
		); transactionErr != nil {
			return transactionErr
		}
		_, transactionErr := transaction.Exec(
			ctx,
			`UPDATE polar_meter_mappings
			 SET polar_meter_id = $2
			 WHERE meter_id = $1`,
			usageMeterID,
			"\u202fmigration-usage-event",
		)
		return transactionErr
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = apply(ctx, connection, ordered[usageServiceMigrationIndex]); err == nil {
		t.Fatal("usage migration accepted Unicode edge whitespace in a Polar mapping")
	}
	err = pgx.BeginFunc(ctx, connection, func(transaction pgx.Tx) error {
		if _, transactionErr := transaction.Exec(
			ctx,
			"SET LOCAL session_replication_role = replica",
		); transactionErr != nil {
			return transactionErr
		}
		_, transactionErr := transaction.Exec(
			ctx,
			`UPDATE polar_meter_mappings
			 SET polar_meter_id = 'migration-usage-event'
			 WHERE meter_id = $1`,
			usageMeterID,
		)
		return transactionErr
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = apply(ctx, connection, ordered[usageServiceMigrationIndex]); err != nil {
		t.Fatal(err)
	}
	const backgroundAuditMigrationIndex = 12
	for _, item := range ordered[usageServiceMigrationIndex+1 : backgroundAuditMigrationIndex] {
		if err = apply(ctx, connection, item); err != nil {
			t.Fatal(err)
		}
	}
	err = pgx.BeginFunc(ctx, connection, func(transaction pgx.Tx) error {
		if _, transactionErr := transaction.Exec(
			ctx,
			"SET LOCAL session_replication_role = replica",
		); transactionErr != nil {
			return transactionErr
		}
		for _, authorization := range []struct {
			id                uuid.UUID
			teamID            uuid.UUID
			featureResourceID uuid.UUID
		}{
			{
				id:                auditedAuthorizationID,
				teamID:            auditedDeletedTeamID,
				featureResourceID: auditedFeatureResourceID,
			},
			{
				id:                unknownAuthorizationID,
				teamID:            unknownDeletedTeamID,
				featureResourceID: unknownFeatureResourceID,
			},
		} {
			if _, transactionErr := transaction.Exec(ctx, `
				INSERT INTO background_usage_authorizations (
					id, authorizer_account_id, owner_type, owner_account_id,
					organization_id, team_id, service_identity_id, meter_id,
					purpose, feature_resource_id, period, maximum_units,
					status, revision, created_at, updated_at, revoked_at,
					retain_until
				) VALUES (
					$1, $2, 'personal_account', $2, $3, $4, $5, $6,
					'realqa_storage', $7, 'utc_day', 1, 'access_lost', 2,
					$8::timestamptz, $8::timestamptz, $8::timestamptz,
					$8::timestamptz + interval '7 years'
				)
			`,
				authorization.id,
				accountID,
				organizationID,
				authorization.teamID,
				serviceID,
				meterID,
				authorization.featureResourceID,
				createdAt,
			); transactionErr != nil {
				return transactionErr
			}
		}
		_, transactionErr := transaction.Exec(ctx, `
			INSERT INTO audit_events (
				id, occurred_at, event_type, actor_reference,
				organization_id, team_id, team_name_snapshot,
				decision, result, metadata, retain_until
			) VALUES (
				$1, $2::timestamptz, 'team.deleted', '', $3, $4,
				'Deleted Migration Team', 'allow', 'success', '{}'::jsonb,
				$2::timestamptz + interval '7 years'
			)
		`,
			teamDeletedAuditID,
			createdAt,
			organizationID,
			auditedDeletedTeamID,
		)
		return transactionErr
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = apply(
		ctx,
		connection,
		ordered[backgroundAuditMigrationIndex],
	); err != nil {
		t.Fatal(err)
	}
	for _, item := range ordered[backgroundAuditMigrationIndex+1:] {
		if err = apply(ctx, connection, item); err != nil {
			t.Fatal(err)
		}
	}
	var eventName string
	var backfilledPeriodID uuid.UUID
	var clientReference string
	var clientReferenceGrandfathered bool
	if err = connection.QueryRow(
		ctx,
		`SELECT polar_event_name_snapshot, overage_billing_period_id,
		        client_reference, client_reference_grandfathered
		 FROM usage_reservations
		 WHERE id = $1`,
		reservationID,
	).Scan(
		&eventName,
		&backfilledPeriodID,
		&clientReference,
		&clientReferenceGrandfathered,
	); err != nil {
		t.Fatal(err)
	}
	if eventName != "unknown" || backfilledPeriodID != periodID ||
		clientReference != "migration reservation" ||
		!clientReferenceGrandfathered {
		t.Fatalf(
			"backfilled event = %q, period = %v, client reference = %q, grandfathered = %t",
			eventName,
			backfilledPeriodID,
			clientReference,
			clientReferenceGrandfathered,
		)
	}
	var grandfatheredDuplicateCount int
	if err = connection.QueryRow(
		ctx,
		`SELECT count(*)
		 FROM usage_reservations
		 WHERE service_identity_id = $1
		   AND client_reference = 'duplicate-reference'
		   AND client_reference_grandfathered`,
		serviceID,
	).Scan(&grandfatheredDuplicateCount); err != nil {
		t.Fatal(err)
	}
	if grandfatheredDuplicateCount != 2 {
		t.Fatalf(
			"grandfathered duplicate count = %d, want 2",
			grandfatheredDuplicateCount,
		)
	}
	var auditedSnapshot pgtype.Text
	if err = connection.QueryRow(ctx, `
		SELECT team_name_snapshot
		FROM background_usage_authorizations
		WHERE id = $1
	`, auditedAuthorizationID).Scan(&auditedSnapshot); err != nil {
		t.Fatal(err)
	}
	if !auditedSnapshot.Valid ||
		auditedSnapshot.String != "Deleted Migration Team" {
		t.Fatalf("audited deleted-team snapshot = %#v", auditedSnapshot)
	}
	var unknownSnapshot pgtype.Text
	if err = connection.QueryRow(ctx, `
		SELECT team_name_snapshot
		FROM background_usage_authorizations
		WHERE id = $1
	`, unknownAuthorizationID).Scan(&unknownSnapshot); err != nil {
		t.Fatal(err)
	}
	if unknownSnapshot.Valid {
		t.Fatalf("unknown deleted-team snapshot = %#v", unknownSnapshot)
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
