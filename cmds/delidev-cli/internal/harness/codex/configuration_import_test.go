// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func nativeConfigurationFixture(t *testing.T) (domain.NativeConfigurationRead, map[string][]byte) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "home")
	outer := filepath.Join(base, "project")
	inner := filepath.Join(outer, "child")
	values := map[string]string{filepath.Join(home, "config.toml"): "model_reasoning_effort = 'low'\napi_key = 'SECRET-CONFIG'\n[mcp_servers.private]\ntoken = 'SECRET-MCP'\n", filepath.Join(home, "auth.json"): "SECRET-AUTH", filepath.Join(home, "AGENTS.md"): "Home instructions\n", filepath.Join(outer, ".codex", "config.toml"): "model_reasoning_effort = 'high'\n[hooks]\ncommand = 'SECRET-HOOK'\n", filepath.Join(outer, "AGENTS.md"): "Unselected overridden instructions\n", filepath.Join(outer, "AGENTS.override.md"): "Selected outer override\n", filepath.Join(inner, "AGENTS.override.md"): "", filepath.Join(inner, "AGENTS.md"): "Inner fallback instructions\n", filepath.Join(inner, "memory.md"): "SECRET-MEMORY"}
	originals := map[string][]byte{}
	for path, value := range values {
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
		originals[path] = []byte(value)
	}
	return domain.NativeConfigurationRead{Scopes: []domain.NativeConfigurationScope{{Kind: domain.NativeConfigurationHome, Path: home}, {Kind: domain.NativeConfigurationProject, Path: outer}, {Kind: domain.NativeConfigurationProject, Path: inner}}}, originals
}
func TestNativeConfigurationSelectedLayersRedactAndPreserveSources(t *testing.T) {
	request, originals := nativeConfigurationFixture(t)
	snapshot, err := ReadConfiguration(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(snapshot)
	for _, marker := range []string{"SECRET-", "Unselected overridden"} {
		if bytes.Contains(raw, []byte(marker)) {
			t.Fatalf("preview leaked excluded source: %s", marker)
		}
	}
	instructions := []string{}
	efforts := []string{}
	unsupported := 0
	for _, entry := range snapshot.Entries {
		switch entry.Kind {
		case domain.NativeConfigurationInstruction:
			instructions = append(instructions, entry.Value)
		case domain.NativeConfigurationSetting:
			efforts = append(efforts, entry.Value)
		case domain.NativeConfigurationUnsupported:
			unsupported++
			if entry.Value != "" {
				t.Fatal("unsupported value exposed")
			}
		}
	}
	if strings.Join(instructions, "|") != "Home instructions\n|Selected outer override\n|Inner fallback instructions\n" || strings.Join(efforts, "|") != "low|high" || unsupported != 3 {
		t.Fatalf("unexpected exact layers: %+v", snapshot.Entries)
	}
	for path, original := range originals {
		current, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(original, current) {
			t.Fatal("native source changed")
		}
	}
	request.ExpectedDigest = snapshot.Digest
	if _, err = ReadConfiguration(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(request.Scopes[1].Path, ".codex", "config.toml"), []byte("model_reasoning_effort = 'medium'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = ReadConfiguration(context.Background(), request); domain.SafeError(err).Code != domain.Conflict {
		t.Fatalf("changed source accepted: %v", err)
	}
}
func TestNativeConfigurationReadRejectsUnboundedOrIndirectScope(t *testing.T) {
	request, _ := nativeConfigurationFixture(t)
	t.Run("symlink source", func(t *testing.T) {
		home := request.Scopes[0].Path
		os.Remove(filepath.Join(home, "AGENTS.md"))
		if err := os.Symlink(filepath.Join(request.Scopes[1].Path, "AGENTS.md"), filepath.Join(home, "AGENTS.md")); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadConfiguration(context.Background(), request); err == nil {
			t.Fatal("followed symlink")
		}
	})
	t.Run("scope order", func(t *testing.T) {
		bad := request
		bad.Scopes = []domain.NativeConfigurationScope{request.Scopes[2], request.Scopes[1]}
		if bad.Validate() == nil {
			t.Fatal("accepted reversed project layers")
		}
	})
	t.Run("unknown value", func(t *testing.T) {
		if domain.NativeConfigurationSettingSupported("model_reasoning_effort", "SECRET-UNKNOWN") {
			t.Fatal("unknown effort supported")
		}
	})
}
func TestNativeConfigurationRemotePathAndManifestBounds(t *testing.T) {
	windows := domain.NativeConfigurationRead{Scopes: []domain.NativeConfigurationScope{{Kind: domain.NativeConfigurationHome, Path: `C:\Users\fixture\.codex`}, {Kind: domain.NativeConfigurationProject, Path: `D:\work\project`}, {Kind: domain.NativeConfigurationProject, Path: `D:\work\project\child`}}}
	if windows.Validate() != nil {
		t.Fatal("server OS rejected canonical remote Worker paths")
	}
	windows.Scopes[2].Path = `D:\work\project\..\outside`
	if windows.Validate() == nil {
		t.Fatal("accepted unclean remote path")
	}
	request, _ := nativeConfigurationFixture(t)
	snapshot, err := ReadConfiguration(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{"instruction", "file", "unsupported"} {
		t.Run(change, func(t *testing.T) {
			raw, _ := json.Marshal(snapshot)
			var bad domain.NativeConfigurationSnapshot
			json.Unmarshal(raw, &bad)
			switch change {
			case "instruction":
				for i := range bad.Entries {
					if bad.Entries[i].Kind == domain.NativeConfigurationInstruction {
						bad.Entries[i].Value = "Injected instructions"
						break
					}
				}
			case "file":
				bad.Files[0].Name = "auth.json"
				bad.Digest = bad.SourceDigest()
			case "unsupported":
				for i := range bad.Entries {
					if bad.Entries[i].Kind == domain.NativeConfigurationUnsupported {
						bad.Entries[i].Name = "SECRET-UNKNOWN-KEY"
						break
					}
				}
			}
			if bad.Validate() == nil {
				t.Fatal("malformed source proof accepted")
			}
		})
	}
}
