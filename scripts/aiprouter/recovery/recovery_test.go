// SPDX-License-Identifier: Apache-2.0
package recovery

import (
	"bytes"
	"context"
	"database/sql"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/packages/aiprouter/core"
	"github.com/delinoio/oss/packages/aiprouter/store"
)

// This test owns unique schemas, never the application's schema or other tests' data.
func TestBackupRestoreAndRotation(t *testing.T) {
	dsn := os.Getenv("AIPROUTER_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set AIPROUTER_TEST_DATABASE_URL for PostgreSQL recovery fixture")
	}
	for _, name := range []string{"pg_dump", "pg_restore"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	schema := "recovery_" + strings.ReplaceAll(core.ID(), "-", "")[:20]
	db, err := sql.Open("pgx", dsn)
	must(t, err)
	defer db.Close()
	defer func() {
		_, _ = db.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS "+schema+"_temp CASCADE; DROP SCHEMA IF EXISTS "+schema+" CASCADE")
	}()
	oldKey := bytes.Repeat([]byte{1}, 32)
	newKey := bytes.Repeat([]byte{2}, 32)
	open := func(active string, keys map[string][]byte) *store.Store {
		envelope, e := core.NewEnvelope(active, keys)
		must(t, e)
		s, e := store.Open(ctx, dsn, store.Options{Schema: schema, Keys: envelope})
		must(t, e)
		t.Cleanup(func() { _ = s.Close() })
		return s
	}
	s := open("v1", map[string][]byte{"v1": oldKey})
	must(t, s.Migrate(ctx))
	alice, err := s.RegisterIdentity(ctx, "http://127.0.0.1:18090", "alice", "alice@example.invalid", true)
	must(t, err)
	bob, err := s.RegisterIdentity(ctx, "http://127.0.0.1:18090", "bob", "bob@example.invalid", true)
	must(t, err)
	var scopes []core.Scope
	for i, user := range []string{alice.ID, alice.ID, bob.ID} {
		org, e := s.CreateOrganization(ctx, user, []string{"alpha", "beta", "gamma"}[i])
		must(t, e)
		sc, _, e := s.Membership(ctx, org.ID, user)
		must(t, e)
		scopes = append(scopes, sc)
		must(t, s.PutPrivate(ctx, sc, "fixture", "provider", []byte("synthetic-secret")))
		must(t, s.PutState(ctx, sc, core.Responses, "same-id", []byte("synthetic-temporary")))
	}
	_, raw, err := s.IssueKey(ctx, scopes[0], "fixture", nil)
	must(t, err)
	session, err := s.CreateSession(ctx, alice.ID)
	must(t, err)
	invitation, err := s.Invite(ctx, scopes[0], "new@example.invalid")
	must(t, err)
	device, err := s.StartDevice(ctx, "fixture", "http://127.0.0.1:8080")
	must(t, err)
	_, err = s.ApproveDevice(ctx, scopes[0], device.Code, nil)
	must(t, err)
	// Rotate with both keys, then prove reads work with only the new key.
	rotated := open("v2", map[string][]byte{"v1": oldKey, "v2": newKey})
	must(t, rotated.RotateEncryption(ctx))
	current := open("v2", map[string][]byte{"v2": newKey})
	for _, sc := range scopes {
		value, e := current.GetPrivate(ctx, sc, "fixture", "provider")
		must(t, e)
		if string(value) != "synthetic-secret" {
			t.Fatal("private data changed")
		}
		_, e = current.GetState(ctx, sc, core.Responses, "same-id")
		must(t, e)
	}
	archive := filepath.Join(t.TempDir(), "backup.dump")
	connection, err := url.Parse(dsn)
	must(t, err)
	pgEnv := append(os.Environ(), "PGHOST="+connection.Hostname(), "PGPORT="+connection.Port(), "PGDATABASE="+strings.TrimPrefix(connection.Path, "/"), "PGSSLMODE="+connection.Query().Get("sslmode"))
	if connection.User != nil {
		password, _ := connection.User.Password()
		pgEnv = append(pgEnv, "PGUSER="+connection.User.Username(), "PGPASSWORD="+password)
	}
	command := func(name string, args ...string) []byte {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Env = pgEnv
		out, e := cmd.CombinedOutput()
		if e != nil {
			t.Fatalf("%s failed: %v %s", name, e, out)
		}
		return out
	}
	command("pg_dump", "--format=custom", "--schema="+schema, "--exclude-schema="+schema+"_temp", "--no-owner", "--no-acl", "--file="+archive)
	listing := command("pg_restore", "--list", archive)
	if bytes.Contains(listing, []byte(schema+"_temp")) {
		t.Fatal("temporary schema included")
	}
	// Drop schemas while no admission listener exists. Restore the matching backup.
	_, err = db.ExecContext(ctx, "DROP SCHEMA "+schema+"_temp CASCADE; DROP SCHEMA "+schema+" CASCADE")
	must(t, err)
	command("pg_restore", "--dbname="+strings.TrimPrefix(connection.Path, "/"), "--single-transaction", "--exit-on-error", "--no-owner", "--no-acl", archive)
	must(t, current.Migrate(ctx))
	must(t, current.InvalidateAfterRestore(ctx))
	if _, err = current.AuthenticateKey(ctx, raw); err == nil {
		t.Fatal("restored key still works")
	}
	if _, err = current.AuthenticateSession(ctx, session); err == nil {
		t.Fatal("restored session still works")
	}
	var pending int
	must(t, db.QueryRowContext(ctx, "SELECT count(*) FROM "+schema+".invitations WHERE id=$1 AND status='pending'", invitation.ID).Scan(&pending))
	if pending != 0 {
		t.Fatal("restored invitation active")
	}
	must(t, db.QueryRowContext(ctx, "SELECT count(*) FROM "+schema+"_temp.devices").Scan(&pending))
	if pending != 0 {
		t.Fatal("device authority survived")
	}
	for _, sc := range scopes {
		if _, err = current.GetState(ctx, sc, core.Responses, "same-id"); err == nil {
			t.Fatal("temporary state survived restore")
		}
		_, err = current.GetPrivate(ctx, sc, "fixture", "provider")
		must(t, err)
	}
}
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
