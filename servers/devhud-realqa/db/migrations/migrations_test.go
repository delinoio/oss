package migrations

import (
	"bytes"
	"context"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/delinoio/oss/servers/internal/uuidv7"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestEmbeddedMigrationsAreStrictlyOrdered(t *testing.T) {
	t.Parallel()
	ordered, err := load(files)
	if err != nil {
		t.Fatal(err)
	}
	if len(ordered) != 9 {
		t.Fatalf("migration count = %d, want 9", len(ordered))
	}
	for index, item := range ordered {
		if item.version != int64(index+1) {
			t.Fatalf("migration %d version = %d", index, item.version)
		}
	}
}

func TestRecurringStorageMigrationBackfillsDeletedAssetsForClosure(t *testing.T) {
	databaseURL := os.Getenv("REALQA_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("REALQA_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	schema := "realqa_backfill_" + uuidv7.MustNew().String()[24:]
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err = connection.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		t.Fatal(err)
	}
	_ = connection.Close(ctx)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(
			context.Background(), 5*time.Second)
		defer cleanupCancel()
		cleanup, cleanupErr := pgx.Connect(cleanupCtx, databaseURL)
		if cleanupErr == nil {
			_, _ = cleanup.Exec(
				cleanupCtx, "DROP SCHEMA "+identifier+" CASCADE")
			_ = cleanup.Close(cleanupCtx)
		}
	})
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	acquired, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer acquired.Release()
	if _, err = acquired.Exec(ctx, `
		CREATE TABLE realqa_schema_migrations (
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
	for _, item := range ordered[:8] {
		if err = apply(ctx, acquired.Conn(), item); err != nil {
			t.Fatal(err)
		}
	}

	accountID := uuidv7.MustNew()
	organizationID := uuidv7.MustNew()
	teamID := uuidv7.MustNew()
	serviceID := uuidv7.MustNew()
	meterID := uuidv7.MustNew()
	deletedSubmissionID := uuidv7.MustNew()
	inflightSubmissionID := uuidv7.MustNew()
	deletedAuthorizationID := uuidv7.MustNew()
	inflightAuthorizationID := uuidv7.MustNew()
	if _, err = acquired.Exec(ctx, `
		INSERT INTO realqa_identities (account_id, subject_digest)
		VALUES ($1, $2)
	`, accountID, bytes.Repeat([]byte{1}, 32)); err != nil {
		t.Fatal(err)
	}
	if _, err = acquired.Exec(ctx, `
		INSERT INTO realqa_submissions (
			id, owner_kind, owner_id, created_by_account_id, state,
			idempotency_digest, payer_organization_id, payer_team_id,
			created_at, updated_at, upload_deadline, upload_expires_at,
			submitted_at
		) VALUES
			(
				$2, 'personal', $1, $1, 'submitted', $3, $4, $5,
				transaction_timestamp() - interval '2 hours',
				transaction_timestamp() - interval '1 hour',
				transaction_timestamp() + interval '21 hours',
				transaction_timestamp() + interval '22 hours',
				transaction_timestamp() - interval '1 hour'
			),
			(
				$6, 'personal', $1, $1, 'submitting', $7, $4, $5,
				transaction_timestamp() - interval '2 hours',
				transaction_timestamp() - interval '1 hour',
				transaction_timestamp() + interval '21 hours',
				transaction_timestamp() + interval '22 hours',
				NULL
			)
	`, accountID, deletedSubmissionID, bytes.Repeat([]byte{2}, 32),
		organizationID, teamID, inflightSubmissionID,
		bytes.Repeat([]byte{3}, 32)); err != nil {
		t.Fatal(err)
	}
	if _, err = acquired.Exec(ctx, `
		INSERT INTO realqa_storage_authorization_attempts (
			submission_id, idempotency_key, request_digest,
			service_identity_id, meter_id, maximum_units, state,
			authorization_id, authorization_revision, mapping_revision
		) VALUES
			($1, $2, $3, $4, $5, 1, 'active', $6, 1, 1),
			($7, $8, $9, $4, $5, 1, 'active', $10, 1, 1)
	`, deletedSubmissionID, uuidv7.MustNew(),
		bytes.Repeat([]byte{4}, 32), serviceID, meterID,
		deletedAuthorizationID, inflightSubmissionID, uuidv7.MustNew(),
		bytes.Repeat([]byte{5}, 32),
		inflightAuthorizationID); err != nil {
		t.Fatal(err)
	}
	if err = apply(ctx, acquired.Conn(), ordered[8]); err != nil {
		t.Fatal(err)
	}

	var (
		deletedClosureState  string
		deletedCutoff        time.Time
		deletedSubmissionCut time.Time
		inflightClosureState string
		inflightCutoff       pgtype.Timestamptz
	)
	if err = acquired.QueryRow(ctx, `
		SELECT binding.closure_state, binding.accrual_cutoff_at,
		       submission.updated_at
		FROM realqa_storage_authorization_bindings AS binding
		JOIN realqa_submissions AS submission
		  ON submission.id = binding.submission_id
		WHERE binding.authorization_id = $1
	`, deletedAuthorizationID).Scan(
		&deletedClosureState, &deletedCutoff, &deletedSubmissionCut,
	); err != nil {
		t.Fatal(err)
	}
	if deletedClosureState != "resource_deletion_pending" ||
		!deletedCutoff.Equal(deletedSubmissionCut) {
		t.Fatalf("deleted backfill = %q / %v, want closure pending at %v",
			deletedClosureState, deletedCutoff, deletedSubmissionCut)
	}
	if err = acquired.QueryRow(ctx, `
		SELECT closure_state, accrual_cutoff_at
		FROM realqa_storage_authorization_bindings
		WHERE authorization_id = $1
	`, inflightAuthorizationID).Scan(
		&inflightClosureState, &inflightCutoff,
	); err != nil {
		t.Fatal(err)
	}
	if inflightClosureState != "open" || inflightCutoff.Valid {
		t.Fatalf("inflight backfill = %q / %v, want open without cutoff",
			inflightClosureState, inflightCutoff)
	}
}

func TestPostgreSQLMigrationsAreConcurrentAndIdempotent(t *testing.T) {
	databaseURL := os.Getenv("REALQA_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("REALQA_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	schema := "realqa_migration_" + uuidv7.MustNew().String()[24:]
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err = connection.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		t.Fatal(err)
	}
	_ = connection.Close(ctx)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(
			context.Background(), 5*time.Second)
		defer cleanupCancel()
		cleanup, cleanupErr := pgx.Connect(cleanupCtx, databaseURL)
		if cleanupErr == nil {
			_, _ = cleanup.Exec(cleanupCtx, "DROP SCHEMA "+identifier+" CASCADE")
			_ = cleanup.Close(cleanupCtx)
		}
	})
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	const workers = 4
	var group sync.WaitGroup
	failures := make(chan error, workers)
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			failures <- Run(ctx, pool)
		}()
	}
	group.Wait()
	close(failures)
	for migrationErr := range failures {
		if migrationErr != nil {
			t.Fatal(migrationErr)
		}
	}
	if err = Run(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = pool.QueryRow(ctx,
		"SELECT count(*) FROM realqa_schema_migrations").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 9 {
		t.Fatalf("applied migration count = %d, want 9", count)
	}
	for _, operation := range []string{
		"create_submission",
		"create_image_upload",
		"finalize_image_upload",
		"submit_issue",
		"rebind_submission_storage_authorization",
		"delete_image",
		"delete_submission_assets",
	} {
		if _, err = pool.Exec(ctx, `
			INSERT INTO realqa_idempotency_records (
				id, caller_kind, caller_digest, operation, idempotency_key,
				request_digest, resource_id
			) VALUES ($1, 'user', $2, $3, $4, $5, $6)
		`, uuidv7.MustNew(), bytes.Repeat([]byte{1}, 32), operation,
			uuidv7.MustNew(), bytes.Repeat([]byte{2}, 32),
			uuidv7.MustNew()); err != nil {
			t.Fatalf("insert %s idempotency record: %v", operation, err)
		}
	}
	for _, eventType := range []string{
		"submission_created",
		"transfer_reserved",
		"image_upload_authorized",
		"image_upload_verified",
		"transfer_committed",
		"transfer_released",
		"storage_authorization_created",
		"storage_daily_reserved",
		"storage_daily_committed",
		"storage_daily_released",
		"storage_billing_grace_started",
		"storage_authorization_rebound",
		"storage_rebind_replacement_closed",
		"storage_authorization_closed",
		"issue_submission_started",
		"issue_reconciled",
		"submission_completed",
		"image_deleted",
		"submission_assets_deleted",
	} {
		if _, err = pool.Exec(ctx, `
			INSERT INTO realqa_audits (
				id, event_type, actor_reference, decision, result
			) VALUES ($1, $2, 'system', 'allow', 'success')
		`, uuidv7.MustNew(), eventType); err != nil {
			t.Fatalf("insert %s audit: %v", eventType, err)
		}
	}
	if _, err = pool.Exec(ctx, `
		INSERT INTO realqa_audits (
			id, event_type, actor_reference, decision, result
		) VALUES ($1, 'github_user_authorization_started', 'system', 'allow', 'success')
	`, uuidv7.MustNew()); err != nil {
		t.Fatalf("member GitHub authorization audit event was rejected: %v", err)
	}
	if _, err = pool.Exec(ctx, `
		INSERT INTO realqa_github_connections (
			id, owner_kind, owner_id, state
		) VALUES ($1, 'personal', $2, 'connected')
	`, uuidv7.MustNew(), uuidv7.MustNew()); err == nil {
		t.Fatal("connected GitHub connection accepted without encrypted credentials")
	}
	t.Run("recurring storage UTC concurrency and grace", func(t *testing.T) {
		accountID := uuidv7.MustNew()
		submissionID := uuidv7.MustNew()
		authorizationID := uuidv7.MustNew()
		organizationID := uuidv7.MustNew()
		teamID := uuidv7.MustNew()
		serviceID := uuidv7.MustNew()
		meterID := uuidv7.MustNew()
		if _, err = pool.Exec(ctx, `
			INSERT INTO realqa_identities (account_id, subject_digest)
			VALUES ($1, $2)
		`, accountID, bytes.Repeat([]byte{3}, 32)); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `
			INSERT INTO realqa_submissions (
				id, owner_kind, owner_id, created_by_account_id, state,
				idempotency_digest, payer_organization_id, payer_team_id,
				upload_deadline, upload_expires_at, submitted_at
			) VALUES (
				$1, 'personal', $2, $2, 'submitted', $3, $4, $5,
				transaction_timestamp() + interval '23 hours',
				transaction_timestamp() + interval '24 hours',
				transaction_timestamp()
			)
		`, submissionID, accountID, bytes.Repeat([]byte{4}, 32),
			organizationID, teamID); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `
			INSERT INTO realqa_storage_authorization_attempts (
				submission_id, idempotency_key, request_digest,
				service_identity_id, meter_id, maximum_units, state,
				authorization_id, authorization_revision, mapping_revision
			) VALUES (
				$1, $2, $3, $4, $5, 2, 'active', $6, 1, 1
			)
		`, submissionID, uuidv7.MustNew(),
			bytes.Repeat([]byte{5}, 32), serviceID, meterID,
			authorizationID); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `
			INSERT INTO realqa_storage_authorization_bindings (
				authorization_id, submission_id, mapping_revision,
				authorizer_account_id, owner_kind, owner_id,
				organization_id, team_id, service_identity_id, meter_id,
				maximum_units, status, authorization_revision
			) VALUES (
				$1, $2, 1, $3, 'personal', $3, $4, $5, $6, $7,
				2, 'active', 1
			)
		`, authorizationID, submissionID, accountID, organizationID,
			teamID, serviceID, meterID); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `
			INSERT INTO realqa_storage_retention_intervals (
				authorization_id, asset_id, retained_bytes,
				starts_at, ends_at
			) VALUES
				($1, $2, 1048576,
				 '2030-01-01 23:30:00+00',
				 '2030-01-02 00:30:00+00'),
				($1, $3, 2097152,
				 '2030-01-02 12:00:00+00',
				 '2030-01-02 12:00:01.5+00')
		`, authorizationID, uuidv7.MustNew(),
			uuidv7.MustNew()); err != nil {
			t.Fatal(err)
		}
		byteSecondsFor := func(
			start string,
			end string,
		) int64 {
			var byteSeconds int64
			if queryErr := pool.QueryRow(ctx, `
				SELECT COALESCE(
					CEIL(SUM(
						retained_bytes::numeric
						* EXTRACT(EPOCH FROM (
							LEAST(COALESCE(ends_at, $3), $3)
							- GREATEST(starts_at, $2)
						))
					)), 0
				)::bigint
				FROM realqa_storage_retention_intervals
				WHERE authorization_id = $1
				  AND starts_at < $3
				  AND COALESCE(ends_at, $3) > $2
			`, authorizationID, start, end).Scan(&byteSeconds); queryErr != nil {
				t.Fatal(queryErr)
			}
			return byteSeconds
		}
		if got, want := byteSecondsFor(
			"2030-01-01 00:00:00+00",
			"2030-01-02 00:00:00+00",
		), int64(1800*1048576); got != want {
			t.Fatalf("first UTC day byte-seconds = %d, want %d", got, want)
		}
		if got, want := byteSecondsFor(
			"2030-01-02 00:00:00+00",
			"2030-01-03 00:00:00+00",
		), int64(1803*1048576); got != want {
			t.Fatalf("second UTC day byte-seconds = %d, want %d", got, want)
		}

		const contenders = 8
		inserted := make(chan int64, contenders)
		failures := make(chan error, contenders)
		var checkpointGroup sync.WaitGroup
		for range contenders {
			checkpointGroup.Add(1)
			go func() {
				defer checkpointGroup.Done()
				tag, insertErr := pool.Exec(ctx, `
					INSERT INTO realqa_storage_daily_settlements (
						authorization_id, period_start, byte_seconds,
						units, state, request_digest,
						reserve_idempotency_key,
						commit_idempotency_key,
						release_idempotency_key
					) VALUES (
						$1, '2030-01-01 00:00:00+00',
						$2, 1, 'pending', $3, $4, $5, $6
					)
					ON CONFLICT (authorization_id, period_start)
					DO NOTHING
				`, authorizationID, int64(1800*1048576),
					bytes.Repeat([]byte{6}, 32),
					uuidv7.MustNew(), uuidv7.MustNew(), uuidv7.MustNew())
				if insertErr != nil {
					failures <- insertErr
					return
				}
				inserted <- tag.RowsAffected()
			}()
		}
		checkpointGroup.Wait()
		close(inserted)
		close(failures)
		for insertErr := range failures {
			t.Fatal(insertErr)
		}
		var insertedRows int64
		for count := range inserted {
			insertedRows += count
		}
		if insertedRows != 1 {
			t.Fatalf("concurrent checkpoint inserts = %d, want 1",
				insertedRows)
		}
		if _, err = pool.Exec(ctx, `
			UPDATE realqa_storage_daily_settlements
			SET request_digest = $3
			WHERE authorization_id = $1
			  AND period_start = $2
		`, authorizationID, "2030-01-01 00:00:00+00",
			bytes.Repeat([]byte{7}, 32)); err == nil {
			t.Fatal("daily settlement digest mutation was accepted")
		}

		graceStart := time.Date(
			2030, time.January, 3, 4, 5, 6, 0, time.UTC)
		if _, err = pool.Exec(ctx, `
			INSERT INTO realqa_storage_recoveries (
				id, submission_id, authorization_id, reason,
				grace_started_at, grace_expires_at
			) VALUES (
				$1, $2, $3, 'payment_required',
				$4::timestamptz, $4::timestamptz + interval '30 days'
			)
		`, uuidv7.MustNew(), submissionID, authorizationID,
			graceStart); err != nil {
			t.Fatal(err)
		}
		var graceExpires time.Time
		if err = pool.QueryRow(ctx, `
			SELECT grace_expires_at
			FROM realqa_storage_recoveries
			WHERE submission_id = $1
			  AND recovered_at IS NULL
			  AND expired_at IS NULL
		`, submissionID).Scan(&graceExpires); err != nil {
			t.Fatal(err)
		}
		if !graceExpires.Equal(graceStart.Add(30 * 24 * time.Hour)) {
			t.Fatalf("grace expiry = %s", graceExpires)
		}

		firstRebindKey := uuidv7.MustNew()
		secondRebindKey := uuidv7.MustNew()
		insertRebind := func(
			callerDigest []byte,
			idempotencyKey uuid.UUID,
		) error {
			_, insertErr := pool.Exec(ctx, `
					INSERT INTO realqa_storage_rebind_attempts (
						submission_id, caller_digest, idempotency_key,
						request_digest, expected_authorization_id,
						expected_mapping_revision,
						replacement_organization_id, replacement_team_id,
						replacement_maximum_units,
						replacement_service_identity_id, replacement_meter_id,
						revoke_idempotency_key, create_idempotency_key, state
					) VALUES (
						$1, $2, $3, $4, $5, 1, $6, $7, 2, $8, $9,
						$10, $11, 'pending'
					)
				`, submissionID, callerDigest, idempotencyKey,
				bytes.Repeat([]byte{8}, 32), authorizationID,
				organizationID, teamID, serviceID, meterID,
				uuidv7.MustNew(), uuidv7.MustNew())
			return insertErr
		}
		firstCallerDigest := bytes.Repeat([]byte{9}, 32)
		if err = insertRebind(
			firstCallerDigest, firstRebindKey); err != nil {
			t.Fatal(err)
		}
		if err = insertRebind(
			bytes.Repeat([]byte{10}, 32), secondRebindKey); err == nil {
			t.Fatal("distinct pending rebind was accepted")
		}
		if _, err = pool.Exec(ctx, `
				UPDATE realqa_storage_rebind_attempts
				SET state = 'closed',
				    replacement_authorization_id = $3,
				    replacement_authorization_revision = 1,
				    completed_at = transaction_timestamp()
				WHERE caller_digest = $1
				  AND idempotency_key = $2
			`, firstCallerDigest, firstRebindKey,
			uuidv7.MustNew()); err != nil {
			t.Fatal(err)
		}
		if err = insertRebind(
			bytes.Repeat([]byte{10}, 32), secondRebindKey); err != nil {
			t.Fatalf("new rebind after closed cleanup: %v", err)
		}
		if _, err = pool.Exec(ctx, `
				UPDATE realqa_storage_rebind_attempts
				SET state = 'owner_deleted',
				    completed_at = transaction_timestamp()
				WHERE caller_digest = $1
				  AND idempotency_key = $2
			`, bytes.Repeat([]byte{10}, 32), secondRebindKey); err != nil {
			t.Fatal(err)
		}
		if err = insertRebind(
			bytes.Repeat([]byte{11}, 32),
			uuidv7.MustNew(),
		); err != nil {
			t.Fatalf("new rebind after owner deletion: %v", err)
		}
	})
}

func TestImageStorageMigrationNormalizesLegacyAssetsAndBackfillsTotals(
	t *testing.T,
) {
	databaseURL := os.Getenv("REALQA_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("REALQA_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	schema := "realqa_upgrade_" + uuidv7.MustNew().String()[24:]
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err = connection.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		t.Fatal(err)
	}
	_ = connection.Close(ctx)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(
			context.Background(), 5*time.Second)
		defer cleanupCancel()
		cleanup, cleanupErr := pgx.Connect(cleanupCtx, databaseURL)
		if cleanupErr == nil {
			_, _ = cleanup.Exec(
				cleanupCtx, "DROP SCHEMA "+identifier+" CASCADE")
			_ = cleanup.Close(cleanupCtx)
		}
	})
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	ordered, err := load(files)
	if err != nil {
		t.Fatal(err)
	}
	acquired, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = acquired.Exec(ctx, `
		CREATE TABLE realqa_schema_migrations (
			version bigint PRIMARY KEY,
			name text NOT NULL UNIQUE,
			checksum bytea NOT NULL CHECK (octet_length(checksum) = 32),
			applied_at timestamptz NOT NULL DEFAULT transaction_timestamp()
		)
	`); err != nil {
		acquired.Release()
		t.Fatal(err)
	}
	for _, item := range ordered[:3] {
		if err = apply(ctx, acquired.Conn(), item); err != nil {
			acquired.Release()
			t.Fatal(err)
		}
	}
	acquired.Release()

	accountID := uuidv7.MustNew()
	submissionID := uuidv7.MustNew()
	overflowSubmissionID := uuidv7.MustNew()
	if _, err = pool.Exec(ctx, `
		INSERT INTO realqa_identities (account_id, subject_digest)
		VALUES ($1, $2)
	`, accountID, bytes.Repeat([]byte{1}, 32)); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `
		INSERT INTO realqa_submissions (
			id, owner_kind, owner_id, created_by_account_id, state,
			idempotency_digest, submitted_at
		) VALUES
			($2, 'personal', $1, $1, 'submitted', $4,
			 transaction_timestamp()),
			($3, 'personal', $1, $1, 'submitted', $4,
			 transaction_timestamp())
	`, accountID, submissionID, overflowSubmissionID,
		bytes.Repeat([]byte{1}, 32)); err != nil {
		t.Fatal(err)
	}
	publicID := strings.Repeat("A", 22)
	legacyRows := []struct {
		id         uuid.UUID
		state      string
		size       int64
		publicID   *string
		submission uuid.UUID
	}{
		{uuidv7.MustNew(), "public_retained", 10, nil, submissionID},
		{uuidv7.MustNew(), "verified_unlinked", 20, nil, submissionID},
		{uuidv7.MustNew(), "public_retained", 0, &publicID, submissionID},
		{uuidv7.MustNew(), "verified_unlinked", 26214401, nil, submissionID},
	}
	for _, row := range legacyRows {
		if _, err = pool.Exec(ctx, `
			INSERT INTO realqa_assets (
				id, submission_id, public_id, state, encoded_bytes
			) VALUES ($1, $2, $3, $4, $5)
		`, row.id, row.submission, row.publicID, row.state, row.size); err != nil {
			t.Fatal(err)
		}
	}
	overflowAssetIDs := make([]uuid.UUID, 11)
	for index := range overflowAssetIDs {
		overflowAssetIDs[index] = uuidv7.MustNew()
	}
	sort.Slice(overflowAssetIDs, func(left, right int) bool {
		return overflowAssetIDs[left].String() <
			overflowAssetIDs[right].String()
	})
	for _, assetID := range overflowAssetIDs {
		if _, err = pool.Exec(ctx, `
			INSERT INTO realqa_assets (
				id, submission_id, state, encoded_bytes
			) VALUES ($1, $2, 'verified_unlinked', 26214400)
		`, assetID, overflowSubmissionID); err != nil {
			t.Fatal(err)
		}
	}
	if err = Run(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var declared, verified int64
	if err = pool.QueryRow(ctx, `
		SELECT declared_encoded_bytes, verified_encoded_bytes
		FROM realqa_submissions
		WHERE id = $1
	`, submissionID).Scan(&declared, &verified); err != nil {
		t.Fatal(err)
	}
	if declared != 30 || verified != 30 {
		t.Fatalf("legacy totals = %d / %d, want 30 / 30",
			declared, verified)
	}
	var invalidCount int
	if err = pool.QueryRow(ctx, `
		SELECT count(*)
		FROM realqa_assets
		WHERE submission_id = $1
		  AND upload_state = 'deleted'
		  AND declared_encoded_bytes = 1
	`, submissionID).Scan(&invalidCount); err != nil {
		t.Fatal(err)
	}
	if invalidCount != 2 {
		t.Fatalf("terminal invalid legacy assets = %d, want 2", invalidCount)
	}
	if err = pool.QueryRow(ctx, `
		SELECT declared_encoded_bytes, verified_encoded_bytes
		FROM realqa_submissions
		WHERE id = $1
	`, overflowSubmissionID).Scan(&declared, &verified); err != nil {
		t.Fatal(err)
	}
	if declared != 262144000 || verified != 262144000 {
		t.Fatalf("bounded legacy totals = %d / %d, want 262144000 / 262144000",
			declared, verified)
	}
	if err = pool.QueryRow(ctx, `
		SELECT count(*)
		FROM realqa_assets
		WHERE submission_id = $1
		  AND upload_state = 'deleted'
	`, overflowSubmissionID).Scan(&invalidCount); err != nil {
		t.Fatal(err)
	}
	if invalidCount != 1 {
		t.Fatalf("overflow legacy assets = %d, want 1", invalidCount)
	}
	var tombstones int
	if err = pool.QueryRow(ctx, `
		SELECT count(*)
		FROM realqa_public_asset_tombstones
		WHERE public_id = $1
	`, publicID).Scan(&tombstones); err != nil {
		t.Fatal(err)
	}
	if tombstones != 1 {
		t.Fatalf("invalid public legacy tombstones = %d, want 1", tombstones)
	}
}

func TestGitHubProviderMigrationRepairsLegacyConnections(t *testing.T) {
	databaseURL := os.Getenv("REALQA_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("REALQA_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(context.Background())
	schema := "realqa_github_migration_" + uuidv7.MustNew().String()[24:]
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err = connection.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(
			context.Background(), 5*time.Second)
		defer cleanupCancel()
		cleanup, cleanupErr := pgx.Connect(cleanupCtx, databaseURL)
		if cleanupErr == nil {
			_, _ = cleanup.Exec(cleanupCtx, "DROP SCHEMA "+identifier+" CASCADE")
			_ = cleanup.Close(cleanupCtx)
		}
	})
	if _, err = connection.Exec(ctx, "SET search_path = "+identifier+", public"); err != nil {
		t.Fatal(err)
	}
	ordered, err := load(files)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range ordered[:4] {
		results, migrationErr := connection.PgConn().Exec(ctx, item.contents).ReadAll()
		if migrationErr != nil {
			t.Fatalf("apply migration %d: %v", item.version, migrationErr)
		}
		for _, result := range results {
			if result.Err != nil {
				t.Fatalf("apply migration %d: %v", item.version, result.Err)
			}
		}
	}
	personalID := uuidv7.MustNew()
	organizationID := uuidv7.MustNew()
	personalConnectionID := uuidv7.MustNew()
	organizationConnectionID := uuidv7.MustNew()
	if _, err = connection.Exec(ctx, `
		INSERT INTO realqa_identities (account_id, subject_digest)
		VALUES ($1, decode(repeat('01', 32), 'hex'))
	`, personalID); err != nil {
		t.Fatal(err)
	}
	if _, err = connection.Exec(ctx, `
		INSERT INTO realqa_github_connections (
			id, owner_kind, owner_id, state, github_login,
			credential_ciphertext, wrapped_data_key, key_id, connected_at
		) VALUES
			($1, 'personal', $2, 'connected', 'personal-user',
			 decode('01', 'hex'), decode('02', 'hex'), 'fixture-key',
			 transaction_timestamp()),
			($3, 'organization', $4, 'connected', 'organization-user',
			 decode('03', 'hex'), decode('04', 'hex'), 'fixture-key',
			 transaction_timestamp());
	`, personalConnectionID, personalID, organizationConnectionID, organizationID); err != nil {
		t.Fatal(err)
	}
	results, err := connection.PgConn().Exec(ctx, ordered[4].contents).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range results {
		if result.Err != nil {
			t.Fatal(result.Err)
		}
	}
	var personalState string
	var personalConnector uuid.NullUUID
	var personalCredential []byte
	if err = connection.QueryRow(ctx, `
		SELECT state, connected_by_account_id, credential_ciphertext
		FROM realqa_github_connections
		WHERE id = $1
	`, personalConnectionID).Scan(
		&personalState, &personalConnector, &personalCredential,
	); err != nil {
		t.Fatal(err)
	}
	if personalState != "disconnected" || personalConnector.Valid ||
		personalCredential != nil {
		t.Fatalf(
			"personal connection state=%q connector=%v credential=%t",
			personalState, personalConnector.Valid, personalCredential != nil,
		)
	}
	var organizationState string
	var organizationConnector uuid.NullUUID
	var organizationCredential []byte
	if err = connection.QueryRow(ctx, `
		SELECT state, connected_by_account_id, credential_ciphertext
		FROM realqa_github_connections
		WHERE id = $1
	`, organizationConnectionID).Scan(
		&organizationState, &organizationConnector, &organizationCredential,
	); err != nil {
		t.Fatal(err)
	}
	if organizationState != "disconnected" || organizationConnector.Valid ||
		organizationCredential != nil {
		t.Fatalf(
			"organization connection state=%q connector=%v credential=%t",
			organizationState, organizationConnector.Valid, organizationCredential != nil,
		)
	}
}

func TestMigrationLoaderRejectsInvalidSets(t *testing.T) {
	t.Parallel()
	tests := []fstest.MapFS{
		{},
		{"bad.sql": &fstest.MapFile{Data: []byte("SELECT 1")}},
		{"000002_gap.sql": &fstest.MapFile{Data: []byte("SELECT 1")}},
		{
			"000001_a.sql": &fstest.MapFile{Data: []byte("SELECT 1")},
			"000001_b.sql": &fstest.MapFile{Data: []byte("SELECT 2")},
		},
	}
	for _, source := range tests {
		if _, err := load(source); err == nil {
			t.Fatal("load() accepted invalid migration set")
		}
	}
}
