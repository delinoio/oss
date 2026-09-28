package opencode

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func projectConfigFixture(t *testing.T) apiSessionConfig {
	t.Helper()
	c := fixtureOwnedAPIConfig(t)
	c.NativeRoot = c.Workspace
	c.Workspace = filepath.Join(c.NativeRoot, "nested", "workspace")
	if err := os.MkdirAll(c.Workspace, 0700); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestEveryNativeProjectConfigSourceBlocksBeforeProcessOrClaim(t *testing.T) {
	for _, name := range []string{"opencode.json", "opencode.jsonc", ".opencode"} {
		for _, at := range []string{"root", "parent", "workspace"} {
			t.Run(name+"/"+at, func(t *testing.T) {
				c := projectConfigFixture(t)
				directory := c.Workspace
				if at == "root" {
					directory = c.NativeRoot
				}
				if at == "parent" {
					directory = filepath.Dir(c.Workspace)
				}
				path := filepath.Join(directory, name)
				original := []byte(`{"references":{"unselected":{"path":"private-unselected-path","description":"private-source-sentinel"}}}`)
				if name == ".opencode" {
					if err := os.Mkdir(path, 0700); err != nil {
						t.Fatal(err)
					}
				} else if err := os.WriteFile(path, original, 0600); err != nil {
					t.Fatal(err)
				}
				claims := 0
				c.Claim = func(context.Context, SessionClaim) error { claims++; return nil }
				var logs bytes.Buffer
				c.Probe.Process.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
				api, err := OpenOwnedAPI(context.Background(), c)
				if err == nil || domain.SafeError(err).Code != domain.Unsupported || api != nil || claims != 0 {
					t.Fatal("unselected native configuration reached execution")
				}
				entries, readErr := os.ReadDir(c.Probe.Process.Directory)
				if readErr != nil && !os.IsNotExist(readErr) || len(entries) != 0 {
					t.Fatal("native launch preceded source validation")
				}
				if name != ".opencode" {
					after, err := os.ReadFile(path)
					if err != nil || !bytes.Equal(original, after) {
						t.Fatal("unselected config was rewritten")
					}
				}
				if !strings.Contains(logs.String(), "opencode_project_config_source_refused") {
					t.Fatal("missing source diagnostic")
				}
				for _, secret := range []string{c.Workspace, c.NativeRoot, c.Token, "private-source-sentinel", "private-unselected-path"} {
					if strings.Contains(logs.String(), secret) {
						t.Fatal("source diagnostic leaked private context")
					}
				}
			})
		}
	}
}

func TestNativeConfigScopeStopsAtOriginalRootAndRejectsLinks(t *testing.T) {
	c := projectConfigFixture(t)
	scope := &projectConfigScope{Directory: c.Workspace, Root: c.NativeRoot}
	if err := scope.inspect(); err != nil {
		t.Fatal(err)
	}
	// A sibling source is outside native upward discovery and remains untouched.
	outside := filepath.Join(filepath.Dir(c.NativeRoot), "opencode.json")
	if err := os.WriteFile(outside, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := scope.inspect(); err != nil {
		t.Fatal("inspection escaped native root", err)
	}
	path := filepath.Join(c.Workspace, "opencode.json")
	if err := os.Symlink(outside, path); err != nil {
		t.Skip("symbolic links unavailable")
	}
	if scope.inspect() == nil {
		t.Fatal("linked native config was treated as absent")
	}
	if err := os.Remove(outside); err != nil {
		t.Fatal(err)
	}
	if scope.inspect() == nil {
		t.Fatal("broken native config link was treated as absent")
	}
}

func TestNewNativeProjectSourcesLatchOriginalMutationScope(t *testing.T) {
	for _, name := range []string{"opencode.json", "opencode.jsonc", ".opencode"} {
		t.Run(name, func(t *testing.T) {
			c := projectConfigFixture(t)
			_, p, err := prepareAPISession(c)
			if err != nil {
				t.Fatal(err)
			}
			s := &sessionAPI{apiProfile: p, apiVerified: true}
			path := filepath.Join(c.NativeRoot, name)
			if err := os.WriteFile(path, []byte(`{}`), 0600); err != nil {
				t.Fatal(err)
			}
			if s.verifyInstructions(context.Background(), "source-change") == nil || s.apiVerified || s.problem == nil {
				t.Fatal("new source did not block original session")
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if s.apiVerified || s.problem == nil {
				t.Fatal("removal cleared original uncertainty")
			}
		})
	}
}
