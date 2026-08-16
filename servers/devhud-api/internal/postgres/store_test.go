package postgres

import (
	"context"
	"strings"
	"testing"
)

func TestNewPoolDoesNotExposeMalformedDatabaseURL(t *testing.T) {
	databaseURL := "postgres://devhud:super-secret@localhost/%zz"
	_, err := NewPool(context.Background(), databaseURL)
	if err == nil {
		t.Fatal("NewPool accepted a malformed database URL")
	}
	if got := err.Error(); got != "parse PostgreSQL configuration" {
		t.Fatalf("error = %q", got)
	} else if strings.Contains(got, databaseURL) || strings.Contains(got, "super-secret") {
		t.Fatalf("error exposed database credentials: %q", got)
	}
}

func TestNewSweeperPoolRequiresTwoConnections(t *testing.T) {
	_, err := NewSweeperPool(context.Background(), "postgres://unused?pool_max_conns=1")
	if err == nil {
		t.Fatal("NewSweeperPool accepted a one-connection pool")
	}
	if got := err.Error(); got != "sweeper PostgreSQL pool must allow at least 2 connections" {
		t.Fatalf("error = %q", got)
	}
}
