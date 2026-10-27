// SPDX-License-Identifier: Apache-2.0
package cursor

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/packages/aiprouter/core"
	"github.com/delinoio/oss/packages/aiprouter/inference"
	"github.com/delinoio/oss/packages/aiprouter/store"
)

// The production bridge lives in the server's main package. This fixture uses
// the same exported store operations without importing or modifying that server.
type postgresBridge struct {
	s                          *store.Store
	lease                      store.Lease
	mu                         sync.Mutex
	sends, denied, revokeAfter int
	savedNamespace, savedID    string
	saved                      []byte
}

func (b *postgresBridge) Admit(ctx context.Context, raw string) (inference.Principal, func(), error) {
	l, err := b.s.Admit(ctx, raw)
	if err != nil {
		return inference.Principal{}, nil, err
	}
	b.lease = l
	p := l.Principal
	return inference.Principal{OrganizationID: p.Scope.OrganizationID, UserID: p.Scope.UserID, KeyID: p.KeyID, MembershipGeneration: p.Scope.Generation}, func() { _ = b.s.Release(context.Background(), l) }, nil
}
func (b *postgresBridge) BeforeSend(ctx context.Context, _ inference.Principal) error {
	return b.s.CheckDispatch(ctx, b.lease)
}
func (b *postgresBridge) Dispatch(ctx context.Context, _ inference.Principal, start func(context.Context) error) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.revokeAfter > 0 && b.sends == b.revokeAfter {
		if err := b.s.RevokeKey(ctx, b.lease.Principal.Scope, b.lease.Principal.KeyID); err != nil {
			return err
		}
	}
	err := b.s.WithDispatch(ctx, b.lease, func(ctx context.Context) error { b.sends++; return start(ctx) })
	if errors.Is(err, store.ErrForbidden) {
		b.denied++
		return inference.ErrUnauthorized
	}
	return err
}
func (b *postgresBridge) LoadProviderState(ctx context.Context, _ inference.Principal, namespace, id string) (inference.ProviderState, error) {
	value, err := b.s.GetProviderState(ctx, b.lease.Principal, namespace, id)
	if errors.Is(err, store.ErrNotFound) {
		err = inference.ErrContinuation
	}
	return inference.ProviderState{Data: value.Data, ExpiresAt: value.ExpiresAt}, err
}
func (b *postgresBridge) SaveProviderState(ctx context.Context, _ inference.Principal, namespace, id string, value inference.ProviderState) error {
	err := b.s.PutProviderState(ctx, b.lease.Principal, namespace, id, store.ProviderState{Data: value.Data, ExpiresAt: value.ExpiresAt}, store.ProviderStatePolicy{TTL: 15 * time.Minute, Entries: 64, MaxEntryBytes: 16 << 20, MaxTotalBytes: 16 << 20})
	if err == nil {
		b.savedNamespace, b.savedID, b.saved = namespace, id, append([]byte(nil), value.Data...)
	}
	return err
}

func cursorPostgres(t *testing.T) (*store.Store, *sql.DB, string, core.Scope, string) {
	t.Helper()
	dsn := os.Getenv("AIPROUTER_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set AIPROUTER_TEST_DATABASE_URL for Cursor PostgreSQL integration")
	}
	schema := "cursor_" + strings.ReplaceAll(core.ID(), "-", "")
	keys, err := core.NewEnvelope("fixture", map[string][]byte{"fixture": bytes.Repeat([]byte{42}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(context.Background(), dsn, store.Options{Schema: schema, Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		defer db.Close()
		defer s.Close()
		// The schema is generated above, never taken from a caller or connection URL.
		if _, err := db.Exec("DROP SCHEMA IF EXISTS " + schema + "_temp CASCADE"); err != nil {
			t.Error(err)
		}
		if _, err := db.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE"); err != nil {
			t.Error(err)
		}
	})
	ctx := context.Background()
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.RegisterAdmissionInstance(ctx); err != nil {
		t.Fatal(err)
	}
	u, err := s.RegisterIdentity(ctx, "cursor-fixture", core.ID(), "fixture@example.com", true)
	if err != nil {
		t.Fatal(err)
	}
	org, err := s.CreateOrganization(ctx, u.ID, "Cursor fixture")
	if err != nil {
		t.Fatal(err)
	}
	sc, _, err := s.Membership(ctx, org.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, raw, err := s.IssueKey(ctx, sc, "Cursor fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	return s, db, schema, sc, raw
}

func TestCoreCursorPostgresDuplex(t *testing.T) {
	for _, revoke := range []bool{false, true} {
		name := "persists_eight_exchanges"
		if revoke {
			name = "revokes_sixth_exchange"
		}
		t.Run(name, func(t *testing.T) {
			s, db, schema, _, key := cursorPostgres(t)
			f := newDuplexFixture(t)
			f.extraContexts = 4
			close(f.ackInitial)
			bridge := &postgresBridge{s: s}
			if revoke {
				bridge.revokeAfter = 5
			}
			usage := &integrationUsage{}
			target := input(simpleRequest).Target
			target.BaseURL = f.server.URL
			h, err := inference.New(inference.Config{Adapters: map[inference.Wire]inference.Adapter{Wire: New()}, Authorizer: bridge, Router: integrationRouter{inference.Route{Targets: []inference.Target{target}, MaxSends: 258}}, Credentials: integrationCredentials{}, Usage: usage, ProviderStates: bridge, Destinations: inference.DestinationPolicy{ApprovedOrigins: []string{f.server.URL}}})
			if err != nil {
				t.Fatal(err)
			}
			var body map[string]any
			json.Unmarshal([]byte(simpleRequest), &body)
			body["model"], body["stream"] = "fixture", true
			data, _ := json.Marshal(body)
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(data))
			req.Header.Set("Authorization", "Bearer "+key)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			expected := 8
			if revoke {
				expected = 5
			}
			bridge.mu.Lock()
			sends, denied := bridge.sends, bridge.denied
			bridge.mu.Unlock()
			f.mu.Lock()
			runs, appends := len(f.runs), uint64(0)
			for _, run := range f.runs {
				appends += run.sequence
			}
			f.mu.Unlock()
			if sends != expected || usage.record.Sends != expected || runs != 1 || int(appends)+runs != expected {
				t.Fatalf("sends=%d recorded=%d runs=%d appends=%d want=%d: %s", sends, usage.record.Sends, runs, appends, expected, rec.Body.String())
			}
			select {
			case err := <-f.errors:
				t.Fatal(err)
			default:
			}
			if !strings.Contains(rec.Body.String(), "hello before acknowledgement") {
				t.Fatal("no visible output")
			}
			var rows int
			if err = db.QueryRow("SELECT count(*) FROM " + schema + "_temp.provider_states").Scan(&rows); err != nil {
				t.Fatal(err)
			}
			if revoke {
				if denied != 1 || rows != 0 || strings.Contains(rec.Body.String(), "[DONE]") || !strings.Contains(rec.Body.String(), "\"error\"") {
					t.Fatalf("revoked result denied=%d rows=%d: %s", denied, rows, rec.Body.String())
				}
				_, err = s.GetProviderState(context.Background(), bridge.lease.Principal, string(Wire), "absent")
				if !errors.Is(err, store.ErrForbidden) {
					t.Fatalf("revoked lookup returned %v", err)
				}
			} else {
				if denied != 0 || rows != 1 || !strings.Contains(rec.Body.String(), "[DONE]") {
					t.Fatalf("success denied=%d rows=%d: %s", denied, rows, rec.Body.String())
				}
				restored, err := bridge.LoadProviderState(context.Background(), inference.Principal{}, bridge.savedNamespace, bridge.savedID)
				if err != nil || !bytes.Equal(restored.Data, bridge.saved) {
					t.Fatalf("checkpoint restore: %v", err)
				}
				var ciphertext []byte
				if err = db.QueryRow("SELECT value FROM " + schema + "_temp.provider_states").Scan(&ciphertext); err != nil {
					t.Fatal(err)
				}
				if bytes.Equal(ciphertext, restored.Data) || bytes.Contains(ciphertext, []byte("hello before acknowledgement")) {
					t.Fatal("provider state stored as plaintext")
				}
				if usage.record.Usage.OutputTokens == nil || *usage.record.Usage.OutputTokens != 7 {
					t.Fatal("confirmed usage lost")
				}
			}
		})
	}
}

func (b *postgresBridge) UpdateProviderState(ctx context.Context, _ inference.Principal, namespace, id string, expires time.Time, merge func([]byte) ([]byte, error)) error {
	return b.s.UpdateProviderState(ctx, b.lease.Principal, namespace, id, store.ProviderStatePolicy{TTL: time.Until(expires), Entries: 64, MaxEntryBytes: 16 << 20, MaxTotalBytes: 16 << 20}, merge)
}

func TestCoreCursorPostgresSemanticRecovery(t *testing.T) {
	s, db, schema, _, key := cursorPostgres(t)
	f := newDuplexFixture(t)
	f.refusals = []string{"invalid_argument"}
	close(f.ackInitial)
	bridge := &postgresBridge{s: s}
	usage := &integrationUsage{}
	target := input(simpleRequest).Target
	target.BaseURL = f.server.URL
	h, err := inference.New(inference.Config{Adapters: map[inference.Wire]inference.Adapter{Wire: New()}, Authorizer: bridge, Router: integrationRouter{inference.Route{Targets: []inference.Target{target}, MaxSends: 258}}, Credentials: integrationCredentials{}, Usage: usage, ProviderStates: bridge, Destinations: inference.DestinationPolicy{ApprovedOrigins: []string{f.server.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"fixture","stream":true,"cursor_thread_id":"postgres-recovery-thread","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "[DONE]") || usage.record.Sends != 6 || usage.record.UnmeasuredAttempts < 1 {
		t.Fatalf("recovery sends=%d unknown=%d: %s", usage.record.Sends, usage.record.UnmeasuredAttempts, rec.Body.String())
	}
	bridge.mu.Lock()
	sends := bridge.sends
	denied := bridge.denied
	bridge.mu.Unlock()
	if sends != 6 || denied != 0 {
		t.Fatalf("DB dispatches=%d denied=%d", sends, denied)
	}
	var rows int
	if err = db.QueryRow("SELECT count(*) FROM " + schema + "_temp.provider_states").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Fatalf("want retained remint metadata and final checkpoint; rows=%d", rows)
	}
	restored, err := bridge.LoadProviderState(context.Background(), inference.Principal{}, bridge.savedNamespace, bridge.savedID)
	if err != nil || !bytes.Equal(restored.Data, bridge.saved) {
		t.Fatalf("recovered checkpoint: %v", err)
	}
}
