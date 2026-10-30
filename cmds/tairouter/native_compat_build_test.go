// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeBuildClaudeBundleInspection(t *testing.T) {
	root := realTemp(t)
	bundle := filepath.Join(root, "Claude.app")
	contents := filepath.Join(bundle, "Contents")
	if err := os.MkdirAll(filepath.Join(contents, "MacOS"), 0700); err != nil {
		t.Fatal(err)
	}
	plist := filepath.Join(contents, "Info.plist")
	if err := os.WriteFile(plist, []byte("fixture plist"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(contents, "MacOS", "Claude"), []byte("fixture executable"), 0700); err != nil {
		t.Fatal(err)
	}
	metadata := map[string]string{"CFBundleIdentifier": "com.anthropic.claudefordesktop", "CFBundleShortVersionString": NativeCompatibilityClaudeBuild, "CFBundleExecutable": "Claude", "CFBundlePackageType": "APPL"}
	signed := true
	mutate := false
	calls := 0
	resolver := NativeBuildResolver{Platform: "darwin", Bundles: []string{bundle}, Run: func(ctx context.Context, name string, args []string, input []byte) ([]byte, error) {
		calls++
		switch name {
		case "/usr/bin/plutil":
			if string(input) != "fixture plist" {
				t.Fatal("plist not passed through bounded reader")
			}
			return json.Marshal(metadata)
		case "/usr/bin/codesign":
			if !strings.Contains(strings.Join(args, " "), "Anthropic, PBC") {
				t.Fatal("signature does not bind publisher")
			}
			if mutate {
				_ = os.WriteFile(plist, []byte("changed"), 0600)
			}
			if signed {
				return nil, nil
			}
			return nil, errors.New("unsigned")
		default:
			t.Fatalf("unexpected command %s", name)
			return nil, nil
		}
	}}
	req := NativeRuntimeRequest{Mode: NativeCompatibilityClaudePicker, Build: NativeCompatibilityClaudeBuild}
	if version, err := resolver.Observe(context.Background(), req); err != nil || version != req.Build || calls != 2 {
		t.Fatalf("valid fixture rejected: %s %v", version, err)
	}
	first, err := resolver.ObserveInstallation(context.Background(), req)
	if err != nil || first.Root != bundle || first.ExecutableStamp == "" || first.ManifestDigest == "" {
		t.Fatalf("incomplete macOS proof: %+v %v", first, err)
	}
	if err := os.WriteFile(filepath.Join(contents, "MacOS", "Claude.new"), []byte("fixture executable"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(contents, "MacOS", "Claude.new"), filepath.Join(contents, "MacOS", "Claude")); err != nil {
		t.Fatal(err)
	}
	replaced, err := resolver.ObserveInstallation(context.Background(), req)
	if err != nil || replaced.ExecutableStamp == first.ExecutableStamp {
		t.Fatalf("same-version replacement retained proof: %+v %v", replaced, err)
	}
	moved := filepath.Join(root, "Other", "Claude.app")
	if err := os.MkdirAll(filepath.Dir(moved), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(bundle, moved); err != nil {
		t.Fatal(err)
	}
	resolver.Bundles = []string{bundle, moved}
	relocated, err := resolver.ObserveInstallation(context.Background(), req)
	if err != nil || relocated.Root != moved || relocated == replaced {
		t.Fatalf("relocation retained proof: %+v %v", relocated, err)
	}
	resolver.Bundles = []string{moved}
	bundle = moved
	plist = filepath.Join(moved, "Contents", "Info.plist")
	signed = false
	if _, err := resolver.Observe(context.Background(), req); err == nil {
		t.Fatal("unsigned bundle accepted")
	}
	signed = true
	metadata["CFBundleShortVersionString"] = "unknown"
	if _, err := resolver.Observe(context.Background(), req); err == nil {
		t.Fatal("unknown installed version accepted")
	}
	metadata["CFBundleShortVersionString"] = req.Build
	mutate = true
	if _, err := resolver.Observe(context.Background(), req); err == nil {
		t.Fatal("bundle changed during probe accepted")
	}
	resolver.Bundles = []string{bundle, bundle}
	if _, err := resolver.Observe(context.Background(), req); err == nil {
		t.Fatal("ambiguous installation accepted")
	}
}
func TestNativeBuildWindowsMetadata(t *testing.T) {
	req := NativeRuntimeRequest{Mode: NativeCompatibilityClaudePicker, Build: NativeCompatibilityClaudeBuild}
	stampA := "1:20:30:" + strings.Repeat("a", 64)
	stampB := "1:20:30:" + strings.Repeat("b", 64)
	result := map[string]string{"Version": req.Build, "Name": "Claude", "Publisher": "Anthropic, PBC", "Path": `C:\Users\fixture\Claude\claude.exe`, "ExecutableStamp": stampA}
	resolver := NativeBuildResolver{Platform: "windows", SystemRoot: `C:\Windows`, Run: func(ctx context.Context, name string, args []string, input []byte) ([]byte, error) {
		if !strings.Contains(name, "System32") || len(args) != 4 || !strings.Contains(args[3], "Assert-NativePath") || !strings.Contains(args[3], "Get-AuthenticodeSignature") {
			t.Fatal("unsafe metadata command")
		}
		return json.Marshal(result)
	}}
	if version, err := resolver.Observe(context.Background(), req); err != nil || version != req.Build {
		t.Fatalf("valid fixture rejected: %s %v", version, err)
	}
	first, err := resolver.ObserveInstallation(context.Background(), req)
	if err != nil || first.Root != result["Path"] || first.ExecutableStamp != stampA {
		t.Fatalf("incomplete Windows proof: %+v %v", first, err)
	}
	result["ExecutableStamp"] = stampB
	replaced, err := resolver.ObserveInstallation(context.Background(), req)
	if err != nil || replaced == first {
		t.Fatalf("Windows replacement retained proof: %+v %v", replaced, err)
	}
	result["Path"] = `C:\Users\fixture\Other\claude.exe`
	relocated, err := resolver.ObserveInstallation(context.Background(), req)
	if err != nil || relocated == replaced {
		t.Fatalf("Windows relocation retained proof: %+v %v", relocated, err)
	}
	result["ExecutableStamp"] = ""
	if _, err := resolver.ObserveInstallation(context.Background(), req); err == nil {
		t.Fatal("missing stamp accepted")
	}
	result["ExecutableStamp"] = "bad-stamp"
	if _, err := resolver.ObserveInstallation(context.Background(), req); err == nil {
		t.Fatal("malformed stamp accepted")
	}
	result["ExecutableStamp"] = stampB
	result["Path"] = "relative"
	if _, err := resolver.ObserveInstallation(context.Background(), req); err == nil {
		t.Fatal("noncanonical path accepted")
	}
	result["Path"] = `C:\Users\fixture\Other\claude.exe`
	result["Version"] = "unknown"
	if _, err := resolver.Observe(context.Background(), req); err == nil {
		t.Fatal("unknown version accepted")
	}
	result["Version"] = req.Build
	result["Publisher"] = "foreign"
	if _, err := resolver.Observe(context.Background(), req); err == nil {
		t.Fatal("foreign publisher accepted")
	}
	resolver.SystemRoot = "relative"
	if _, err := resolver.Observe(context.Background(), req); err == nil {
		t.Fatal("relative tool path accepted")
	}
}
func TestNativeControllerAdvisoryLockAndPortOwnership(t *testing.T) {
	c, req, _ := controllerFixture(t)
	ctx := context.Background()
	release, err := c.lock(req.Profile.Name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.lock("other"); err == nil {
		t.Fatal("cross-profile lock not exclusive")
	}
	release()
	release, err = c.lock(req.Profile.Name)
	if err != nil {
		t.Fatal("stable lock marker blocked reacquisition")
	}
	release()
	if _, err = c.Enable(ctx, req, controllerConsent(t, c, req)); err != nil {
		t.Fatal(err)
	}
	other := req
	other.Profile.Name = "other"
	if _, err = c.Plan(ctx, other); err == nil {
		t.Fatal("plan accepted owned port")
	}
	if _, err = c.Enable(ctx, other, controllerConsent(t, c, req)); err == nil {
		t.Fatal("enable accepted owned port")
	}
	other.Port++
	if _, err = c.Plan(ctx, other); err != nil {
		t.Fatal(err)
	}
}
