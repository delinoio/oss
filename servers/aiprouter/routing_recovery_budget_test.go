// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"github.com/delinoio/oss/packages/aiprouter/inference"
	"github.com/delinoio/oss/packages/aiprouter/providers"
	"github.com/delinoio/oss/packages/aiprouter/store"
	v1 "github.com/delinoio/oss/protos/aiprouter/v1"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func newRecoveryHTTPSAcceptance(t *testing.T, handler http.HandlerFunc) *providerAcceptanceFixture {
	t.Helper()
	f := newProviderAcceptance(t, "openai", "gpt-6-astra", func(w http.ResponseWriter, r *http.Request) {
		t.Error("request reached the unrelated TLS fixture")
		w.WriteHeader(500)
	})
	f.data.Credential.ExpiresAt = time.Now().Add(time.Hour)
	f.data.Models = &providers.ModelCatalog{State: providers.ValidationAccepted, Models: []providers.Model{{ID: "gpt-6-astra"}}}
	f.data.ModelsDiscoveredAt = time.Now()
	f.save()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	acceptanceOK(t, err)
	template := &x509.Certificate{SerialNumber: big.NewInt(2), DNSNames: []string{"chatgpt.com"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	acceptanceOK(t, err)
	cert, err := x509.ParseCertificate(der)
	acceptanceOK(t, err)
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	remote := httptest.NewUnstartedServer(handler)
	remote.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	remote.StartTLS()
	t.Cleanup(remote.Close)
	f.srv.inferenceDestinations = &inference.DestinationPolicy{Resolver: providerAcceptanceResolver{}, TLSConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, remote.Listener.Addr().String())
	}}
	return f
}

func TestRoutingRecoveryProbeChargedOnlyAtGrant(t *testing.T) {
	a, sc, key := poolRoutingFixture(t, v1.PoolStrategy_POOL_STRATEGY_PRIORITY)
	ctx := context.Background()
	d, err := a.provider(ctx, sc, "a")
	acceptanceOK(t, err)
	d.Metadata.Id = "d"
	putRoutingProvider(t, a, sc, d)
	p, release, err := a.Admit(ctx, key)
	acceptanceOK(t, err)
	defer release()
	l, err := a.current(p)
	acceptanceOK(t, err)
	sends := 0
	for _, id := range []string{"a", "b", "c"} {
		_, version, err := a.providerVersion(ctx, sc, id)
		acceptanceOK(t, err)
		permit, err := a.s.AcquireNativeHealthPermitForRecovery(ctx, l, store.NativeHealthKey{AccountID: id, CredentialVersion: version, Lane: store.NativeHealthTransient}, true, "fixture-pool", nativeRecoveryClass(ctx, true))
		acceptanceOK(t, err)
		acceptanceOK(t, a.s.WithNativeHealthDispatch(ctx, l, []store.NativeHealthPermit{permit}, func(context.Context) error { sends++; return nil }))
		acceptanceOK(t, a.s.ReleaseNativeHealthPermit(ctx, l, permit))
	}
	_, version, err := a.providerVersion(ctx, sc, "d")
	acceptanceOK(t, err)
	_, err = a.s.AcquireNativeHealthPermitForRecovery(ctx, l, store.NativeHealthKey{AccountID: "d", CredentialVersion: version, Lane: store.NativeHealthTransient}, true, "fixture-pool", nativeRecoveryClass(ctx, true))
	var exhausted *store.NativeRecoveryLimitError
	if !errors.As(err, &exhausted) || sends != 3 {
		t.Fatalf("probe budget was not charged exactly once: sends=%d error=%v", sends, err)
	}
}

func TestRoutingRecoveryBlockedInitialDoesNotCreateDemand(t *testing.T) {
	f := newRecoveryHTTPSAcceptance(t, func(http.ResponseWriter, *http.Request) { t.Error("blocked gate reached upstream") })
	a := &inferenceAdapter{s: f.s, telemetry: f.srv.telemetry}
	ctx := context.Background()
	p, release, err := a.Admit(ctx, f.key)
	acceptanceOK(t, err)
	defer release()
	l, err := a.current(p)
	acceptanceOK(t, err)
	_, version, err := a.providerVersion(ctx, f.scope, f.data.Metadata.Id)
	acceptanceOK(t, err)
	permit, err := f.s.AcquireNativeHealthPermitForRecovery(ctx, l, store.NativeHealthKey{AccountID: f.data.Metadata.Id, CredentialVersion: version, Lane: store.NativeHealthTransient}, false, "blocked-pool", store.NativeRecoveryInitial)
	acceptanceOK(t, err)
	f.data.Credential.AccessToken = "replacement-fixture"
	f.save()
	sent := false
	err = f.s.WithNativeHealthDispatch(ctx, l, []store.NativeHealthPermit{permit}, func(context.Context) error { sent = true; return nil })
	if err == nil || sent {
		t.Fatalf("stale generation crossed physical gate: %v", err)
	}
	var initials int
	err = f.db.QueryRowContext(ctx, `SELECT COALESCE(sum(initials),0) FROM "`+f.schema+`".native_recovery_buckets WHERE pool_key=$1`, "blocked-pool").Scan(&initials)
	acceptanceOK(t, err)
	if initials != 0 {
		t.Fatalf("blocked initial created demand: %d", initials)
	}
}

func TestRoutingRecoveryBudgetPhysicalInitials(t *testing.T) {
	var model, support atomic.Int32
	f := newRecoveryHTTPSAcceptance(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/backend-api/wham/usage" {
			support.Add(1)
			providerAcceptanceResponse(w, `{"rate_limit":{"primary_window":{"used_percent":1,"limit_window_seconds":18000},"secondary_window":{"used_percent":2,"limit_window_seconds":604800}}}`)
			return
		}
		model.Add(1)
		providerAcceptanceResponse(w, `{"id":"fixture","object":"response","status":"completed","output":[{"id":"msg_fixture","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1,"output_tokens":1}}`)
	})
	for i := 0; i < 1; i++ {
		w := f.call("/v1/responses", `{"model":"fixture","input":"fixture","stream":false}`, nil)
		if w.Code != 200 {
			t.Fatalf("request%d status%d body%s", i, w.Code, w.Body.String())
		}
	}
	if model.Load() != 1 {
		t.Fatalf("model sends=%d", model.Load())
	}
	a := &inferenceAdapter{s: f.s, telemetry: f.srv.telemetry}
	p, release, err := a.Admit(context.Background(), f.key)
	acceptanceOK(t, err)
	defer release()
	route, err := a.Resolve(context.Background(), p, "fixture", inference.Generate)
	acceptanceOK(t, err)
	pool := "native/openai/" + route.Targets[0].Provider
	lease, err := a.current(p)
	acceptanceOK(t, err)
	// Inspect the production receipt from one HTTPS model send. Running twenty
	// TLS requests serially under -race can exceed the real ten-second window.
	var initial int
	err = f.db.QueryRowContext(context.Background(), `SELECT COALESCE(sum(initials),0) FROM "`+f.schema+`".native_recovery_buckets WHERE pool_key=$1`, pool).Scan(&initial)
	acceptanceOK(t, err)
	if initial != 1 {
		t.Fatalf("support exchanges changed model denominator: %d", initial)
	}
	// Add nineteen atomic demand receipts within the same window, then verify
	// the policy admits four recoveries rather than its quiet-pool floor of three.
	for i := 0; i < 19; i++ {
		_, err = f.s.ChargeNativeRecovery(context.Background(), lease, pool, store.NativeRecoveryInitial)
		acceptanceOK(t, err)
	}
	for i := 0; i < 4; i++ {
		_, err = f.s.ChargeNativeRecovery(context.Background(), lease, pool, store.NativeRecoveryRetry)
		acceptanceOK(t, err)
	}
	_, err = f.s.ChargeNativeRecovery(context.Background(), lease, pool, store.NativeRecoveryRetry)
	var limit *store.NativeRecoveryLimitError
	if !errors.As(err, &limit) || !limit.RetryAt.After(time.Now()) {
		t.Fatalf("missing bounded recovery refusal: %v", err)
	}
}
