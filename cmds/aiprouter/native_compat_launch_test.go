// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func launchProof(pac string) nativeCodexLaunchProof {
	return nativeCodexLaunchProof{Root: `C:\Program Files\WindowsApps\fixture`, ExecutableStamp: "exe-stamp", ManifestStamp: "manifest-stamp", ManifestDigest: "manifest-hash", Verified: true, PackageFullName: "OpenAI.Codex_26.924.2738.0_x64__2p2nqsd0c76g0", Family: nativeCodexFamily, Version: NativeCompatibilityCodexBuild, Executable: `app\ChatGPT.exe`, PID: 42, Arguments: "--proxy-pac-url=" + pac}
}
func TestNativeCodexCommandAttestation(t *testing.T) {
	pac := "http://127.0.0.1:17844/0123456789abcdef0123456789abcdef/proxy.pac"
	for _, test := range []struct {
		name   string
		mutate func(*nativeCodexLaunchProof)
	}{
		{"valid", func(*nativeCodexLaunchProof) {}},
		{"unverified", func(p *nativeCodexLaunchProof) { p.Verified = false }},
		{"build", func(p *nativeCodexLaunchProof) { p.Version = "26.925.0.0" }},
		{"full-name", func(p *nativeCodexLaunchProof) { p.PackageFullName = "OpenAI.Codex_26.925.0.0_x64__2p2nqsd0c76g0" }},
		{"family", func(p *nativeCodexLaunchProof) { p.Family = "OpenAI.Codex_attacker" }},
		{"executable", func(p *nativeCodexLaunchProof) { p.Executable = `evil\ChatGPT.exe` }},
		{"pid", func(p *nativeCodexLaunchProof) { p.PID = 0 }},
		{"arguments", func(p *nativeCodexLaunchProof) { p.Arguments = "--proxy-server=http://evil" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := func(ctx context.Context, name string, args []string, input []byte) ([]byte, error) {
				if !strings.HasSuffix(name, "powershell.exe") || len(args) != 4 || args[0] != "-NoProfile" || args[1] != "-NonInteractive" || args[2] != "-Command" || args[3] != nativeCodexPackageScript {
					t.Fatal("unexpected command")
				}
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("unbounded command")
				}
				var request map[string]string
				if json.Unmarshal(input, &request) != nil || request["operation"] != "launch" || request["pac"] != pac {
					t.Fatal("invalid command input")
				}
				proof := launchProof(pac)
				test.mutate(&proof)
				return json.Marshal(proof)
			}
			_, err := nativeCodexCommand(context.Background(), "windows", runner, "launch", pac)
			if (err == nil) != (test.name == "valid") {
				t.Fatalf("result %v", err)
			}
		})
	}
	for _, fragment := range []string{"Get-AppxPackage", "Package-Proof", "ReparsePoint", "Manifest identity", "Application already running", "AIPRouterCodexActivation]::Package", "ExecutablePath", "AIPRouterCodexActivation]::Arguments", "Package changed after operation"} {
		if !strings.Contains(nativeCodexPackageScript, fragment) {
			t.Fatalf("missing safety gate %s", fragment)
		}
	}
	for _, forbidden := range []string{"Stop-Process", "taskkill", "Start-Process", "Invoke-Expression"} {
		if strings.Contains(nativeCodexPackageScript, forbidden) {
			t.Fatal("unowned application action")
		}
	}
}
func TestNativeCodexCommandRefusesBeforeExecution(t *testing.T) {
	called := false
	run := func(context.Context, string, []string, []byte) ([]byte, error) { called = true; return nil, nil }
	for _, pac := range []string{"http://localhost:17844/0123456789abcdef/proxy.pac", "http://127.0.0.1:99999/0123456789abcdef/proxy.pac", "http://127.0.0.1:0/0123456789abcdef/proxy.pac", "http://127.0.0.1:17844/short/proxy.pac", "http://a:b@127.0.0.1:17844/0123456789abcdef/proxy.pac", "http://127.0.0.1:17844/0123456789abcdef/proxy.pac?x", "http://127.0.0.1:17844/0123456789abcdef/proxy.pac#x"} {
		if _, err := nativeCodexCommand(context.Background(), "windows", run, "launch", pac); err == nil {
			t.Fatal("invalid PAC accepted")
		}
	}
	if _, err := nativeCodexCommand(context.Background(), "darwin", run, "observe", ""); err == nil {
		t.Fatal("platform accepted")
	}
	if called {
		t.Fatal("refused input executed")
	}
	run = func(context.Context, string, []string, []byte) ([]byte, error) {
		return nil, errors.New("sensitive command diagnostic")
	}
	if _, err := nativeCodexCommand(context.Background(), "windows", run, "observe", ""); err == nil || strings.Contains(err.Error(), "sensitive") {
		t.Fatal("command diagnostic exposed")
	}
}
func TestNativeCodexLaunchOwnedRuntimeLifecycle(t *testing.T) {
	c, req, trust := controllerFixture(t)
	c.options.Platform = "windows"
	req.Mode = NativeCompatibilityCodexObserve
	req.Build = NativeCompatibilityCodexBuild
	installed := launchProof("").installation()
	c.options.ObserveInstallation = func(context.Context, NativeRuntimeRequest) (NativeInstallationProof, error) {
		return installed, nil
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	req.Port = listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls int
	var pac string
	mutateDuringLaunch := false
	runner := func(_ context.Context, _ string, _ []string, input []byte) ([]byte, error) {
		calls++
		var request map[string]string
		json.Unmarshal(input, &request)
		pac = request["pac"]
		if mutateDuringLaunch {
			original := installed
			installed.ExecutableStamp += "replacement"
			if _, e := c.installation(ctx, req); e != nil {
				t.Fatal(e)
			}
			installed = original
			if _, e := c.installation(ctx, req); e != nil {
				t.Fatal(e)
			}
		}
		return json.Marshal(launchProof(pac))
	}
	if err := c.launchCodex(ctx, req.Profile.Name, runner); err == nil || calls != 0 {
		t.Fatal("disabled runtime launched")
	}
	plan, err := c.Plan(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Enable(ctx, req, NativeRuntimeConsent{Trust: true, Application: true, PlanFingerprint: plan.Fingerprint, PlanDigest: plan.PlanDigest}); err != nil {
		t.Fatal(err)
	}
	if err := c.launchCodex(ctx, req.Profile.Name, runner); err == nil || calls != 0 {
		t.Fatal("stopped runtime launched")
	}
	ready := make(chan struct{})
	done := make(chan error, 1)
	c.options.Run = func(ctx context.Context, cfg NativeTLSConfig) error {
		previous := cfg.OnReady
		cfg.OnReady = func() { previous(); close(ready) }
		return RunNativeTLS(ctx, cfg)
	}
	go func() { done <- c.Serve(ctx, req.Profile.Name) }()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("serve: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("readiness timeout")
	}
	if err = c.launchCodex(ctx, req.Profile.Name, runner); err != nil || calls != 1 {
		t.Fatalf("launch %v calls %d", err, calls)
	}
	if err = validateNativePAC(pac, nativeRuntimeAddress(req)); err != nil {
		t.Fatal(err)
	}
	res, err := http.Get(pac)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatal("owned PAC unavailable")
	}
	mutateDuringLaunch = true
	if err = c.launchCodex(ctx, req.Profile.Name, runner); err == nil {
		t.Fatal("A-B-A during launch retained consent")
	}
	if err = c.Stop(ctx, req.Profile.Name); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("owned listener survived stop")
	}
	if err = c.launchCodex(ctx, req.Profile.Name, runner); err == nil || calls != 2 {
		t.Fatal("stopped runtime launched")
	}
	if res, err = http.Get(pac); err == nil {
		res.Body.Close()
		t.Fatal("stopped PAC still served")
	}
	root, _ := c.root(req.Profile.Name)
	if _, err = os.Stat(filepath.Join(root, "runtime.json")); err != nil {
		t.Fatal("stop discarded recovery ownership")
	}
	if trust.removes != 0 {
		t.Fatal("stop changed trust")
	}
	if err = c.Disable(ctx, req.Profile.Name); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(root, "runtime.json")); !os.IsNotExist(err) {
		t.Fatal("disable retained consent")
	}
	if calls != 2 || trust.removes != 1 {
		t.Fatal(fmt.Sprint("unowned cleanup", calls, trust.removes))
	}
}
