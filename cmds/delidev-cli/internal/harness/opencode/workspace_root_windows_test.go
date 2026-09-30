package opencode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func TestWindowsGlobalPlanUsesOriginalProcessDriveAndNativeRuleOrder(t *testing.T) {
	// These operands deliberately differ from the test/server cwd and workspace
	// drive. They pin native Node/Bun semantics without accessing either drive.
	for _, test := range []struct{ worktree, cwd, target, expected string }{
		{`/`, `C:\owned\runtime`, `C:\owned\runtime\data\opencode\plans\*.md`, `owned\runtime\data\opencode\plans\*.md`},
		{`/`, `D:\worker\runtime`, `D:\worker\runtime\data\opencode\plans\*.md`, `worker\runtime\data\opencode\plans\*.md`},
		{`/`, `C:\owned\runtime`, `D:\owned\plans\*.md`, `D:\owned\plans\*.md`},
	} {
		got, err := nativePlanRelativePath(test.worktree, test.cwd, test.target)
		if err != nil || got != test.expected {
			t.Fatal("Plan borrowed another cwd or altered native path semantics", got, err)
		}
	}
	selected, err := expectedPrimaryAgent(PlanAgent, `C:\owned\runtime`, "/")
	if err != nil {
		t.Fatal(err)
	}
	rules := selected["permission"].([]PermissionRule)
	end := rules[len(rules)-5:]
	want := []PermissionRule{
		{"external_directory", `C:\owned\runtime\data\opencode\plans\*`, PermissionAllow},
		{"edit", "*", PermissionDeny},
		{"edit", `.opencode\plans\*.md`, PermissionAllow},
		{"edit", `owned\runtime\data\opencode\plans\*.md`, PermissionAllow},
		{"external_directory", `C:\owned\runtime\data\opencode\tool-output\*`, PermissionAllow},
	}
	for i := range want {
		if end[i] != want[i] {
			t.Fatal("native Plan exception or rule order changed", i)
		}
	}
}

func TestWindowsGlobalRequiresOriginalGlobalProjectIdentity(t *testing.T) {
	c := globalRootConfig(t)
	_, profile, err := prepareAPISession(c)
	if err != nil {
		t.Fatal(err)
	}
	s := &sessionAPI{apiProfile: profile}
	if !s.validRootProject(sessionIdentity{project: "global"}) || s.validRootProject(sessionIdentity{project: "foreign"}) {
		t.Fatal("Windows global metadata acquired another native project identity")
	}
	for _, directory := range []string{"/", `C:relative`, `\\server\share\workspace`, `\\?\C:\workspace`} {
		if root, err := GlobalWorkspaceRoot(directory); err == nil || root != (WorkspaceRoot{}) {
			t.Fatal("an alias or unsupported filesystem root gained global authority")
		}
	}
}

func TestWindowsGlobalRefusesUnsupportedRuntimeContextBeforeInitialization(t *testing.T) {
	c := globalRootConfig(t)
	for _, root := range []string{`C:relative`, `\\server\share\runtime`, `\\?\C:\runtime`} {
		changed := c
		changed.Probe.Home = filepath.Join(root, "opencode")
		changed.Probe.Process.Cwd = root
		// In particular, no UNC read or write is needed to refuse this context.
		if _, _, err := prepareAPISession(changed); err == nil {
			t.Fatal("unsupported runtime reached native initialization")
		}
	}
}

func TestManualNativeWindowsOpenCodeDifferentDrivePlan(t *testing.T) {
	if os.Getenv("DELIDEV_NATIVE_OPENCODE_EXECUTABLE") == "" || os.Getenv("DELIDEV_NATIVE_OPENCODE_WINDOWS_WORKSPACE_PARENT") == "" {
		t.Skip("opt-in installed pinned binary and writable workspace parent on another local drive")
	}
	nativeOwnedAPISessionWithProfile(t, true, false, nativeServerRelay, func(c *apiSessionConfig) {
		parent := os.Getenv("DELIDEV_NATIVE_OPENCODE_WINDOWS_WORKSPACE_PARENT")
		if !canonicalDirectory(parent) || filesystemBoundary(parent) == "" || filesystemBoundary(parent) == filesystemBoundary(c.Probe.Process.Cwd) {
			t.Fatal("cross-drive fixture requires an independently selected canonical local parent")
		}
		directory, err := os.MkdirTemp(parent, "delidev-issue-1205-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(directory) })
		if err := security.PrivateDir(directory); err != nil {
			t.Fatal(err)
		}
		directory, err = filepath.EvalSymlinks(directory)
		if err != nil {
			t.Fatal(err)
		}
		c.Workspace, c.NativeRoot = directory, ""
		c.Root, err = GlobalWorkspaceRoot(directory)
		if err != nil {
			t.Fatal(err)
		}
		c.Settings.Agent, c.Settings.Permission = PlanAgent, []PermissionRule{}
	})
}

func TestWindowsGlobalCheckpointBindsBothRootsAndRefusesLegacyPromotion(t *testing.T) {
	api, home := completedCheckpointFixture(t)
	s := api.session
	root, err := GlobalWorkspaceRoot(s.cwd)
	if err != nil {
		t.Fatal(err)
	}
	s.runtimeRoot = root.native
	s.apiProfile.WorkspaceRoot = &root
	s.apiProfile.ProjectInstructions, err = collectProjectInstructions(s.cwd, root.instructionRoot())
	if err != nil {
		t.Fatal(err)
	}
	raw, ref, err := api.RetainCheckpoint(context.Background())
	if err != nil || InspectReplacementRoot(context.Background(), home, raw, ref, s.cwd, root) != nil {
		t.Fatal("original Windows global checkpoint did not round trip", err)
	}
	value, err := decodeCheckpoint(raw, ref, home)
	if err != nil || value.Version != 2 || value.NativeRoot != "/" || value.FilesystemRoot != root.boundary || checkpointReplacementProfile(value) != nil {
		t.Fatal("native and filesystem checkpoint roots were collapsed", err)
	}
	if InspectReplacementWorkspace(context.Background(), home, raw, ref, s.cwd, "/") == nil {
		t.Fatal("legacy inspector inferred Windows filesystem authority")
	}
	for _, scenario := range []string{"native", "filesystem", "missing", "v1", "project", "foreign-workspace"} {
		changed := value
		switch scenario {
		case "native":
			changed.NativeRoot = root.boundary
		case "filesystem":
			changed.FilesystemRoot = filepath.Dir(s.cwd)
		case "missing":
			changed.FilesystemRoot = ""
		case "v1":
			changed.Version = 1
		case "project":
			changed.Project = "foreign"
		case "foreign-workspace":
			changed.Workspace = filepath.Dir(s.cwd)
		}
		bytes, _ := json.Marshal(changed)
		other := ref
		other.SHA256 = mutationDigest(bytes)
		if InspectReplacementRoot(context.Background(), home, bytes, other, s.cwd, root) == nil {
			t.Fatal("changed root or rewritten legacy profile gained continuation", scenario)
		}
	}
}
