// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/packages/tairouter/inference"
)

func nativeHealthUsagePolicy(t *testing.T, handler http.HandlerFunc) inference.DestinationPolicy {
	t.Helper()
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(7), DNSNames: []string{"chatgpt.com", "api.anthropic.com"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, e := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	cert, e := x509.ParseCertificate(der)
	if e != nil {
		t.Fatal(e)
	}
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	remote := httptest.NewUnstartedServer(handler)
	remote.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	remote.StartTLS()
	t.Cleanup(remote.Close)
	return inference.DestinationPolicy{Resolver: providerAcceptanceResolver{}, TLSConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, remote.Listener.Addr().String())
	}}
}
func TestNativeHealthUsageCapturedGateAndBudget(t *testing.T) {
	var sends, gateCalls, budget atomic.Int32
	policy := nativeHealthUsagePolicy(t, func(w http.ResponseWriter, r *http.Request) {
		sends.Add(1)
		if r.Host != "chatgpt.com" || r.URL.Path != "/backend-api/wham/usage" || r.Header.Get("Authorization") != "Bearer fixture-token" || r.Header.Get("ChatGPT-Account-Id") != "fixture-account" {
			t.Error("usage request identity mismatch")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"rate_limit":{"primary_window":{"used_percent":10,"limit_window_seconds":18000,"reset_at":2000000000}}}`))
	})
	credential := inference.Credential{AccountID: "a", CredentialVersion: "v", Secret: "fixture-token", Headers: http.Header{"Chatgpt-Account-Id": []string{"fixture-account"}}}
	gate := func(ctx context.Context, start func(context.Context) error) error {
		gateCalls.Add(1)
		return start(ctx)
	}
	if _, e := nativeUsageRequest(context.Background(), policy, gate, "openai", credential); e == nil || sends.Load() != 0 {
		t.Fatal("usage escaped missing inherited budget")
	}
	ctx := inference.WithPhysicalSendBudget(context.Background(), func(ctx context.Context, start func(context.Context) error) error { budget.Add(1); return start(ctx) })
	q, e := nativeUsageRequest(ctx, policy, gate, "openai", credential)
	if e != nil || !nativeQuotaProvesRecovery(q, time.Now()) {
		t.Fatalf("quota %+v %v", q, e)
	}
	if sends.Load() != 1 || budget.Load() != 1 || gateCalls.Load() != 1 {
		t.Fatalf("sends=%d budget=%d gate=%d", sends.Load(), budget.Load(), gateCalls.Load())
	}
	denied := func(context.Context, func(context.Context) error) error { return inference.ErrUnauthorized }
	if _, e = nativeUsageRequest(ctx, policy, denied, "openai", credential); e == nil || sends.Load() != 1 || budget.Load() != 1 {
		t.Fatal("revoked gate initiated usage send")
	}
}
