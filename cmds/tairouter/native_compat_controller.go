// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"
)

// NativeRuntimeOptions isolates OS trust, installed-build inspection and network
// work. Tests must inject these dependencies instead of modifying real trust.
type NativeRuntimeOptions struct {
	Platform            string
	Trust               NativeTrustAdapter
	EnsureCA            func(string, []string) (*NativeCA, error)
	ObserveBuild        func(context.Context, NativeRuntimeRequest) (string, error)
	ObserveInstallation func(context.Context, NativeRuntimeRequest) (NativeInstallationProof, error)
	Account             func(context.Context, NativeRuntimeRequest) (NativeCompatibilityAccount, error)
	Run                 func(context.Context, NativeTLSConfig) error
	Transport           http.RoundTripper
	Models              func(context.Context, Profile, string) ([]ClientModel, error)
	CLIModels           func(context.Context, Profile, string) ([]ClientModel, error)
}
type nativeRuntimeController struct {
	disk        Disk
	credentials Credentials
	options     NativeRuntimeOptions
}

func NewNativeRuntimeController(disk Disk, credentials Credentials) NativeRuntimeController {
	return NewNativeRuntimeControllerWithOptions(disk, credentials, NativeRuntimeOptions{})
}
func NewNativeRuntimeControllerWithOptions(disk Disk, credentials Credentials, options NativeRuntimeOptions) NativeRuntimeController {
	if disk.Protection == nil {
		disk.Protection = NativeProtector{}
	}
	if options.Platform == "" {
		options.Platform = runtime.GOOS
	}
	if options.ObserveBuild == nil && options.ObserveInstallation == nil {
		options.ObserveInstallation = (NativeBuildResolver{Platform: options.Platform}).ObserveInstallation
	}
	if options.Account == nil {
		options.Account = ObserveNativeCodexAccount
	}
	if options.Trust == nil {
		home, err := os.UserHomeDir()
		if err == nil {
			options.Trust, _ = NewNativeTrustAdapter(options.Platform, home, NativeTrustExecRunner)
		}
	}
	if options.Run == nil {
		options.Run = RunNativeTLS
	}
	return &nativeRuntimeController{disk, credentials, options}
}

type nativeRuntimeRecord struct {
	Version            int
	Request            NativeRuntimeRequest
	Consent            NativeRuntimeConsent
	Token, Fingerprint string
	TrialUntil         time.Time
	Account            NativeCompatibilityAccount
	Installation       NativeInstallationProof
}

func (c *nativeRuntimeController) root(name string) (string, error) {
	if !namePattern.MatchString(name) {
		return "", errors.New("invalid native runtime profile")
	}
	if !filepath.IsAbs(c.disk.Root) {
		return "", errors.New("state directory must be absolute")
	}
	root := filepath.Join(c.disk.Root, "native-tls", name)
	return root, safeParents(root)
}
func (c *nativeRuntimeController) load(name string) (nativeRuntimeRecord, bool, error) {
	var r nativeRuntimeRecord
	root, err := c.root(name)
	if err != nil {
		return r, false, err
	}
	b, exists, err := readConfig(filepath.Join(root, "runtime.json"))
	if err != nil || !exists {
		return r, exists, err
	}
	if json.Unmarshal(b, &r) != nil || r.Version != 1 || r.Request.Profile.Name != name || len(r.Token) != 64 || r.Fingerprint == "" || r.Consent.PlanFingerprint != r.Fingerprint {
		return r, true, errors.New("invalid native runtime record; preserve it for recovery")
	}
	_, err = c.policy(r.Request)
	return r, true, err
}
func (c *nativeRuntimeController) policy(req NativeRuntimeRequest) (NativeCompatibilityPolicy, error) {
	if err := req.Profile.Validate(); err != nil {
		return NativeCompatibilityPolicy{}, err
	}
	if req.Port == 0 {
		req.Port = DefaultNativePort
	}
	if req.Port < 1024 || req.Port > 65535 {
		return NativeCompatibilityPolicy{}, errors.New("native port must be fixed and between 1024 and 65535")
	}
	if req.Trial != (req.Mode == NativeCompatibilityCodexAccountUITrial) {
		return NativeCompatibilityPolicy{}, errors.New("account trial mode requires explicit trial selection")
	}
	return NewNativeCompatibilityPolicy(req.Mode, c.options.Platform, req.Build)
}
func (c *nativeRuntimeController) observed(ctx context.Context, req NativeRuntimeRequest) error {
	_, err := c.installation(ctx, req)
	return err
}

func (c *nativeRuntimeController) ca(req NativeRuntimeRequest) (*NativeCA, error) {
	root, err := c.root(req.Profile.Name)
	if err != nil {
		return nil, err
	}
	p, err := c.policy(req)
	if err != nil {
		return nil, err
	}
	if c.options.EnsureCA != nil {
		return c.options.EnsureCA(root, p.Hosts())
	}
	return EnsureNativeCAWithOptions(root, NativeCAOptions{Platform: c.options.Platform, DNSNames: p.Hosts(), FileProtection: c.disk.Protection})
}
func (c *nativeRuntimeController) trust(root string) NativeTrustManager {
	return NativeTrustManager{Root: root, Adapter: c.options.Trust, Protection: c.disk.Protection}
}

// A shared advisory lock serializes port allocation across profiles. Its stable
// file remains after exit; the OS releases ownership even after a crash.
func (c *nativeRuntimeController) lock(name string) (func(), error) {
	if _, err := c.root(name); err != nil {
		return nil, err
	}
	d := Disk{Root: filepath.Join(c.disk.Root, "native-tls"), Protection: c.disk.Protection}
	return d.Lock()
}
func (c *nativeRuntimeController) checkPort(req NativeRuntimeRequest) error {
	entries, err := os.ReadDir(filepath.Join(c.disk.Root, "native-tls"))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("symbolic links are not accepted in native runtime records")
		}
		if entry.Name() == req.Profile.Name || !entry.IsDir() {
			continue
		}
		r, exists, e := c.load(entry.Name())
		if e != nil {
			return errors.New("cannot validate native runtime port ownership; preserve existing records")
		}
		if exists && nativeRuntimeAddress(r.Request) == nativeRuntimeAddress(req) {
			return errors.New("native loopback port belongs to another profile; select a distinct fixed port")
		}
	}
	return nil
}
func (c *nativeRuntimeController) Plan(ctx context.Context, req NativeRuntimeRequest) (NativeRuntimePlan, error) {
	var plan NativeRuntimePlan
	p, err := c.policy(req)
	if err != nil {
		return plan, err
	}
	installation, err := c.installation(ctx, req)
	if err != nil {
		return plan, err
	}
	release, err := c.lock(req.Profile.Name)
	if err != nil {
		return plan, err
	}
	defer release()
	if err = c.checkPort(req); err != nil {
		return plan, err
	}
	ca, err := c.ca(req)
	if err != nil {
		return plan, err
	}
	address := nativeRuntimeAddress(req)
	return NativeRuntimePlan{PlanDigest: nativePlanDigest(req, ca.Fingerprint, p, installation), Profile: req.Profile.Name, Mode: req.Mode, Build: req.Build, Address: address, CAPath: ca.CertificatePath, Fingerprint: ca.Fingerprint, Hosts: p.Hosts(), Routes: p.Routes(), Listeners: []string{"HTTP CONNECT and authenticated control: " + address}, TrustChanges: []string{"Install only the owned CA in current-user trust"}, SettingsChanges: []string{"Apply the separately previewed native application settings"}, RequiresTrust: true, RequiresApplication: true, RequiresAccountTrial: req.Trial}, nil
}
func nativePlanDigest(req NativeRuntimeRequest, fingerprint string, policy NativeCompatibilityPolicy, installation ...NativeInstallationProof) string {
	if req.Port == 0 {
		req.Port = DefaultNativePort
	}
	data, _ := json.Marshal(struct {
		Request       NativeRuntimeRequest
		Fingerprint   string
		Hosts, Routes []string
		Installation  []NativeInstallationProof
	}{req, fingerprint, policy.Hosts(), policy.Routes(), installation})
	return digest(data)
}
func nativeRuntimeAddress(req NativeRuntimeRequest) string {
	port := req.Port
	if port == 0 {
		port = DefaultNativePort
	}
	return net.JoinHostPort("127.0.0.1", fmt.Sprint(port))
}
func (c *nativeRuntimeController) Enable(ctx context.Context, req NativeRuntimeRequest, consent NativeRuntimeConsent) (NativeRuntimeStatus, error) {
	if len(consent.PlanFingerprint) != 64 || len(consent.PlanDigest) != 64 {
		return NativeRuntimeStatus{}, errors.New("reviewed native CA fingerprint is required")
	}
	if !consent.Trust || !consent.Application || req.Trial && !consent.AccountTrial {
		return NativeRuntimeStatus{}, errors.New("separate trust, application and applicable account trial consent are required")
	}
	if _, err := c.policy(req); err != nil {
		return NativeRuntimeStatus{}, err
	}
	installation, err := c.installation(ctx, req)
	if err != nil {
		return NativeRuntimeStatus{}, err
	}
	release, err := c.lock(req.Profile.Name)
	if err != nil {
		return NativeRuntimeStatus{}, err
	}
	defer release()
	if err = c.checkPort(req); err != nil {
		return NativeRuntimeStatus{}, err
	}
	if req.Port == 0 {
		req.Port = DefaultNativePort
	}
	r, exists, err := c.load(req.Profile.Name)
	if err != nil {
		return NativeRuntimeStatus{}, err
	}
	if exists && req.Trial && !time.Now().Before(r.TrialUntil) {
		return NativeRuntimeStatus{}, errors.New("account trial expired; disable it before granting new consent")
	}
	if exists && (r.Request != req || r.Consent != consent) {
		return NativeRuntimeStatus{}, errors.New("native runtime configuration differs; disable it before changing consent or configuration")
	}
	ca, err := c.ca(req)
	if err != nil {
		return NativeRuntimeStatus{}, err
	}
	policy, _ := c.policy(req)
	if consent.PlanDigest != nativePlanDigest(req, ca.Fingerprint, policy, installation) {
		return NativeRuntimeStatus{}, errors.New("native request differs from the reviewed plan; preview and confirm again")
	}
	if ca.Fingerprint != consent.PlanFingerprint {
		return NativeRuntimeStatus{}, errors.New("native CA differs from the reviewed plan; preview and confirm again")
	}
	if !exists {
		r = nativeRuntimeRecord{Version: 1, Request: req, Consent: consent, Fingerprint: ca.Fingerprint, Installation: installation}
		if req.Trial {
			if c.options.Account == nil {
				return NativeRuntimeStatus{}, errors.New("native account attestation unavailable; account UI trial refused")
			}
			r.Account, err = c.options.Account(ctx, req)
			if err != nil {
				return NativeRuntimeStatus{}, err
			}
			r.TrialUntil = time.Now().Add(3 * time.Minute)
			p, _ := c.policy(req)
			if _, err = p.WithAccountUITrial(r.Account, r.TrialUntil, time.Now, func() bool { return true }); err != nil {
				return NativeRuntimeStatus{}, err
			}
		}
		if !c.installationMatches(ctx, r) {
			return NativeRuntimeStatus{}, errors.New("native installation changed during consent")
		}
		token := make([]byte, 32)
		if _, err = rand.Read(token); err != nil {
			return NativeRuntimeStatus{}, err
		}
		r.Token = hex.EncodeToString(token)
		b, err := json.Marshal(r)
		if err != nil {
			return NativeRuntimeStatus{}, err
		}
		root, _ := c.root(req.Profile.Name)
		if err = nativeDurableWrite(c.disk, filepath.Join(root, "runtime.json"), b, compareFile(filepath.Join(root, "runtime.json"), nil, false)); err != nil {
			return NativeRuntimeStatus{}, err
		}
	}
	if r.Fingerprint != ca.Fingerprint {
		return NativeRuntimeStatus{}, errors.New("native CA changed; preserve runtime record for recovery")
	}
	root, _ := c.root(req.Profile.Name)
	manager := c.trust(root)
	if err = manager.Install(ctx, ca); err != nil {
		return c.statusRecord(ctx, r, ca), err
	}
	return c.statusRecord(ctx, r, ca), nil
}

// Revalidate durable consent, account identity, package build and key generation
// for each trial rewrite. A changed or unavailable authority ends eligibility.
func (c *nativeRuntimeController) trialValid(ctx context.Context, r nativeRuntimeRecord, key string) bool {
	if !r.Consent.AccountTrial || !time.Now().Before(r.TrialUntil) || c.options.Account == nil || c.credentials == nil {
		return false
	}
	current, exists, err := c.load(r.Request.Profile.Name)
	if err != nil || !exists || current != r || !c.installationMatches(ctx, r) {
		return false
	}
	account, err := c.options.Account(ctx, r.Request)
	if err != nil || account != r.Account {
		return false
	}
	actual, err := c.credentials.Get(credentialID(r.Request.Profile))
	return err == nil && actual != "" && (key == "" || actual == key) && c.installationMatches(ctx, r)
}
func (c *nativeRuntimeController) statusRecord(ctx context.Context, r nativeRuntimeRecord, ca *NativeCA) NativeRuntimeStatus {
	s := NativeRuntimeStatus{Profile: r.Request.Profile.Name, Mode: r.Request.Mode, Build: r.Request.Build, Address: nativeRuntimeAddress(r.Request), CAPath: ca.CertificatePath, Fingerprint: r.Fingerprint, Enabled: true, Consent: r.Consent, TrialUntil: r.TrialUntil}
	root, _ := c.root(s.Profile)
	manager := c.trust(root)
	trust, err := manager.Status(ctx, ca)
	s.Trusted = err == nil && trust.Trusted && ca.Fingerprint == r.Fingerprint
	s.Ready = !time.Now().Before(ca.Certificate.NotBefore) && time.Now().Before(ca.Certificate.NotAfter) && s.Trusted && r.Consent.Trust && r.Consent.Application && (!r.Request.Trial || c.trialValid(ctx, r, "")) && c.installationMatches(ctx, r) && c.control(ctx, r, http.MethodGet, "health") == nil
	return s
}
func (c *nativeRuntimeController) publicCA(req NativeRuntimeRequest) (*NativeCA, error) {
	root, err := c.root(req.Profile.Name)
	if err != nil {
		return nil, err
	}
	return ReadNativeCAPublic(root)
}
func (c *nativeRuntimeController) Status(ctx context.Context, name string) (NativeRuntimeStatus, error) {
	r, exists, err := c.load(name)
	if err != nil || !exists {
		return NativeRuntimeStatus{}, err
	}
	ca, err := c.publicCA(r.Request)
	if err != nil {
		return NativeRuntimeStatus{}, err
	}
	return c.statusRecord(ctx, r, ca), nil
}
func (c *nativeRuntimeController) Ready(ctx context.Context, name string) (NativeRuntimeReady, error) {
	r, exists, err := c.load(name)
	if err != nil {
		return NativeRuntimeReady{}, err
	}
	if !exists {
		return NativeRuntimeReady{}, errors.New("native runtime is not enabled")
	}
	ca, err := c.publicCA(r.Request)
	if err != nil {
		return NativeRuntimeReady{}, err
	}
	if !c.statusRecord(ctx, r, ca).Ready {
		return NativeRuntimeReady{}, errors.New("native runtime is not ready")
	}
	proxy := (&url.URL{Scheme: "http", Host: nativeRuntimeAddress(r.Request), User: url.UserPassword("tairouter", NativeProxyPassword(r.Token))}).String()
	expiry := time.Now().Add(30 * time.Second)
	if r.Request.Trial && r.TrialUntil.Before(expiry) {
		expiry = r.TrialUntil
	}
	return NativeRuntimeReady{Profile: name, Mode: r.Request.Mode, Build: r.Request.Build, ProxyURL: proxy, CAPath: ca.CertificatePath, proof: &nativeReadyProof{profile: name, mode: r.Request.Mode, build: r.Request.Build, proxyURL: proxy, caPath: ca.CertificatePath, expires: expiry, check: func() bool {
		probeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		current, exists, e := c.load(name)
		if e != nil || !exists || current != r {
			return false
		}
		public, e := c.publicCA(current.Request)
		return e == nil && c.statusRecord(probeCtx, current, public).Ready
	}}}, nil
}
func (c *nativeRuntimeController) control(ctx context.Context, r nativeRuntimeRecord, method, action string) error {
	data, err := nativeControlRequest(ctx, nativeRuntimeAddress(r.Request), r.Token, method, "/_tairouter/native/"+action)
	if err != nil {
		return err
	}
	if action == "health" {
		var health struct {
			Status, CAFingerprint, LeafFingerprint string
			LeafNotAfter                           time.Time
		}
		if json.Unmarshal(data, &health) != nil || health.Status != "ready" || health.CAFingerprint != r.Fingerprint || !time.Now().Before(health.LeafNotAfter) {
			return errors.New("native listener is not ready")
		}
		fingerprint, e := hex.DecodeString(health.LeafFingerprint)
		if e != nil || len(fingerprint) != 32 {
			return errors.New("invalid served native leaf proof")
		}
	}
	return nil
}
func nativeConnectionRefused(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED)
}
func (c *nativeRuntimeController) stopRecord(ctx context.Context, r nativeRuntimeRecord) error {
	err := c.control(ctx, r, http.MethodPost, "stop")
	if nativeConnectionRefused(err) {
		return nil
	}
	if err != nil {
		return errors.New("cannot stop owned native runtime; retain recovery state")
	}
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("native runtime did not stop; retain recovery state")
		case <-tick.C:
			if nativeConnectionRefused(c.control(ctx, r, http.MethodGet, "health")) {
				return nil
			}
		}
	}
}
func (c *nativeRuntimeController) Stop(ctx context.Context, name string) error {
	release, err := c.lock(name)
	if err != nil {
		return err
	}
	defer release()
	r, exists, err := c.load(name)
	if err != nil || !exists {
		return err
	}
	return c.stopRecord(ctx, r)
}
func (c *nativeRuntimeController) Disable(ctx context.Context, name string) error {
	release, err := c.lock(name)
	if err != nil {
		return err
	}
	defer release()
	r, exists, err := c.load(name)
	if err != nil || !exists {
		return err
	}
	if err = c.stopRecord(ctx, r); err != nil {
		return err
	}
	ca, err := c.publicCA(r.Request)
	if err != nil {
		return err
	}
	if ca.Fingerprint != r.Fingerprint {
		return errors.New("native CA ownership conflict")
	}
	root, _ := c.root(name)
	manager := c.trust(root)
	if err = manager.Remove(ctx, ca); err != nil {
		return err
	}
	if err = os.Remove(filepath.Join(root, "runtime.json")); err != nil {
		return err
	}
	return nativeSyncDirectory(root)
}
func (c *nativeRuntimeController) Serve(ctx context.Context, name string) error {
	release, err := c.lock(name)
	if err != nil {
		return err
	}
	r, exists, err := c.load(name)
	if err != nil || !exists {
		release()
		if err != nil {
			return err
		}
		return errors.New("native runtime is not enabled")
	}
	if err = c.checkPort(r.Request); err != nil {
		release()
		return err
	}
	if !c.installationMatches(ctx, r) {
		release()
		return errors.New("native installation changed")
	}
	ca, err := c.ca(r.Request)
	if err != nil {
		release()
		return err
	}
	root, _ := c.root(name)
	manager := c.trust(root)
	trust, err := manager.Status(ctx, ca)
	if err != nil || !trust.Trusted || ca.Fingerprint != r.Fingerprint || !r.Consent.Trust || !r.Consent.Application {
		release()
		return errors.New("native runtime trust or consent is pending")
	}
	if c.credentials == nil {
		release()
		return ErrUnavailable
	}
	key, err := c.credentials.Get(credentialID(r.Request.Profile))
	if err != nil || key == "" {
		release()
		return ErrUnavailable
	}
	policy, err := c.policy(r.Request)
	if err != nil {
		release()
		return err
	}
	if r.Request.Trial {
		if !r.Consent.AccountTrial || c.options.Account == nil {
			release()
			return errors.New("native account trial attestation unavailable")
		}
		account, e := c.options.Account(ctx, r.Request)
		if e != nil || account != r.Account {
			release()
			return errors.New("native account context changed")
		}
		policy, err = policy.WithAccountUITrial(account, r.TrialUntil, time.Now, func() bool { return c.trialValid(ctx, r, key) })
		if err != nil {
			release()
			return err
		}
	}
	var models, cliModels []ClientModel
	client := "claude"
	if r.Request.Mode == NativeCompatibilityClaudePicker {
		client = "claude-desktop"
	}
	if c.options.Models != nil {
		models, err = c.options.Models(ctx, r.Request.Profile, key)
	} else if r.Request.Mode == NativeCompatibilityClaudePicker || r.Request.Mode == NativeCompatibilityClaudeFirstParty {
		api, e := NewConnectAPI(r.Request.Profile.Server)
		if e != nil {
			err = e
		} else {
			api.HTTP.Transport = c.options.Transport
			models, err = api.ModelsForClient(ctx, key, client)
		}
	}
	if err == nil && r.Request.Mode == NativeCompatibilityClaudeFirstParty {
		cliModels = models
	}
	if err == nil && r.Request.Mode == NativeCompatibilityClaudePicker {
		if c.options.CLIModels != nil {
			cliModels, err = c.options.CLIModels(ctx, r.Request.Profile, key)
		} else {
			api, e := NewConnectAPI(r.Request.Profile.Server)
			if e != nil {
				err = e
			} else {
				api.HTTP.Transport = c.options.Transport
				cliModels, err = api.ModelsForClient(ctx, key, "claude")
			}
		}
	}
	if err != nil {
		release()
		return errors.New("native model catalog unavailable")
	}
	if !c.installationMatches(ctx, r) {
		release()
		return errors.New("native installation changed while preparing runtime")
	}
	// Hold the mutation lock until the transport owns its listener. OnReady releases
	// it before the blocking server loop, allowing Stop and Disable to proceed.
	var once sync.Once
	unlock := func() { once.Do(release) }
	defer unlock()
	return c.options.Run(ctx, NativeTLSConfig{Address: nativeRuntimeAddress(r.Request), Policy: policy, CA: ca, Gateway: r.Request.Profile.Server, Key: key, Token: r.Token, Models: models, CLIModels: cliModels, TrialUntil: r.TrialUntil, OnReady: unlock, Transport: c.options.Transport})
}
