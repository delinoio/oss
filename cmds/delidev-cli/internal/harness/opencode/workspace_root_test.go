package opencode

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func globalRootConfig(t *testing.T) apiSessionConfig {
	t.Helper()
	c := fixtureOwnedAPIConfig(t)
	root, err := GlobalWorkspaceRoot(c.Workspace)
	if err != nil {
		t.Fatal(err)
	}
	c.NativeRoot, c.Root = "", root
	return c
}

func TestGlobalRootKeepsMetadataSeparateAndInstructionsSessionOnly(t *testing.T) {
	c := globalRootConfig(t)
	_, profile, err := prepareAPISession(c)
	if err != nil || profile.WorkspaceRoot.native != "/" || profile.WorkspaceRoot.boundary != filesystemBoundary(c.Workspace) || profile.ProjectConfig.Root != profile.WorkspaceRoot.boundary || profile.ProjectInstructions.Root != c.Workspace {
		t.Fatal("global metadata supplied filesystem or instruction authority", err)
	}
	for _, change := range []func(*apiSessionConfig){
		func(c *apiSessionConfig) { c.Root.native = c.Root.boundary },
		func(c *apiSessionConfig) { c.Root.native = "foreign" },
		func(c *apiSessionConfig) { c.Root.boundary = c.Workspace },
		func(c *apiSessionConfig) { c.Root.directory = filepath.Dir(c.Workspace) },
		func(c *apiSessionConfig) { c.Root.kind = 0 },
		func(c *apiSessionConfig) { c.NativeRoot = c.Root.boundary },
	} {
		changed := c
		change(&changed)
		// Unix '/' already is the filesystem boundary; exercise a genuinely
		// different native identity there as well as Windows drive substitution.
		if changed.Root == c.Root && changed.NativeRoot == "" {
			changed.Root.native = c.Workspace
		}
		if _, _, err := prepareAPISession(changed); err == nil {
			t.Fatal("foreign root gained initializer authority")
		}
	}
}

func TestGlobalRootRechecksGitAndEveryAncestorConfigurationWithoutMutation(t *testing.T) {
	for _, name := range []string{".git", "opencode.json", "opencode.jsonc", ".opencode"} {
		for _, at := range []string{"workspace", "parent"} {
			t.Run(name+"/"+at, func(t *testing.T) {
				c := globalRootConfig(t)
				_, profile, err := prepareAPISession(c)
				if err != nil {
					t.Fatal(err)
				}
				directory := c.Workspace
				if at == "parent" {
					directory = filepath.Dir(directory)
				}
				path := filepath.Join(directory, name)
				original := []byte("private unselected source")
				if err := os.WriteFile(path, original, 0600); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Remove(path) })
				s := &sessionAPI{apiProfile: profile, apiVerified: true}
				if s.verifyInstructions(context.Background(), "root-change") == nil || s.problem == nil || s.apiVerified {
					t.Fatal("new ancestor authority did not latch uncertainty")
				}
				after, err := os.ReadFile(path)
				if err != nil || string(after) != string(original) {
					t.Fatal("unselected source was changed")
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if s.problem == nil || s.apiVerified {
					t.Fatal("source removal restored mutation authority")
				}
			})
		}
	}
}

func TestGlobalRootNativeContextRequiresExactDirectoryAndSlash(t *testing.T) {
	for _, agent := range []PrimaryAgent{BuildAgent, PlanAgent} {
		for _, scenario := range []string{"original", "foreign-root", "drive-root", "foreign-directory", "changed-boundary"} {
			t.Run(string(agent)+"/"+scenario, func(t *testing.T) {
				c := globalRootConfig(t)
				c.Settings.Agent = agent
				_, profile, err := prepareAPISession(c)
				if err != nil {
					t.Fatal(err)
				}
				home := filepath.Dir(c.Probe.Home)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					switch r.URL.Path {
					case "/path":
						root, directory := "/", c.Workspace
						if scenario == "foreign-root" {
							root = c.Workspace
						}
						if scenario == "drive-root" {
							root = c.Root.boundary
							if root == "/" {
								root = `C:\`
							}
						}
						if scenario == "foreign-directory" {
							directory = home
						}
						_ = json.NewEncoder(w).Encode(map[string]any{"home": home, "state": filepath.Join(home, "state", "opencode"), "config": filepath.Join(home, "config", "opencode"), "worktree": root, "directory": directory})
					case "/agent":
						inventory := fixtureAgentInventory(home)
						selected, _ := expectedPrimaryAgent(agent, home, "/")
						index := 0
						if agent == PlanAgent {
							index = 1
						}
						inventory[index] = selected
						_ = json.NewEncoder(w).Encode(inventory)
					default:
						t.Error("unexpected native read")
					}
				}))
				defer server.Close()
				s := &sessionAPI{client: server.Client(), origin: server.URL, password: "private-fixture", cwd: c.Workspace, gate: make(chan struct{}, 1), apiProfile: profile, apiVerified: true, alive: func() error { return nil }}
				if scenario == "changed-boundary" {
					copy := *profile.WorkspaceRoot
					copy.boundary = c.Workspace
					profile.WorkspaceRoot = &copy
				}
				err = s.verifyNativeContext(context.Background(), c)
				if (err == nil) != (scenario == "original") || err == nil && s.runtimeRoot != "/" {
					t.Fatal("native metadata or changed filesystem authority was accepted", err)
				}
			})
		}
	}
}
