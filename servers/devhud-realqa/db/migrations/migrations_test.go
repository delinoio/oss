package migrations

import (
	"context"
	"os"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/delinoio/oss/servers/internal/uuidv7"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestEmbeddedMigrationsAreStrictlyOrdered(t *testing.T) {
	t.Parallel()
	ordered, err := load(files)
	if err != nil {
		t.Fatal(err)
	}
	if len(ordered) != 4 {
		t.Fatalf("migration count = %d, want 4", len(ordered))
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
	if count != 4 {
		t.Fatalf("applied migration count = %d, want 4", count)
	}
	if _, err = pool.Exec(ctx, `
		INSERT INTO realqa_github_connections (
			id, owner_kind, owner_id, state
		) VALUES ($1, 'personal', $2, 'connected')
	`, uuidv7.MustNew(), uuidv7.MustNew()); err == nil {
		t.Fatal("connected GitHub connection accepted without encrypted credentials")
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
