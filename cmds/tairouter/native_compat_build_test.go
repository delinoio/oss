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
	result := map[string]string{"Version": req.Build, "Name": "Claude", "Publisher": "Anthropic, PBC"}
	resolver := NativeBuildResolver{Platform: "windows", SystemRoot: `C:\Windows`, Run: func(ctx context.Context, name string, args []string, input []byte) ([]byte, error) {
		if !strings.Contains(name, "System32") || len(args) != 4 || !strings.Contains(args[3], "Assert-NativePath") || !strings.Contains(args[3], "Get-AuthenticodeSignature") {
			t.Fatal("unsafe metadata command")
		}
		return json.Marshal(result)
	}}
	if version, err := resolver.Observe(context.Background(), req); err != nil || version != req.Build {
		t.Fatalf("valid fixture rejected: %s %v", version, err)
	}
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
