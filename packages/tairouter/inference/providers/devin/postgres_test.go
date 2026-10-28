// SPDX-License-Identifier: Apache-2.0
package devin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/packages/tairouter/core"
	"github.com/delinoio/oss/packages/tairouter/inference"
	"github.com/delinoio/oss/packages/tairouter/store"
)

// This fixture runs the actual provider lifecycle against the production store.
// Server bridge translation is covered by its owning package's integration test.
type postgresAdapterState struct {
	s     *store.Store
	p     store.Principal
	scope string
}

func (s postgresAdapterState) key(id string) string {
	sum := sha256.Sum256([]byte(s.scope + "\x00" + id))
	return hex.EncodeToString(sum[:])
}
func (s postgresAdapterState) policy(ttl time.Duration) store.ProviderStatePolicy {
	return store.ProviderStatePolicy{TTL: ttl, Entries: StateMaxEntries, MaxEntryBytes: StateMaxEntryBytes, MaxTotalBytes: StateMaxBytes}
}
func (s postgresAdapterState) Load(ctx context.Context, id string) ([]byte, error) {
	v, err := s.s.GetProviderState(ctx, s.p, "devin", s.key(id))
	return v.Data, err
}
func (s postgresAdapterState) Save(ctx context.Context, id string, b []byte, ttl time.Duration) error {
	return s.s.PutProviderState(ctx, s.p, "devin", s.key(id), store.ProviderState{Data: b}, s.policy(ttl))
}
func (s postgresAdapterState) Update(ctx context.Context, id string, ttl time.Duration, f func([]byte) ([]byte, error)) error {
	return s.s.UpdateProviderState(ctx, s.p, "devin", s.key(id), s.policy(ttl), f)
}

func TestPostgresExecuteTrajectoryLifecycle(t *testing.T) {
	dsn := os.Getenv("TAIROUTER_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TAIROUTER_TEST_DATABASE_URL for isolated PostgreSQL provider integration")
	}
	ctx := context.Background()
	schema := "devin_" + strings.ReplaceAll(core.ID(), "-", "")
	keys, err := core.NewEnvelope("fixture", map[string][]byte{"fixture": make([]byte, 32)})
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(ctx, dsn, store.Options{Schema: schema, Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		cleanup, err := sql.Open("pgx", dsn)
		if err != nil {
			t.Error(err)
			return
		}
		defer cleanup.Close()
		for _, name := range []string{schema + "_temp", schema} {
			if _, err = cleanup.ExecContext(context.Background(), `DROP SCHEMA IF EXISTS "`+name+`" CASCADE`); err != nil {
				t.Error(err)
			}
		}
	})
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	user, err := db.RegisterIdentity(ctx, "fixture", "devin-owner", "devin@example.test", true)
	if err != nil {
		t.Fatal(err)
	}
	org, err := db.CreateOrganization(ctx, user.ID, "Devin fixture")
	if err != nil {
		t.Fatal(err)
	}
	sc, _, err := db.Membership(ctx, org.ID, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	key, _, err := db.IssueKey(ctx, sc, "Devin fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	in := input(`{"messages":[{"role":"user","content":"fixture"}]}`)
	in.Principal = inference.Principal{OrganizationID: org.ID, UserID: user.ID, KeyID: key.ID, MembershipGeneration: sc.Generation}
	in.Credential.AccountID = "fixture-account"
	in.ClientHeaders = http.Header{"Thread-Id": {"fixture-conversation"}}
	backend := postgresAdapterState{s: db, p: store.Principal{Scope: sc, KeyID: key.ID}, scope: identity(in)}
	a := New()
	type turn struct {
		ctx        context.Context
		response   *http.Response
		trajectory string
		err        error
	}
	execute := func() turn {
		local, err := a.RestoreState(a.PrepareContext(ctx), in, backend)
		if err != nil {
			return turn{err: err}
		}
		trajectory := ""
		response, _, err := a.Execute(local, in, executorFunc(func(_ context.Context, r inference.AdapterRequest) (*http.Response, error) {
			_, proto, e := readFrame(bytes.NewReader(r.Body))
			if e != nil {
				return nil, e
			}
			// Inspect the actual protobuf sent, not an internal option value.
			e = walk(proto, func(f field) error {
				if f.num == 15 {
					return walk(f.b, func(v field) error {
						if v.num == 1 {
							trajectory = string(v.b)
						}
						return nil
					})
				}
				return nil
			})
			if e != nil {
				return nil, e
			}
			return unary(append(frame(textField(nil, 3, "fixture answer")), eos(`{}`)...)), nil
		}), backend)
		return turn{ctx: local, response: response, trajectory: trajectory, err: err}
	}
	// Keep all response bodies open: every claim overlaps the first active turn.
	results := make(chan turn, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); results <- execute() }()
	}
	wg.Wait()
	close(results)
	seen := map[string]bool{}
	var turns []turn
	for v := range results {
		if v.err != nil {
			t.Fatal(v.err)
		}
		if !uuidShape.MatchString(v.trajectory) || seen[v.trajectory] {
			t.Fatal("overlapping database claims shared trajectory")
		}
		seen[v.trajectory] = true
		turns = append(turns, v)
		t.Cleanup(func() { _ = v.response.Body.Close() })
	}
	for i, v := range turns {
		if _, err = io.ReadAll(v.response.Body); err != nil {
			t.Fatal(err)
		}
		if err = v.response.Body.Close(); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if err = a.CaptureState(v.ctx, in, "resp_fixture", backend); err != nil {
				t.Fatal(err)
			}
		}
	}
	next := execute()
	if next.err != nil {
		t.Fatal(next.err)
	}
	defer next.response.Body.Close()
	if !seen[next.trajectory] {
		t.Fatal("released retained trajectory was not reused")
	}
	if _, err = io.ReadAll(next.response.Body); err != nil {
		t.Fatal(err)
	}
	_ = next.response.Body.Close()
	continued := in
	continued.PreviousResponseID = "resp_fixture"
	restored, err := a.RestoreState(a.PrepareContext(ctx), continued, backend)
	if err != nil {
		t.Fatal(err)
	}
	if attempt(restored).cascade != attempt(turns[0].ctx).cascade {
		t.Fatal("database cascade continuation lost")
	}
	// Revoke while a final response owns the retained claim. Cleanup must fail
	// through the real current-key gate, and later execution must never send.
	active := execute()
	if active.err != nil {
		t.Fatal(active.err)
	}
	if err = db.RevokeKey(ctx, sc, key.ID); err != nil {
		t.Fatal(err)
	}
	if err = active.response.Body.Close(); err == nil {
		t.Fatal("release ignored revoked key")
	}
	called := false
	local := a.PrepareContext(ctx)
	if _, _, err = a.Execute(local, in, executorFunc(func(context.Context, inference.AdapterRequest) (*http.Response, error) {
		called = true
		return nil, nil
	}), backend); err == nil || called {
		t.Fatal("revoked claim reached executor", err)
	}
}
