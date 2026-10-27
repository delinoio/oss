// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"github.com/delinoio/oss/packages/aiprouter/core"
	_ "github.com/jackc/pgx/v5/stdlib"
	"regexp"
	"strings"
	"time"
)

var (
	ErrForbidden    = errors.New("permission denied")
	ErrNotFound     = errors.New("not found")
	ErrExpired      = errors.New("expired")
	ErrConflict     = errors.New("conflict")
	ErrContinuation = errors.New("continuation missing, expired or evicted")
	ErrPending      = errors.New("authorization pending")
)

type LimitError struct{ RetryAfter time.Duration }

func (e *LimitError) Error() string { return "rate or concurrency limit exceeded" }

type Options struct {
	Schema     string
	InstanceID string
	Keys       *core.Envelope
	Operators  []core.Identity
}
type Store struct {
	instanceID string
	db         *sql.DB
	schema     string
	temp       string
	keys       *core.Envelope
	operators  map[core.Identity]bool
}

//go:embed migrations/001.sql
var migration string

//go:embed migrations/002.sql
var migration2 string

//go:embed migrations/003.sql
var migration3 string

//go:embed migrations/004.sql
var migration4 string

//go:embed migrations/005.sql
var migration5 string

//go:embed migrations/006.sql
var migration6 string

//go:embed migrations/007.sql
var migration7 string

//go:embed migrations/008.sql
var migration8 string

//go:embed migrations/009.sql
var migration9 string

//go:embed migrations/010.sql
var migration10 string

//go:embed migrations/011.sql
var migration11 string

//go:embed migrations/012.sql
var migration12 string

func Open(ctx context.Context, dsn string, o Options) (*Store, error) {
	if o.Schema == "" {
		o.Schema = "aiprouter"
	}
	if !regexp.MustCompile(`^[a-z][a-z0-9_]{0,40}$`).MatchString(o.Schema) || o.Keys == nil {
		return nil, errors.New("invalid schema or missing encryption keys")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	if o.InstanceID == "" {
		o.InstanceID = core.ID()
	}
	s := &Store{instanceID: o.InstanceID, db: db, schema: o.Schema, temp: o.Schema + "_temp", keys: o.Keys, operators: map[core.Identity]bool{}}
	for _, i := range o.Operators {
		s.operators[i] = true
	}
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) Close() error                   { return s.db.Close() }
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }
func (s *Store) Migrate(ctx context.Context) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, e := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", s.schema+":migration"); e != nil {
			return e
		}
		_, e := tx.ExecContext(ctx, strings.NewReplacer("{{schema}}", s.schema, "{{temp}}", s.temp).Replace(migration+"\n"+migration2+"\n"+migration3+"\n"+migration4+"\n"+migration5+"\n"+migration6+"\n"+migration7+"\n"+migration8+"\n"+migration9+"\n"+migration10+"\n"+migration11+"\n"+migration12))
		return e
	})
}
func (s *Store) tx(ctx context.Context, f func(*sql.Tx) error) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, "SET LOCAL search_path TO "+s.schema+", public"); e != nil {
		return e
	}
	if e = f(tx); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) lock(ctx context.Context, tx *sql.Tx, org string) error {
	_, e := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", s.schema+":org:"+org)
	return e
}
func (s *Store) guard(ctx context.Context, tx *sql.Tx, sc core.Scope) (core.Role, error) {
	if e := s.lock(ctx, tx, sc.OrganizationID); e != nil {
		return "", e
	}
	var r core.Role
	var g int64
	e := tx.QueryRowContext(ctx, "SELECT role,generation FROM memberships WHERE org_id=$1 AND user_id=$2", sc.OrganizationID, sc.UserID).Scan(&r, &g)
	if errors.Is(e, sql.ErrNoRows) || e == nil && g != sc.Generation {
		return "", ErrForbidden
	}
	return r, e
}
func elevated(r core.Role) bool { return r == core.Owner || r == core.Administrator }
func (s *Store) audit(ctx context.Context, tx *sql.Tx, org, actor, action, target string) error {
	_, e := tx.ExecContext(ctx, "INSERT INTO audits(id,org_id,actor_id,action,target_id,outcome,expires_at) SELECT $1,NULLIF($2,''),NULLIF($3,''),$4,$5,'succeeded',clock_timestamp()+audit_days*interval '1 day' FROM settings", core.ID(), org, actor, action, target)
	return e
}
func ownership(sc core.Scope, kind, id string) string {
	return fmt.Sprintf("%s/%s/%d/%s/%s", sc.OrganizationID, sc.UserID, sc.Generation, kind, id)
}
func noRows(e error) error {
	if errors.Is(e, sql.ErrNoRows) {
		return ErrNotFound
	}
	return e
}
