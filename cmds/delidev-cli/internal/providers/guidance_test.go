// SPDX-License-Identifier: Apache-2.0
package providers

import (
	"bytes"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNativeGuidanceMatchesCanonicalRegistry(t *testing.T) {
	want, err := GuidanceJSON()
	if err != nil {
		t.Fatal(err)
	}
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("source location unavailable")
	}
	path := filepath.Join(filepath.Dir(source), "../../../../apps/delidev/src-tauri/provider-guidance.generated.json")
	actual, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(actual, want) {
		t.Fatal("native guidance is stale; regenerate from internal/providers/cmd/guidance", err)
	}
	keys := 0
	for _, preset := range Presets() {
		for _, target := range []string{preset.Documentation, preset.KeyCreationURL} {
			if target == "" {
				continue
			}
			u, err := url.Parse(target)
			if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Port() != "" || u.RawQuery != "" || u.Fragment != "" {
				t.Fatal("guidance must be a fixed public HTTPS page without secret transport")
			}
		}
		if preset.KeyCreationURL != "" {
			keys++
		}
	}
	if keys != 32 {
		t.Fatal("keyless/hosted guidance identity mismatch")
	}
}
