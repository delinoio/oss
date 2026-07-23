package database

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

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
		accountA   = "0198a000-0000-7000-8000-000000000001"
		accountB   = "0198a000-0000-7000-8000-000000000002"
		accountC   = "0198a000-0000-7000-8000-000000000003"
		orgA       = "0198a000-0000-7000-8000-000000000011"
		orgB       = "0198a000-0000-7000-8000-000000000012"
		orgC       = "0198a000-0000-7000-8000-000000000013"
		teamA      = "0198a000-0000-7000-8000-000000000021"
		teamB      = "0198a000-0000-7000-8000-000000000022"
		appID      = "0198a000-0000-7000-8000-000000000031"
		meterID    = "0198a000-0000-7000-8000-000000000032"
		priceID    = "0198a000-0000-7000-8000-000000000033"
		serviceID  = "0198a000-0000-7000-8000-000000000034"
		reserveID  = "0198a000-0000-7000-8000-000000000041"
		inviteID   = "0198a000-0000-7000-8000-000000000042"
		recordID   = "0198a000-0000-7000-8000-000000000043"
		accountJob = "0198a000-0000-7000-8000-000000000051"
		orgJob     = "0198a000-0000-7000-8000-000000000052"
	)
	setup := []struct {
		statement string
		arguments []any
	}{
		{"INSERT INTO accounts (id, logto_subject) VALUES ($1, 'schema-a'), ($2, 'schema-b'), ($3, 'schema-c')", []any{accountA, accountB, accountC}},
		{"INSERT INTO organizations (id, name, slug) VALUES ($1, 'A', 'schema-a'), ($2, 'B', 'schema-b'), ($3, 'C', 'schema-c')", []any{orgA, orgB, orgC}},
		{"INSERT INTO organization_memberships (organization_id, account_id, role) VALUES ($1, $2, 'member'), ($3, $4, 'member')", []any{orgA, accountA, orgB, accountB}},
		{"INSERT INTO teams (id, organization_id, name) VALUES ($1, $2, 'A'), ($3, $4, 'B')", []any{teamA, orgA, teamB, orgB}},
		{"INSERT INTO catalog_apps (id, slug, name) VALUES ($1, 'schema-app', 'Schema App')", []any{appID}},
		{"INSERT INTO catalog_meters (id, app_id, meter_key, name, unit_name, reservation_ttl_seconds) VALUES ($1, $2, 'requests', 'Requests', 'request', 60)", []any{meterID, appID}},
		{"INSERT INTO catalog_price_versions (id, meter_id, usd_micros_per_unit, effective_from) VALUES ($1, $2, 1, transaction_timestamp())", []any{priceID, meterID}},
		{"INSERT INTO service_identities (id, logto_client_id, name) VALUES ($1, 'schema-service', 'Schema Service')", []any{serviceID}},
	}
	for _, item := range setup {
		if _, err := transaction.Exec(ctx, item.statement, item.arguments...); err != nil {
			t.Fatal(err)
		}
	}

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
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO organization_invitations (
			id, organization_id, token_hash, organization_role, target_team_id,
			team_role, created_by_account_id, expires_at
		) VALUES ($1, $2, decode(repeat('ab', 32), 'hex'), 'member', $3, 'member', $4, transaction_timestamp() + interval '1 day')
	`, inviteID, orgA, teamB, accountA)
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO usage_reservations (
			id, organization_id, team_id, team_name_snapshot, meter_id,
			price_version_id, account_id, service_identity_id, maximum_units,
			usd_micros_per_unit, maximum_cost_micros, held_credit_micros,
			held_overage_micros, client_reference, expires_at
		) VALUES ($1, $2, $3, 'B', $4, $5, $6, $7, 1, 1, 1, 1, 0, 'cross-team', transaction_timestamp() + interval '1 minute')
	`, reserveID, orgA, teamB, meterID, priceID, accountA, serviceID)
	if _, err := transaction.Exec(ctx, `
		INSERT INTO usage_reservations (
			id, organization_id, team_id, team_name_snapshot, meter_id,
			price_version_id, account_id, service_identity_id, maximum_units,
			usd_micros_per_unit, maximum_cost_micros, held_credit_micros,
			held_overage_micros, client_reference, status, expires_at, finalized_at
		) VALUES ($1, $2, $3, 'A', $4, $5, $6, $7, 1, 1, 1, 1, 0, 'valid', 'committed', transaction_timestamp() + interval '1 minute', transaction_timestamp())
	`, reserveID, orgA, teamA, meterID, priceID, accountA, serviceID); err != nil {
		t.Fatal(err)
	}
	requireConstraintFailure(t, ctx, transaction, `
		INSERT INTO usage_records (
			id, reservation_id, organization_id, team_id, team_name_snapshot,
			meter_id, account_id, service_identity_id, committed_units,
			total_cost_micros, credit_applied_micros, overage_applied_micros
		) VALUES ($1, $2, $3, $4, 'B', $5, $6, $7, 1, 1, 1, 0)
	`, recordID, reserveID, orgB, teamB, meterID, accountA, serviceID)

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
