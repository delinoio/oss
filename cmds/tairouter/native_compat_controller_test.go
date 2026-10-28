// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type controllerKeyProtection struct{}

func (controllerKeyProtection) Protect(b []byte) ([]byte, error) {
	return append([]byte(nil), b...), nil
}
func (controllerKeyProtection) Unprotect(b []byte) ([]byte, error) {
	return append([]byte(nil), b...), nil
}

type controllerTrust struct {
	mu                      sync.Mutex
	trusted                 bool
	installs, removes       int
	failInstall, failRemove bool
	beforeInstall           func()
}

func (a *controllerTrust) Status(context.Context, *NativeCA) (NativeTrustStatus, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return NativeTrustStatus{Trusted: a.trusted, Present: a.trusted}, nil
}
func (a *controllerTrust) Install(context.Context, *NativeCA) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.beforeInstall != nil {
		a.beforeInstall()
	}
	a.installs++
	if a.failInstall {
		return errors.New("fixture install failure")
	}
	a.trusted = true
	return nil
}
func (a *controllerTrust) Remove(context.Context, *NativeCA) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.removes++
	if a.failRemove {
		return errors.New("fixture removal failure")
	}
	a.trusted = false
	return nil
}
func controllerFixture(t *testing.T) (*nativeRuntimeController, NativeRuntimeRequest, *controllerTrust) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	disk := Disk{Root: root, Protection: NativeProtector{}}
	trust := &controllerTrust{}
	p := testProfile()
	req := NativeRuntimeRequest{Profile: p, Mode: NativeCompatibilityClaudePicker, Build: NativeCompatibilityClaudeBuild, Port: DefaultNativePort}
	store := &memoryStore{values: map[string]string{credentialID(p): "controller-fixture-key"}}
	options := NativeRuntimeOptions{Platform: "darwin", Trust: trust, Account: func(context.Context, NativeRuntimeRequest) (NativeCompatibilityAccount, error) {
		return NativeCompatibilityAccount{}, errors.New("fixture account unavailable")
	}, ObserveBuild: func(context.Context, NativeRuntimeRequest) (string, error) {
		return NativeCompatibilityClaudeBuild, nil
	}, EnsureCA: func(root string, hosts []string) (*NativeCA, error) {
		return EnsureNativeCAWithOptions(root, NativeCAOptions{Platform: "darwin", DNSNames: hosts, Protection: controllerKeyProtection{}, FileProtection: NativeProtector{}})
	}}
	return NewNativeRuntimeControllerWithOptions(disk, store, options).(*nativeRuntimeController), req, trust
}
func TestNativeControllerConsentAndPendingRecovery(t *testing.T) {
	ctx := context.Background()
	c, req, trust := controllerFixture(t)
	plan, err := c.Plan(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Fingerprint == "" || len(plan.Routes) == 0 || trust.installs != 0 {
		t.Fatal("plan mutated trust or omitted preview")
	}
	root, _ := c.root(req.Profile.Name)
	if _, err = os.Stat(filepath.Join(root, "runtime.json")); !os.IsNotExist(err) {
		t.Fatal("plan persisted consent")
	}
	if _, err = c.Enable(ctx, req, NativeRuntimeConsent{Trust: true}); err == nil {
		t.Fatal("missing application consent accepted")
	}
	trust.beforeInstall = func() {
		r, exists, e := c.load(req.Profile.Name)
		if e != nil || !exists || !r.Consent.Trust || !r.Consent.Application {
			t.Error("trust preceded durable consent")
		}
	}
	trust.failInstall = true
	s, err := c.Enable(ctx, req, controllerConsent(t, c, req))
	if err == nil || s.Ready || s.Trusted {
		t.Fatal("failed trust reported activation")
	}
	if _, exists, e := c.load(req.Profile.Name); e != nil || !exists {
		t.Fatal("pending record lost")
	}
	trust.failInstall = false
	s, err = c.Enable(ctx, req, controllerConsent(t, c, req))
	if err != nil || !s.Trusted || s.Ready || s.ProxyURL != "" {
		t.Fatalf("retry status: %+v %v", s, err)
	}
	altered := req
	altered.Port++
	if _, err = c.Enable(ctx, altered, controllerConsent(t, c, req)); err == nil {
		t.Fatal("configuration changed without disable")
	}
	trust.failRemove = true
	if err = c.Disable(ctx, req.Profile.Name); err == nil {
		t.Fatal("failed removal reported success")
	}
	if _, exists, _ := c.load(req.Profile.Name); !exists {
		t.Fatal("failed cleanup lost record")
	}
	trust.failRemove = false
	if err = c.Disable(ctx, req.Profile.Name); err != nil {
		t.Fatal(err)
	}
	if err = c.Disable(ctx, req.Profile.Name); err != nil {
		t.Fatal(err)
	}
	if err = c.Stop(ctx, req.Profile.Name); err != nil {
		t.Fatal(err)
	}
	s, err = c.Status(ctx, req.Profile.Name)
	if err != nil || s.Enabled {
		t.Fatal("absent status not disabled")
	}
}
func TestNativeControllerBuildAndPathGates(t *testing.T) {
	c, req, _ := controllerFixture(t)
	ctx := context.Background()
	c.options.ObserveBuild = nil
	if _, err := c.Plan(ctx, req); err == nil {
		t.Fatal("unattested build accepted")
	}
	c.options.ObserveBuild = func(context.Context, NativeRuntimeRequest) (string, error) { return "unknown", nil }
	if _, err := c.Enable(ctx, req, controllerConsent(t, c, req)); err == nil {
		t.Fatal("mismatched installed build accepted")
	}
	if _, err := c.Status(ctx, "../escape"); err == nil {
		t.Fatal("path traversal accepted")
	}
	c.options.Platform = "linux"
	if _, err := c.Plan(ctx, req); err == nil {
		t.Fatal("unsupported OS accepted")
	}
}
func TestNativeControllerOwnedReadinessAndStop(t *testing.T) {
	c, req, _ := controllerFixture(t)
	ctx := context.Background()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	req.Port = listener.Addr().(*net.TCPAddr).Port
	if _, err = c.Enable(ctx, req, controllerConsent(t, c, req)); err != nil {
		t.Fatal(err)
	}
	record, _, _ := c.load(req.Profile.Name)
	control, _ := newNativeControlServer(record.Token)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if control.Challenge(w, r) {
			return
		}
		if !control.Authorize(r) {
			http.Error(w, "unauthorized", 401)
			return
		}
		if r.URL.Path == "/_tairouter/native/stop" && r.Method == "POST" {
			nativeControlRespond(w, r, record.Token, 200, nil)
			go func() { time.Sleep(10 * time.Millisecond); _ = listener.Close() }()
			return
		}
		if r.URL.Path == "/_tairouter/native/health" {
			body, _ := json.Marshal(map[string]any{"status": "ready", "caFingerprint": record.Fingerprint, "leafFingerprint": strings.Repeat("a", 64), "leafNotAfter": time.Now().Add(time.Hour)})
			nativeControlRespond(w, r, record.Token, 200, body)
			return
		}
		http.NotFound(w, r)
	})}
	go server.Serve(listener)
	defer server.Close()
	ready, err := c.Ready(ctx, req.Profile.Name)
	if err != nil {
		t.Fatal(err)
	}
	if err = ready.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*NativeRuntimeReady){func(r *NativeRuntimeReady) { r.Profile = "other" }, func(r *NativeRuntimeReady) { r.Build = "other" }, func(r *NativeRuntimeReady) { r.Mode = NativeCompatibilityCodexObserve }, func(r *NativeRuntimeReady) { r.ProxyURL = "http://evil" }, func(r *NativeRuntimeReady) { r.CAPath = "other" }} {
		altered := ready
		mutate(&altered)
		if altered.Validate() == nil {
			t.Fatal("altered proof accepted")
		}
	}
	b, _ := json.Marshal(ready)
	if strings.Contains(string(b), record.Token) || strings.Contains(fmt.Sprintf("%+v %#v", ready, ready), record.Token) {
		t.Fatal("readiness diagnostic leaked token")
	}
	beforeStop, err := c.Ready(ctx, req.Profile.Name)
	if err != nil {
		t.Fatal(err)
	}
	ready.proof.expires = time.Now().Add(-time.Second)
	if ready.Validate() == nil {
		t.Fatal("expired proof accepted")
	}
	if err = c.Stop(ctx, req.Profile.Name); err != nil {
		t.Fatal(err)
	}
	if beforeStop.Validate() == nil {
		t.Fatal("stopped runtime retained a live settings proof")
	}
	if _, err = c.Ready(ctx, req.Profile.Name); err == nil {
		t.Fatal("stopped listener reported ready")
	}
}
func TestNativeControllerRefusesForeignListener(t *testing.T) {
	c, req, _ := controllerFixture(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	req.Port = listener.Addr().(*net.TCPAddr).Port
	if _, err = c.Enable(context.Background(), req, controllerConsent(t, c, req)); err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "foreign", 401) })}
	go server.Serve(listener)
	defer server.Close()
	if _, err = c.Ready(context.Background(), req.Profile.Name); err == nil {
		t.Fatal("foreign listener accepted")
	}
	if err = c.Disable(context.Background(), req.Profile.Name); err == nil {
		t.Fatal("foreign listener cleanup reported success")
	}
	if _, exists, _ := c.load(req.Profile.Name); !exists {
		t.Fatal("ownership record lost")
	}
}

func TestNativeControllerServeLoadsSecretWithoutPersistingIt(t *testing.T) {
	c, req, _ := controllerFixture(t)
	ctx := context.Background()
	if _, err := c.Enable(ctx, req, controllerConsent(t, c, req)); err != nil {
		t.Fatal(err)
	}
	c.options.Models = func(context.Context, Profile, string) ([]ClientModel, error) {
		return []ClientModel{{ID: "fixture"}}, nil
	}
	c.options.CLIModels = c.options.Models
	called := false
	c.options.Run = func(ctx context.Context, config NativeTLSConfig) error {
		called = true
		if config.Key != "controller-fixture-key" || config.Address != nativeRuntimeAddress(req) || config.CA == nil || len(config.Models) != 1 || len(config.Token) != 64 {
			t.Fatal("incorrect runtime configuration")
		}
		config.OnReady()
		unlock, err := c.lock(req.Profile.Name)
		if err != nil {
			t.Fatal("Serve retained mutation lock after binding")
		}
		unlock()
		return nil
	}
	if err := c.Serve(ctx, req.Profile.Name); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("runtime was not called")
	}
	root, _ := c.root(req.Profile.Name)
	b, err := os.ReadFile(filepath.Join(root, "runtime.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "controller-fixture-key") {
		t.Fatal("credential persisted outside OS store")
	}
}
func TestNativeControllerTrialExpiryAndIdentity(t *testing.T) {
	c, req, _ := controllerFixture(t)
	ctx := context.Background()
	c.options.Platform = "windows"
	req.Mode = NativeCompatibilityCodexAccountUITrial
	req.Build = NativeCompatibilityCodexBuild
	req.Trial = true
	c.options.ObserveBuild = func(context.Context, NativeRuntimeRequest) (string, error) { return NativeCompatibilityCodexBuild, nil }
	consent := controllerConsent(t, c, req)
	if _, err := c.Enable(ctx, req, consent); err == nil {
		t.Fatal("unattested trial accepted")
	}
	account := NativeCompatibilityAccount{ID: "account", UserID: "user", Plan: "plus", Structure: "personal"}
	c.options.Account = func(context.Context, NativeRuntimeRequest) (NativeCompatibilityAccount, error) { return account, nil }
	if _, err := c.Enable(ctx, req, NativeRuntimeConsent{Trust: true, Application: true, PlanFingerprint: consent.PlanFingerprint}); err == nil {
		t.Fatal("missing trial consent accepted")
	}
	if _, err := c.Enable(ctx, req, consent); err != nil {
		t.Fatal(err)
	}
	r, _, err := c.load(req.Profile.Name)
	if err != nil {
		t.Fatal(err)
	}
	if time.Until(r.TrialUntil) > 3*time.Minute || !c.trialValid(ctx, r, "controller-fixture-key") {
		t.Fatal("invalid bounded trial")
	}
	if _, err := c.Enable(ctx, req, consent); err != nil {
		t.Fatal(err)
	}
	again, _, _ := c.load(req.Profile.Name)
	if !again.TrialUntil.Equal(r.TrialUntil) {
		t.Fatal("retry extended trial")
	}
	account.ID = "different"
	if c.trialValid(ctx, r, "controller-fixture-key") {
		t.Fatal("changed account retained eligibility")
	}
	account = r.Account
	if c.trialValid(ctx, r, "different-key") {
		t.Fatal("changed key retained eligibility")
	}
	r.TrialUntil = time.Now().Add(-time.Second)
	if c.trialValid(ctx, r, "controller-fixture-key") {
		t.Fatal("expired trial accepted")
	}
}

func TestNativeControllerCatalogSurfaces(t *testing.T) {
	for _, mode := range []NativeCompatibilityMode{NativeCompatibilityClaudeFirstParty, NativeCompatibilityClaudePicker} {
		t.Run(string(mode), func(t *testing.T) {
			c, req, _ := controllerFixture(t)
			req.Mode = mode
			var queries []string
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				queries = append(queries, r.URL.RawQuery)
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Query().Get("ids") == "desktop" {
					_, _ = w.Write([]byte(`{"version":1,"models":[{"name":"desktop-only","labelOverride":"Desktop"}]}`))
				} else {
					_, _ = w.Write([]byte(`{"data":[{"id":"cli-only","display_name":"CLI"}]}`))
				}
			}))
			defer server.Close()
			req.Profile.Server = server.URL
			c.credentials = &memoryStore{values: map[string]string{credentialID(req.Profile): "fixture-key"}}
			c.options.Transport = server.Client().Transport
			if _, err := c.Enable(context.Background(), req, controllerConsent(t, c, req)); err != nil {
				t.Fatal(err)
			}
			c.options.Run = func(ctx context.Context, config NativeTLSConfig) error {
				config.OnReady()
				if mode == NativeCompatibilityClaudeFirstParty {
					if len(config.Models) != 1 || config.Models[0].ID != "cli-only" {
						t.Fatal("wrong first-party catalog")
					}
				} else {
					if len(config.Models) != 1 || config.Models[0].ID != "desktop-only" || len(config.CLIModels) != 1 || config.CLIModels[0].ID != "cli-only" {
						t.Fatal("mixed picker catalog surfaces")
					}
				}
				return nil
			}
			if err := c.Serve(context.Background(), req.Profile.Name); err != nil {
				t.Fatal(err)
			}
			expected := []string{"ids=cli"}
			if mode == NativeCompatibilityClaudePicker {
				expected = []string{"ids=desktop&format=desktop-config", "ids=cli"}
			}
			if !reflect.DeepEqual(queries, expected) {
				t.Fatalf("catalog queries %v", queries)
			}
		})
	}
}

func controllerConsent(t *testing.T, c *nativeRuntimeController, req NativeRuntimeRequest) NativeRuntimeConsent {
	t.Helper()
	ca, err := c.ca(req)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := c.policy(req)
	if err != nil {
		t.Fatal(err)
	}
	installation, _ := c.installation(context.Background(), req)
	return NativeRuntimeConsent{Trust: true, Application: true, AccountTrial: req.Trial, PlanFingerprint: ca.Fingerprint, PlanDigest: nativePlanDigest(req, ca.Fingerprint, policy, installation)}
}
func TestNativeControllerConsentBindsPreviewedCertificate(t *testing.T) {
	c, req, trust := controllerFixture(t)
	ctx := context.Background()
	plan, err := c.Plan(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	consent := NativeRuntimeConsent{Trust: true, Application: true, PlanFingerprint: plan.Fingerprint, PlanDigest: plan.PlanDigest}
	root, _ := c.root(req.Profile.Name)
	for _, name := range []string{"authority.json", "ca.pem", "leaf.pem"} {
		if err = os.Remove(filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = c.Enable(ctx, req, consent); err == nil {
		t.Fatal("replacement CA accepted under previous consent")
	}
	if trust.installs != 0 {
		t.Fatal("replacement CA enrolled")
	}
	if _, exists, _ := c.load(req.Profile.Name); exists {
		t.Fatal("invalid consent persisted")
	}
}

func TestNativeControllerConsentBindsFullPlan(t *testing.T) {
	c, req, _ := controllerFixture(t)
	ctx := context.Background()
	req.Port = 0
	plan, err := c.Plan(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	consent := NativeRuntimeConsent{Trust: true, Application: true, PlanFingerprint: plan.Fingerprint, PlanDigest: plan.PlanDigest}
	changed := req
	changed.Port = DefaultNativePort + 1
	if _, err = c.Enable(ctx, changed, consent); err == nil {
		t.Fatal("changed port accepted under old plan")
	}
	changed = req
	changed.Profile.Server = "https://different.example"
	if _, err = c.Enable(ctx, changed, consent); err == nil {
		t.Fatal("changed gateway accepted under old plan")
	}
	req.Port = DefaultNativePort
	if _, err = c.Enable(ctx, req, consent); err != nil {
		t.Fatal("default port normalization changed plan", err)
	}
}
