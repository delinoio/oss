// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/packages/aiprouter/core"
	"github.com/delinoio/oss/packages/aiprouter/providers"
	"github.com/delinoio/oss/packages/aiprouter/store"
	v1 "github.com/delinoio/oss/protos/aiprouter/v1"
)

// Only this fixture's PostgreSQL connections traverse the switch. The shared
// server and the fixture's inspection connection remain available throughout.
func nativeHealthDatabaseSwitch(t *testing.T, dsn string) (string, func(bool)) {
	t.Helper()
	u, e := url.Parse(dsn)
	if e != nil {
		t.Fatal(e)
	}
	upstream := u.Host
	if u.Port() == "" {
		upstream = net.JoinHostPort(u.Hostname(), "5432")
	}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	var mu sync.Mutex
	offline := false
	connections := map[net.Conn]bool{}
	var workers sync.WaitGroup
	acceptDone := make(chan struct{})
	toggle := func(value bool) {
		mu.Lock()
		defer mu.Unlock()
		offline = value
		if value {
			for c := range connections {
				_ = c.Close()
			}
		}
	}
	go func() {
		defer close(acceptDone)
		for {
			client, e := listener.Accept()
			if e != nil {
				return
			}
			mu.Lock()
			if offline {
				mu.Unlock()
				_ = client.Close()
				continue
			}
			connections[client] = true
			workers.Add(1)
			mu.Unlock()
			go func() {
				defer workers.Done()
				defer client.Close()
				defer func() { mu.Lock(); delete(connections, client); mu.Unlock() }()
				backend, e := net.DialTimeout("tcp", upstream, time.Second)
				if e != nil {
					return
				}
				defer backend.Close()
				copied := make(chan struct{})
				go func() { defer close(copied); _, _ = io.Copy(backend, client); _ = backend.Close() }()
				_, _ = io.Copy(client, backend)
				_ = client.Close()
				<-copied
			}()
		}
	}()
	t.Cleanup(func() { _ = listener.Close(); <-acceptDone; toggle(true); workers.Wait() })
	u.Host = listener.Addr().String()
	return u.String(), toggle
}

func TestNativeHealthQuotaCancellationTerminalCleanup(t *testing.T) {
	for _, scenario := range []struct {
		name             string
		outage, shutdown bool
	}{{name: "online"}, {name: "database_outage", outage: true}, {name: "shutdown_reconciliation", outage: true, shutdown: true}} {
		outage := scenario.outage
		t.Run(scenario.name, func(t *testing.T) {
			f := newProviderAcceptance(t, "openai", "gpt-6-astra", func(w http.ResponseWriter, r *http.Request) {
				t.Error("unrelated upstream reached")
				w.WriteHeader(500)
			})
			f.data.Metadata.AuthKind = v1.ProviderAuthKind_PROVIDER_AUTH_KIND_OAUTH
			f.data.Metadata.BaseUrl = ""
			f.data.Secret = ""
			f.data.Credential = &providers.Credential{Provider: "openai", Scope: f.scope, AccessToken: f.secret, AccountID: "fixture-account", ExpiresAt: time.Now().Add(time.Hour)}
			f.data.Models = &providers.ModelCatalog{State: providers.ValidationAccepted, Models: []providers.Model{{ID: "gpt-6-astra"}}}
			f.data.ModelsDiscoveredAt = time.Now()
			f.save()
			pool := &v1.Pool{Id: "health-cleanup", Members: []*v1.PoolMember{{ConnectionId: f.data.Metadata.Id}}}
			raw, e := json.Marshal(pool)
			acceptanceOK(t, e)
			acceptanceOK(t, f.s.PutPrivate(context.Background(), f.scope, "pool", pool.Id, raw))
			putRoutingRoute(t, &inferenceAdapter{s: f.s}, f.scope, &v1.Route{Id: "fixture-route", ModelId: "fixture", Enabled: true, Strategy: v1.RoutingStrategy_ROUTING_STRATEGY_DIRECT, Targets: []*v1.RouteTarget{{Target: &v1.RouteTarget_PoolId{PoolId: pool.Id}, ModelId: "gpt-6-astra"}}})
			ctx := context.Background()
			seed, e := f.s.Admit(ctx, f.key)
			acceptanceOK(t, e)
			_, version, e := f.s.GetPrivateVersion(ctx, f.scope, "provider", f.data.Metadata.Id)
			acceptanceOK(t, e)
			healthKey := store.NativeHealthKey{AccountID: f.data.Metadata.Id, CredentialVersion: version, Lane: store.NativeHealthQuota, Model: "shared"}
			acceptanceOK(t, f.s.UpdateNativeHealth(ctx, seed, healthKey, func(h store.NativeHealth) (store.NativeHealth, error) {
				h.CooldownSince = h.Now.Add(-6 * time.Minute)
				h.CooldownUntil = h.Now.Add(10 * time.Minute)
				h.ProbeEligible = true
				return h, nil
			}))
			acceptanceOK(t, f.s.Release(ctx, seed))
			serviceStore := f.s
			toggle := func(bool) {}
			if outage {
				dsn, switchDB := nativeHealthDatabaseSwitch(t, os.Getenv("AIPROUTER_TEST_DATABASE_URL"))
				toggle = switchDB
				keys, e := core.NewEnvelope("fixture", map[string][]byte{"fixture": make([]byte, 32)})
				acceptanceOK(t, e)
				serviceStore, e = store.Open(ctx, dsn, store.Options{Schema: f.schema, InstanceID: f.s.InstanceID(), Keys: keys})
				acceptanceOK(t, e)
				t.Cleanup(func() { serviceStore.Close() })
				f.srv.store = serviceStore
			}
			lifetime, stopQueue := context.WithCancel(ctx)
			defer stopQueue()
			queue := newAdmissionReleaseQueue(lifetime, serviceStore, f.srv.telemetry)
			defer queue.Close()
			f.srv.releases = queue
			received, joined := make(chan struct{}), make(chan struct{})
			policy := nativeHealthUsagePolicy(t, func(w http.ResponseWriter, r *http.Request) {
				defer close(joined)
				if r.URL.Path != "/backend-api/wham/usage" {
					t.Error("expected quota probe")
				}
				close(received)
				<-r.Context().Done()
			})
			f.srv.inferenceDestinations = &policy
			requestCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"fixture","input":"fixture"}`)).WithContext(requestCtx)
			request.Header.Set("Authorization", "Bearer "+f.key)
			request.Header.Set("Content-Type", "application/json")
			returned := make(chan struct{})
			w := httptest.NewRecorder()
			go func() { defer close(returned); f.srv.handler().ServeHTTP(w, request) }()
			select {
			case <-received:
			case <-returned:
				t.Fatalf("request ended before quota probe: status %d", w.Code)
			case <-time.After(10 * time.Second):
				t.Fatal("quota request not received")
			}
			toggle(outage)
			cancel()
			select {
			case <-joined:
			case <-time.After(10 * time.Second):
				t.Fatal("quota transport did not terminate")
			}
			select {
			case <-returned:
			case <-time.After(10 * time.Second):
				t.Fatal("canceled request did not return")
			}
			if outage {
				short, cancelWait := context.WithTimeout(ctx, 150*time.Millisecond)
				e = queue.Wait(short)
				cancelWait()
				if e == nil {
					t.Fatal("release acknowledged while database unavailable")
				}
				// Inspection uses the same isolated schema through a separate connection.
				observer, e := f.s.Admit(ctx, f.key)
				acceptanceOK(t, e)
				h, e := f.s.GetNativeHealth(ctx, observer, healthKey)
				acceptanceOK(t, e)
				if h.ProbeID == "" {
					t.Fatal("failed cleanup lost its durable ownership obligation")
				}
				if _, e = f.s.AcquireNativeHealthPermit(ctx, observer, healthKey, true); !errors.Is(e, store.ErrBusy) {
					t.Fatalf("uncleaned claim was reclaimed: %v", e)
				}
				acceptanceOK(t, f.s.Release(ctx, observer))
				if scenario.shutdown {
					queue.Close()
				}
				toggle(false)
			}
			wait, stopWait := context.WithTimeout(ctx, 10*time.Second)
			defer stopWait()
			acceptanceOK(t, queue.Wait(wait))
			nextStore := f.s
			if scenario.shutdown {
				// The queue was stopped during the outage. Only the explicit
				// stopped-instance proof may discharge the durable obligation.
				proof, err := f.s.DrainAdmissionInstance(ctx, f.s.InstanceID())
				acceptanceOK(t, err)
				if _, err = f.s.ReconcileTerminatedInstance(ctx, f.s.InstanceID(), proof, false); !errors.Is(err, store.ErrForbidden) {
					t.Fatalf("unconfirmed shutdown reclaimed admission: %v", err)
				}
				count, err := f.s.ReconcileTerminatedInstance(ctx, f.s.InstanceID(), proof, true)
				acceptanceOK(t, err)
				if count != 1 {
					t.Fatalf("reconciled admissions %d", count)
				}
				keys, err := core.NewEnvelope("fixture", map[string][]byte{"fixture": make([]byte, 32)})
				acceptanceOK(t, err)
				nextStore, err = store.Open(ctx, os.Getenv("AIPROUTER_TEST_DATABASE_URL"), store.Options{Schema: f.schema, Keys: keys})
				acceptanceOK(t, err)
				defer nextStore.Close()
				acceptanceOK(t, nextStore.RegisterAdmissionInstance(ctx))
			}
			instances, e := f.s.ListAdmissionInstances(ctx)
			acceptanceOK(t, e)
			for _, instance := range instances {
				if instance.ID == f.s.InstanceID() && instance.ActiveAdmissions != 0 {
					t.Fatalf("terminal request still consumes admission capacity: %d", instance.ActiveAdmissions)
				}
			}
			next, e := nextStore.Admit(ctx, f.key)
			acceptanceOK(t, e)
			defer nextStore.Release(ctx, next)
			h, e := nextStore.GetNativeHealth(ctx, next, healthKey)
			acceptanceOK(t, e)
			if h.ProbeID != "" || !h.ProbeEligible || !h.CooldownUntil.After(h.Now) {
				t.Fatalf("terminal cleanup lost refusal or retained claim: %+v", h)
			}
			if _, e = nextStore.AcquireNativeHealthPermit(ctx, next, healthKey, true); !errors.Is(e, store.ErrBusy) {
				t.Fatalf("cleanup bypassed five-minute probe spacing: %v", e)
			}
			// Move only fixture pacing into the past. The unchanged cooldown can now
			// acquire a new recovery probe, proving the old non-expiring owner is gone.
			_, e = f.db.Exec(`UPDATE "`+f.schema+`".native_health SET state=jsonb_set(state,'{LastProbeAt}',to_jsonb($1::text)) WHERE account_id=$2 AND lane='quota' AND model='shared'`, time.Now().Add(-6*time.Minute).UTC().Format(time.RFC3339Nano), healthKey.AccountID)
			acceptanceOK(t, e)
			_, e = f.db.Exec(`UPDATE "`+f.schema+`".native_health SET state=jsonb_set(state,'{NextProbeAt}',to_jsonb($1::text)) WHERE account_id=$2 AND lane='quota' AND model='shared'`, time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano), healthKey.AccountID)
			acceptanceOK(t, e)
			probe, e := nextStore.AcquireNativeHealthPermit(ctx, next, healthKey, true)
			acceptanceOK(t, e)
			acceptanceOK(t, nextStore.ReleaseNativeHealthPermit(ctx, next, probe))
		})
	}
}
