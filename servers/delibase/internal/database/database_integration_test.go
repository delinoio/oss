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
