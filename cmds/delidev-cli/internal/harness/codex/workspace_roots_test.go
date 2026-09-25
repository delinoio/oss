package codex

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func multipleRootSettings(t *testing.T) ThreadSettings {
	t.Helper()
	settings := threadSettings(t)
	other, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	settings.WorkspaceRoots = []string{other, settings.Cwd}
	return settings
}

func TestMultipleWorkspaceRootsBindExactPrimaryAndAuthority(t *testing.T) {
	for _, mode := range []domain.PermissionMode{domain.PermissionDefault, domain.PermissionReadOnly, domain.PermissionWorkspaceWrite, domain.PermissionFullAccess} {
		t.Run(string(mode), func(t *testing.T) {
			client, capture := openThreadFixture(t, "thread-ready")
			settings := multipleRootSettings(t)
			settings.Options.Permission = mode
			result, err := client.StartThread(context.Background(), domain.NewID(), settings)
			if err != nil || result.Effective == nil || !slices.Equal(result.Effective.WorkspaceRoots, settings.WorkspaceRoots) || result.Effective.Cwd != settings.Cwd {
				t.Fatal("multiple roots were not bound", err)
			}
			requests := capturedThreads(t, capture)
			params := requests[0]["params"].(map[string]any)
			roots := params["runtimeWorkspaceRoots"].([]any)
			if len(roots) != 2 || roots[0] != settings.WorkspaceRoots[0] || roots[1] != settings.Cwd || params["cwd"] != settings.Cwd {
				t.Fatal("native request changed project order or primary")
			}
			retained := slices.Clone(client.execution.settings.WorkspaceRoots)
			result.Effective.WorkspaceRoots[0] = "/foreign"
			settings.WorkspaceRoots[0] = "/another"
			if !slices.Equal(client.execution.settings.WorkspaceRoots, retained) {
				t.Fatal("caller changed native authority through a returned slice")
			}
		})
	}
}

func TestMultipleWorkspaceRootsRejectIncompleteOrBroadenedNativeAuthority(t *testing.T) {
	for _, mode := range []string{"thread-runtime-root", "thread-missing-runtime-roots", "thread-duplicate-runtime-roots", "thread-root", "thread-missing-writable-root", "thread-duplicate-writable-root"} {
		t.Run(mode, func(t *testing.T) {
			client, _ := openThreadFixture(t, mode)
			result, err := client.StartThread(context.Background(), domain.NewID(), multipleRootSettings(t))
			if err == nil || result.Thread == nil || client.execution != nil || client.problem == nil {
				t.Fatal("inconsistent native roots acquired execution authority", err)
			}
		})
	}
}

func TestMultipleWorkspaceRootsValidateBeforeNativeSend(t *testing.T) {
	for _, scenario := range []string{"missing-primary", "duplicate", "missing-directory", "file", "relative", "symlink", "too-many", "control-character"} {
		t.Run(scenario, func(t *testing.T) {
			settings := multipleRootSettings(t)
			switch scenario {
			case "missing-primary":
				settings.WorkspaceRoots = settings.WorkspaceRoots[:1]
			case "duplicate":
				settings.WorkspaceRoots = append(settings.WorkspaceRoots, settings.WorkspaceRoots[0])
			case "missing-directory":
				settings.WorkspaceRoots[0] = filepath.Join(settings.WorkspaceRoots[0], "missing")
			case "file":
				path := filepath.Join(settings.WorkspaceRoots[0], "file")
				if err := os.WriteFile(path, nil, 0o600); err != nil {
					t.Fatal(err)
				}
				settings.WorkspaceRoots[0] = path
			case "relative":
				settings.WorkspaceRoots[0] = "relative"
			case "symlink":
				path := filepath.Join(settings.WorkspaceRoots[0], "link")
				if err := os.Symlink(settings.Cwd, path); err != nil {
					t.Skip("symlink unavailable", err)
				}
				settings.WorkspaceRoots[0] = path
			case "too-many":
				settings.WorkspaceRoots = make([]string, 101)
			case "control-character":
				settings.WorkspaceRoots[0] = "bad\x00root"
			}
			if err := ValidateThreadSettings(settings); err == nil {
				t.Fatal("invalid workspace roots accepted")
			}
		})
	}
}

func TestContinuationWorkspaceRootsCannotChange(t *testing.T) {
	a := EffectiveSettings{Cwd: "/primary", WorkspaceRoots: []string{"/secondary", "/primary"}}
	for _, roots := range [][]string{nil, {"/primary"}, {"/primary", "/secondary"}, {"/foreign", "/primary"}, {"/secondary", "/primary", "/third"}} {
		b := a
		b.WorkspaceRoots = roots
		if sameEffectiveSettings(a, b) {
			t.Fatal("continuation ignored changed retained workspace roots")
		}
	}
	legacy := EffectiveSettings{Cwd: "/primary"}
	explicit := legacy
	explicit.WorkspaceRoots = []string{"/primary"}
	if !sameEffectiveSettings(legacy, explicit) {
		t.Fatal("single-root legacy checkpoint lost compatible authority")
	}
}
