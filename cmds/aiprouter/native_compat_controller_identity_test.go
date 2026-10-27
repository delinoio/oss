// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestNativeControllerFirstPartyResponses(t *testing.T) {
	for _, test := range []struct{ path, body string }{
		{"/api/claude_cli/bootstrap", `{"additional_model_options":[]}`},
		{"/api/organizations/org/model_selector/cc", `{"model_selector_config":[{"id":"cc","models":[{"id":"claude-native"}]}]}`},
	} {
		t.Run(test.path, func(t *testing.T) {
			c, req, _ := controllerFixture(t)
			req.Mode = NativeCompatibilityClaudeFirstParty
			c.options.Models = func(context.Context, Profile, string) ([]ClientModel, error) {
				return []ClientModel{{ID: "claude-fixture", Name: "Fixture"}}, nil
			}
			c.options.Run = func(ctx context.Context, cfg NativeTLSConfig) error {
				cfg.OnReady()
				request, _ := http.NewRequest("GET", "https://api.anthropic.com"+test.path, http.NoBody)
				request.Header.Set("User-Agent", "claude-cli/2.1.296 (external, cli)")
				transport := nativeRoundTripFunc(func(*http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: 200, ProtoMajor: 1, ProtoMinor: 1, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(test.body)), ContentLength: -1}, nil
				})
				var out strings.Builder
				if e := nativeForwardResponse(ctx, &out, request, cfg, &url.URL{}, transport); e != nil {
					return e
				}
				if !strings.Contains(out.String(), "claude-fixture") {
					t.Fatal("routed alias absent from CLI response", out.String())
				}
				return nil
			}
			ctx := context.Background()
			if _, e := c.Enable(ctx, req, controllerConsent(t, c, req)); e != nil {
				t.Fatal(e)
			}
			if e := c.Serve(ctx, req.Profile.Name); e != nil {
				t.Fatal(e)
			}
		})
	}
}
func TestNativeControllerInstallationGeneration(t *testing.T) {
	c, req, _ := controllerFixture(t)
	c.options.Platform = "windows"
	req.Mode = NativeCompatibilityCodexAccountUITrial
	req.Build = NativeCompatibilityCodexBuild
	req.Trial = true
	proof := launchProof("").installation()
	c.options.ObserveInstallation = func(context.Context, NativeRuntimeRequest) (NativeInstallationProof, error) { return proof, nil }
	account := NativeCompatibilityAccount{ID: "account", UserID: "user", Plan: "plus", Structure: "personal"}
	c.options.Account = func(context.Context, NativeRuntimeRequest) (NativeCompatibilityAccount, error) { return account, nil }
	ctx := context.Background()
	consent := controllerConsent(t, c, req)
	if _, e := c.Enable(ctx, req, consent); e != nil {
		t.Fatal(e)
	}
	r, _, _ := c.load(req.Profile.Name)
	if !c.trialValid(ctx, r, "") {
		t.Fatal("initial trial invalid")
	}
	a := proof
	for _, field := range []string{"root", "exe", "manifest", "digest"} {
		proof = a
		switch field {
		case "root":
			proof.Root += "new"
		case "exe":
			proof.ExecutableStamp += "new"
		case "manifest":
			proof.ManifestStamp += "new"
		case "digest":
			proof.ManifestDigest += "new"
		}
		if c.trialValid(ctx, r, "") {
			t.Fatal("replacement retained trial", field)
		}
		proof = a
		if c.trialValid(ctx, r, "") {
			t.Fatal("A-B-A restored consent", field)
		}
	}
	// A second controller must see the persisted generation after process restart.
	other := NewNativeRuntimeControllerWithOptions(c.disk, c.credentials, c.options).(*nativeRuntimeController)
	if other.trialValid(ctx, r, "") {
		t.Fatal("restart revived consent")
	}
	if _, e := c.Enable(ctx, req, consent); e == nil {
		t.Fatal("old plan enabled replacement")
	}
}
func TestNativeControllerInstallationChangesDuringAccountCheck(t *testing.T) {
	c, req, _ := controllerFixture(t)
	c.options.Platform = "windows"
	req.Mode = NativeCompatibilityCodexAccountUITrial
	req.Build = NativeCompatibilityCodexBuild
	req.Trial = true
	proof := launchProof("").installation()
	c.options.ObserveInstallation = func(context.Context, NativeRuntimeRequest) (NativeInstallationProof, error) { return proof, nil }
	account := NativeCompatibilityAccount{ID: "account", UserID: "user", Plan: "plus", Structure: "personal"}
	mutate := false
	c.options.Account = func(context.Context, NativeRuntimeRequest) (NativeCompatibilityAccount, error) {
		if mutate {
			original := proof
			proof.ExecutableStamp += "changed"
			if _, e := c.installation(context.Background(), req); e != nil {
				t.Fatal(e)
			}
			proof = original
			if _, e := c.installation(context.Background(), req); e != nil {
				t.Fatal(e)
			}
		}
		return account, nil
	}
	ctx := context.Background()
	if _, e := c.Enable(ctx, req, controllerConsent(t, c, req)); e != nil {
		t.Fatal(e)
	}
	r, _, _ := c.load(req.Profile.Name)
	mutate = true
	if c.trialValid(ctx, r, "") {
		t.Fatal("in-flight installation replacement accepted")
	}
}

func TestNativeControllerFirstPartyTLSResponses(t *testing.T) {
	c, req, _ := controllerFixture(t)
	req.Mode = NativeCompatibilityClaudeFirstParty
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	req.Port = listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	c.options.Models = func(context.Context, Profile, string) ([]ClientModel, error) {
		return []ClientModel{{ID: "claude-fixture", Name: "Fixture"}}, nil
	}
	c.options.Transport = nativeRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.anthropic.com" {
			t.Error("wrong upstream")
		}
		body := `{"additional_model_options":[]}`
		if strings.Contains(r.URL.Path, "model_selector") {
			body = `{"model_selector_config":[{"id":"cc","models":[{"id":"claude-native"}]}]}`
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), ContentLength: -1}, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, e = c.Enable(ctx, req, controllerConsent(t, c, req)); e != nil {
		t.Fatal(e)
	}
	started := make(chan *NativeCA, 1)
	done := make(chan error, 1)
	c.options.Run = func(ctx context.Context, cfg NativeTLSConfig) error {
		onReady := cfg.OnReady
		cfg.OnReady = func() { onReady(); started <- cfg.CA }
		return RunNativeTLS(ctx, cfg)
	}
	go func() { done <- c.Serve(ctx, req.Profile.Name) }()
	var ca *NativeCA
	select {
	case ca = <-started:
	case e = <-done:
		t.Fatal(e)
	case <-time.After(3 * time.Second):
		t.Fatal("start timeout")
	}
	defer func() {
		cancel()
		select {
		case e := <-done:
			if e != nil {
				t.Error(e)
			}
		case <-time.After(3 * time.Second):
			t.Error("stop timeout")
		}
	}()
	ready, e := c.Ready(ctx, req.Profile.Name)
	if e != nil {
		t.Fatal(e)
	}
	proxy, _ := url.Parse(ready.ProxyURL)
	roots := x509.NewCertPool()
	roots.AddCert(ca.Certificate)
	transport := &http.Transport{Proxy: http.ProxyURL(proxy), TLSClientConfig: &tls.Config{RootCAs: roots}, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	for _, path := range []string{"/api/claude_cli/bootstrap", "/api/organizations/org/model_selector/cc"} {
		request, _ := http.NewRequestWithContext(ctx, "GET", "https://api.anthropic.com"+path, nil)
		request.Header.Set("User-Agent", "claude-cli/2.1.296 (external, cli)")
		response, e := client.Do(request)
		if e != nil {
			t.Fatal(e)
		}
		body, e := io.ReadAll(response.Body)
		response.Body.Close()
		if e != nil || response.StatusCode != 200 || !strings.Contains(string(body), "claude-fixture") {
			t.Fatalf("catalog %s: %d %s %v", path, response.StatusCode, body, e)
		}
	}
	if e = c.Stop(ctx, req.Profile.Name); e != nil {
		t.Fatal(e)
	}
	if ready.Validate() == nil {
		t.Fatal("stopped controller still ready")
	}
}
