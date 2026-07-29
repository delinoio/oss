package migrations

import (
	"context"
	"os"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/delinoio/oss/servers/internal/uuidv7"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestEmbeddedMigrationsAreStrictlyOrdered(t *testing.T) {
	t.Parallel()
	ordered, err := load(files)
	if err != nil {
		t.Fatal(err)
	}
	if len(ordered) != 6 {
		t.Fatalf("migration count = %d, want 6", len(ordered))
	}
	for index, item := range ordered {
		if item.version != int64(index+1) {
			t.Fatalf("migration %d version = %d", index, item.version)
		}
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
	if count != 6 {
		t.Fatalf("applied migration count = %d, want 6", count)
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
	for _, item := range ordered[:3] {
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
	results, err := connection.PgConn().Exec(ctx, ordered[3].contents).ReadAll()
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
