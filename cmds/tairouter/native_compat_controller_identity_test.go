// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
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

func TestNativeControllerClaudeInstallationConsent(t *testing.T) {
	for _, platform := range []string{"darwin", "windows"} {
		for _, mode := range []NativeCompatibilityMode{NativeCompatibilityClaudeFirstParty, NativeCompatibilityClaudePicker} {
			t.Run(platform+"/"+string(mode), func(t *testing.T) {
				c, req, trust := controllerFixture(t)
				c.options.Platform, req.Mode = platform, mode
				proof := NativeInstallationProof{Build: req.Build, Root: "/Applications/Claude.app", ExecutableStamp: "exe-a", ManifestDigest: "manifest-a"}
				if platform == "windows" {
					proof.Root, proof.ManifestDigest = `C:\Users\fixture\Claude\claude.exe`, ""
				}
				c.options.ObserveInstallation = func(context.Context, NativeRuntimeRequest) (NativeInstallationProof, error) { return proof, nil }
				ctx := context.Background()
				plan, err := c.Plan(ctx, req)
				if err != nil {
					t.Fatal(err)
				}
				steady, err := c.Plan(ctx, req)
				if err != nil || steady.PlanDigest != plan.PlanDigest {
					t.Fatal("unchanged installation changed consent")
				}
				consent := NativeRuntimeConsent{Trust: true, Application: true, PlanFingerprint: plan.Fingerprint, PlanDigest: plan.PlanDigest}
				a := proof
				proof.ExecutableStamp = "exe-b"
				if _, err := c.Enable(ctx, req, consent); err == nil || trust.installs != 0 {
					t.Fatal("old consent reached trust after replacement")
				}
				proof = a
				if _, err := c.Enable(ctx, req, consent); err == nil {
					t.Fatal("A-B-A restored old consent")
				}
				restarted := NewNativeRuntimeControllerWithOptions(c.disk, c.credentials, c.options).(*nativeRuntimeController)
				if _, err := restarted.Enable(ctx, req, consent); err == nil {
					t.Fatal("restart restored old consent")
				}
				fresh, err := restarted.Plan(ctx, req)
				if err != nil || fresh.PlanDigest == plan.PlanDigest {
					t.Fatal("replacement did not renew plan")
				}
				consent.PlanDigest = fresh.PlanDigest
				if _, err := restarted.Enable(ctx, req, consent); err != nil {
					t.Fatal(err)
				}
				record, exists, err := restarted.load(req.Profile.Name)
				if err != nil || !exists {
					t.Fatal("runtime record missing", err)
				}
				proof.ExecutableStamp = "exe-b"
				if restarted.installationMatches(ctx, record) {
					t.Fatal("replacement matched runtime")
				}
				if err := restarted.Serve(ctx, req.Profile.Name); err == nil {
					t.Fatal("Serve accepted replacement")
				}
				proof = a
				if restarted.installationMatches(ctx, record) {
					t.Fatal("A-B-A restored runtime")
				}
				root, _ := restarted.root(req.Profile.Name)
				runtimePath := filepath.Join(root, "runtime.json")
				raw, _, err := readConfig(runtimePath)
				if err != nil {
					t.Fatal(err)
				}
				record.Installation = NativeInstallationProof{Build: req.Build, Generation: record.Installation.Generation}
				legacy, err := json.Marshal(record)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(runtimePath, legacy, 0600); err != nil {
					t.Fatal(err)
				}
				if restarted.installationMatches(ctx, record) {
					t.Fatal("legacy runtime proof matched")
				}
				if err := restarted.Serve(ctx, req.Profile.Name); err == nil {
					t.Fatal("Serve accepted legacy runtime")
				}
				if _, err := os.Stat(runtimePath); err != nil {
					t.Fatal("legacy recovery record lost", err)
				}
				if err := os.WriteFile(runtimePath, raw, 0600); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestNativeControllerClaudeInFlightReplacement(t *testing.T) {
	for _, mode := range []NativeCompatibilityMode{NativeCompatibilityClaudeFirstParty, NativeCompatibilityClaudePicker} {
		t.Run(string(mode), func(t *testing.T) {
			ctx := context.Background()
			c, req, trust := controllerFixture(t)
			req.Mode = mode
			proof := NativeInstallationProof{Build: req.Build, Root: "/Applications/Claude.app", ExecutableStamp: "a", ManifestDigest: "manifest"}
			c.options.ObserveInstallation = func(context.Context, NativeRuntimeRequest) (NativeInstallationProof, error) { return proof, nil }
			plan, err := c.Plan(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			consent := NativeRuntimeConsent{Trust: true, Application: true, PlanFingerprint: plan.Fingerprint, PlanDigest: plan.PlanDigest}
			trust.beforeInstall = func() {
				proof.ExecutableStamp = "b"
				if _, err := c.installation(ctx, req); err != nil {
					t.Error(err)
				}
				proof.ExecutableStamp = "a"
			}
			if _, err := c.Enable(ctx, req, consent); err == nil {
				t.Fatal("installation change during activation accepted")
			}
			trust.beforeInstall = nil
			if err := c.Serve(ctx, req.Profile.Name); err == nil {
				t.Fatal("pending activation was served")
			}
			fresh, err := c.Plan(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			consent.PlanDigest = fresh.PlanDigest
			// The pending record retains consent for the old plan; use a fresh fixture
			// for the independent catalog boundary.
			c2, req2, _ := controllerFixture(t)
			req2.Mode = mode
			c2.options.ObserveInstallation = func(context.Context, NativeRuntimeRequest) (NativeInstallationProof, error) { return proof, nil }
			newPlan, err := c2.Plan(ctx, req2)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := c2.Enable(ctx, req2, NativeRuntimeConsent{Trust: true, Application: true, PlanFingerprint: newPlan.Fingerprint, PlanDigest: newPlan.PlanDigest}); err != nil {
				t.Fatal(err)
			}
			c2.options.Models = func(context.Context, Profile, string) ([]ClientModel, error) {
				proof.ExecutableStamp = "b"
				return []ClientModel{{ID: "fixture", Name: "Fixture"}}, nil
			}
			c2.options.CLIModels = func(context.Context, Profile, string) ([]ClientModel, error) { return nil, nil }
			c2.options.Run = func(context.Context, NativeTLSConfig) error {
				t.Error("runtime launched after replacement")
				return nil
			}
			if err := c2.Serve(ctx, req2.Profile.Name); err == nil {
				t.Fatal("catalog change accepted")
			}
		})
	}
}

func TestNativeControllerClaudeLegacyObservationRequiresNewPlan(t *testing.T) {
	c, req, _ := controllerFixture(t)
	ctx := context.Background()
	old, err := c.Plan(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	root, _ := c.root(req.Profile.Name)
	observation := filepath.Join(root, "observation", "installed.json")
	if _, err := os.Stat(observation); err != nil {
		t.Fatal("legacy observation missing", err)
	}
	c.options.ObserveBuild = nil
	c.options.ObserveInstallation = func(context.Context, NativeRuntimeRequest) (NativeInstallationProof, error) {
		return NativeInstallationProof{Build: req.Build, Root: "/Applications/Claude.app", ExecutableStamp: "exe", ManifestDigest: "plist"}, nil
	}
	fresh, err := c.Plan(ctx, req)
	if err != nil || fresh.PlanDigest == old.PlanDigest {
		t.Fatal("legacy build-only observation retained consent", err)
	}
	if _, err := c.Enable(ctx, req, NativeRuntimeConsent{Trust: true, Application: true, PlanFingerprint: old.Fingerprint, PlanDigest: old.PlanDigest}); err == nil {
		t.Fatal("legacy plan accepted")
	}
	if _, err := os.Stat(observation); err != nil {
		t.Fatal("observation recovery evidence lost", err)
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
