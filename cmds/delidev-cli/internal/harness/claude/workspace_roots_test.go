package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestClaudeWorkspaceRootsBindOriginalOrderAndPrimary(t *testing.T) {
	for _, scenario := range []string{"valid", "single", "missing-primary", "duplicate", "relative", "noncanonical", "absent", "file", "symlink", "overflow"} {
		t.Run(scenario, func(t *testing.T) {
			cfg, _ := apiFixtureConfig(t, "roots")
			first, last := filepath.Join(filepath.Dir(cfg.Workspace), "first"), filepath.Join(filepath.Dir(cfg.Workspace), "last root")
			for _, root := range []string{first, last} {
				if err := os.Mkdir(root, 0700); err != nil {
					t.Fatal(err)
				}
			}
			cfg.WorkspaceRoots = []string{first, cfg.Workspace, last}
			switch scenario {
			case "single":
				cfg.WorkspaceRoots = []string{cfg.Workspace}
			case "missing-primary":
				cfg.WorkspaceRoots = []string{first, last}
			case "duplicate":
				cfg.WorkspaceRoots[2] = first
			case "relative":
				cfg.WorkspaceRoots[2] = "relative"
			case "noncanonical":
				cfg.WorkspaceRoots[2] = first + string(filepath.Separator) + "."
			case "absent":
				cfg.WorkspaceRoots[2] = filepath.Join(last, "missing")
			case "file":
				file := filepath.Join(last, "file")
				if err := os.WriteFile(file, []byte("fixture"), 0600); err != nil {
					t.Fatal(err)
				}
				cfg.WorkspaceRoots[2] = file
			case "symlink":
				link := filepath.Join(last, "link")
				if err := os.Symlink(first, link); err != nil {
					t.Skip("symlink unavailable")
				}
				cfg.WorkspaceRoots[2] = link
			case "overflow":
				cfg.WorkspaceRoots = make([]string, 101)
			}
			prepared, err := prepareAPIStream(cfg)
			if scenario != "valid" {
				if err == nil {
					t.Fatal("invalid roots permitted")
				}
				return
			}
			if err != nil || prepared.Cwd != cfg.Workspace || !slices.Contains(prepared.Args, "--add-dir="+first) || !slices.Contains(prepared.Args, "--add-dir="+last) || slices.Contains(prepared.Args, "--add-dir="+cfg.Workspace) || !slices.Contains(prepared.Args, "--setting-sources=") {
				t.Fatal("original root scope changed", err)
			}
			digest := checkpointConfiguration(cfg, cfg.API.ServerOrigin)
			cfg.WorkspaceRoots[0], cfg.WorkspaceRoots[2] = cfg.WorkspaceRoots[2], cfg.WorkspaceRoots[0]
			if digest == checkpointConfiguration(cfg, cfg.API.ServerOrigin) {
				t.Fatal("checkpoint lost original root order")
			}
		})
	}
}

func TestClaudeSingleRootCheckpointConfigurationKeepsOriginalDigest(t *testing.T) {
	cfg, _ := apiFixtureConfig(t, "legacy-roots")
	origin := cfg.API.ServerOrigin
	legacy := struct{ Version, Executable, Directory, Runtime, Home, Workspace, Model, Effort, Permission, Instructions, Origin string }{cfg.Version, cfg.Process.Executable, cfg.Process.Directory, cfg.Process.Cwd, cfg.Home, cfg.Workspace, cfg.Model, string(cfg.Effort), string(cfg.Permission), checkpointDigest([]byte(cfg.Instructions)), origin}
	raw, _ := json.Marshal(legacy)
	if checkpointConfiguration(cfg, origin) != checkpointDigest(raw) {
		t.Fatal("legacy single-root checkpoint changed")
	}
}

func TestManualNativeMultipleRootCheckpointContinuation(t *testing.T) {
	nativeClosedSessionContinuation(t, nativeCheckpointMultipleRoots)
}

func TestClaudeWorkspaceCheckpointRejectsChangedOriginalScope(t *testing.T) {
	for _, change := range []string{"unchanged", "omitted", "reordered", "primary", "missing-directory", "replaced-directory"} {
		t.Run(change, func(t *testing.T) {
			s, _ := continuationFixture(t)
			for _, message := range s.history.messages {
				s.current.seen[message.NativeID] = true
			}
			extra := filepath.Join(filepath.Dir(s.config.Workspace), "additional-root")
			if err := os.Mkdir(extra, 0700); err != nil {
				t.Fatal(err)
			}
			s.config.WorkspaceRoots = []string{extra, s.config.Workspace}
			closed, err := s.CloseForContinuation(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			raw, ref, err := closed.RetainCheckpoint(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(raw, []byte(extra)) {
				t.Fatal("native metadata checkpoint exposed root path")
			}
			cfg := s.config
			cfg.WorkspaceRoots = slices.Clone(cfg.WorkspaceRoots)
			cfg.API = APIConfig{ServerOrigin: s.serverOrigin}
			switch change {
			case "omitted":
				cfg.WorkspaceRoots = nil
			case "reordered":
				slices.Reverse(cfg.WorkspaceRoots)
			case "primary":
				cfg.Workspace = extra
			case "missing-directory":
				if err := os.Remove(extra); err != nil {
					t.Fatal(err)
				}
			case "replaced-directory":
				if err := os.Remove(extra); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(cfg.Workspace, extra); err != nil {
					t.Skip("symlink unavailable")
				}
			}
			inspection := cfg
			digest := checkpointDigest([]byte(cfg.Instructions))
			inspection.Instructions = ""
			inspected := InspectCheckpoint(context.Background(), inspection, digest, raw, ref)
			restored, err := RestoreCheckpoint(context.Background(), cfg, raw, ref)
			if change != "unchanged" {
				if inspected == nil || err == nil || restored != nil {
					t.Fatal("changed roots gained restoration or comparison authority")
				}
				return
			}
			if inspected != nil || err != nil {
				t.Fatal("original roots failed checkpoint inspection", inspected, err)
			}
			cfg.WorkspaceRoots[0] = "caller mutation"
			if restored.previous.config.WorkspaceRoots[0] != extra {
				t.Fatal("restored root scope aliases caller")
			}
		})
	}
}

func TestClaudeSessionOwnsOriginalWorkspaceRootList(t *testing.T) {
	cfg, _ := apiFixtureConfig(t, "roots-alias")
	extra := filepath.Join(filepath.Dir(cfg.Workspace), "extra")
	if err := os.Mkdir(extra, 0700); err != nil {
		t.Fatal(err)
	}
	cfg.WorkspaceRoots = []string{extra, cfg.Workspace}
	s, err := OpenAPISession(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg.WorkspaceRoots[0] = "caller mutation"
	if s.config.WorkspaceRoots[0] != extra {
		t.Fatal("live root scope aliases caller")
	}
}
