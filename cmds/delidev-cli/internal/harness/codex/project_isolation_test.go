// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func readIsolationDocument(t *testing.T, home string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	document := map[string]any{}
	if err := toml.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	return document
}

func isolationProjectLevel(t *testing.T, home, key string) any {
	t.Helper()
	projects, _ := readIsolationDocument(t, home)["projects"].(map[string]any)
	entry, _ := projects[key].(map[string]any)
	return entry["trust_level"]
}

func TestIsolationPolicyTableByProfileKind(t *testing.T) {
	rows := []struct {
		profile isolationKind
		seed    bool
		verify  bool
	}{
		{isolationAPI, true, true},
		{isolationManaged, true, true},
		{isolationSidechat, false, false},
		{isolationTitle, false, false},
		{isolationReview, false, false},
		{isolationOther, false, false},
	}
	for _, row := range rows {
		got := policyFor(row.profile)
		if got.SeedTrust != row.seed || got.VerifyInventory != row.verify {
			t.Errorf("%s: policy %+v, want seed=%v verify=%v", row.profile, got, row.seed, row.verify)
		}
	}
}

func TestIsolationKindDerivedFromClientConfiguration(t *testing.T) {
	rows := []struct {
		name   string
		client *Client
		want   isolationKind
	}{
		{"api", &Client{api: &apiBinding{}}, isolationAPI},
		{"api-title", &Client{api: &apiBinding{title: true}}, isolationTitle},
		{"managed", &Client{managedHome: "/home"}, isolationManaged},
		{"shared-member", &Client{managedHome: "/home", sharedEvents: &sharedEvents{}}, isolationManaged},
		{"managed-title", &Client{managedHome: "/home", managedTitle: true}, isolationTitle},
		{"sidechat-api", &Client{api: &apiBinding{}, sidechat: ReadOnlySidechatV1}, isolationSidechat},
		{"sidechat-managed", &Client{managedHome: "/home", sidechat: ReadOnlySidechatV1}, isolationSidechat},
		{"review", &Client{api: &apiBinding{}, sidechat: ReadOnlySidechatV1, codeReviewModel: "model"}, isolationReview},
		{"other", &Client{}, isolationOther},
	}
	for _, row := range rows {
		if got := row.client.isolationProfile(); got != row.want {
			t.Errorf("%s: profile %s, want %s", row.name, got, row.want)
		}
	}
}

func TestIsolationPinsOnlyDefaultsThatUntrustedProjectsChange(t *testing.T) {
	approvals := []ApprovalPolicy{"", ApprovalNever, ApprovalOnRequest, ApprovalUntrusted}
	sandboxes := []string{"", "read-only", "workspace-write", "danger-full-access"}
	for _, approval := range approvals {
		for _, sandbox := range sandboxes {
			p := threadParams{ApprovalPolicy: approval, Sandbox: sandbox}
			pinIsolatedDefaults(&p)
			wantApproval, wantSandbox := approval, sandbox
			if approval == "" {
				wantApproval = ApprovalOnRequest
			}
			if sandbox == "" {
				wantSandbox = "read-only"
			}
			if p.ApprovalPolicy != wantApproval || p.Sandbox != wantSandbox {
				t.Errorf("approval %q sandbox %q -> %q/%q, want %q/%q", approval, sandbox, p.ApprovalPolicy, p.Sandbox, wantApproval, wantSandbox)
			}
		}
	}
	// A named permission profile is a sandbox selection and is never replaced.
	p := threadParams{Permissions: "named"}
	pinIsolatedDefaults(&p)
	if p.Sandbox != "" || p.Permissions != "named" {
		t.Fatal("pinned defaults replaced a named permission profile")
	}
}

func TestProjectTrustKeysCoverDirectoryRepositoryAndLinkedWorktreeRoots(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(root, "plain")
	repo := filepath.Join(root, "repo")
	sub := filepath.Join(repo, "pkg", "inner")
	linked := filepath.Join(root, "linked")
	for _, dir := range []string{plain, filepath.Join(repo, ".git", "worktrees", "linked"), sub, linked} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	gitdir := filepath.Join(repo, ".git", "worktrees", "linked")
	if err := os.WriteFile(filepath.Join(linked, ".git"), []byte("gitdir: "+gitdir+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := projectTrustKeys([]string{plain}); !slices.Contains(got, plain) || len(got) != 1 && !strings.HasPrefix(got[1], root) {
		t.Fatalf("plain directory keys %v", got)
	}
	if got := projectTrustKeys([]string{sub}); !slices.Equal(got, []string{sub, repo}) {
		t.Fatalf("subdirectory keys %v", got)
	}
	if got := projectTrustKeys([]string{linked}); !slices.Equal(got, []string{linked, repo}) {
		t.Fatalf("linked worktree keys %v", got)
	}
	if got := projectTrustKeys([]string{sub, linked, sub}); !slices.Equal(got, []string{sub, repo, linked}) {
		t.Fatalf("deduplicated workspace root keys %v", got)
	}
}

func TestIsolateHomeConfigPreservesContentAndReplacesTrustAtomically(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codex")
	if err := security.PrivateDir(home); err != nil {
		t.Fatal(err)
	}
	original := "# retained comment\nmodel = \"fixture-model\"\n[projects.\"/repo\"]\ntrust_level = \"trusted\"\nextra = 3\n[projects.\"/other\"]\ntrust_level = \"trusted\"\n[mcp_servers.foreign]\ncommand = \"/usr/bin/true\"\n"
	path := filepath.Join(home, "config.toml")
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := isolateHomeConfig(home, []string{"/repo", "/new"}); err != nil {
		t.Fatal(err)
	}
	document := readIsolationDocument(t, home)
	if document["model"] != "fixture-model" {
		t.Fatal("unrelated setting lost")
	}
	if _, present := document["mcp_servers"]; present {
		t.Fatal("private home retained a foreign MCP table")
	}
	if isolationProjectLevel(t, home, "/repo") != "untrusted" || isolationProjectLevel(t, home, "/new") != "untrusted" || isolationProjectLevel(t, home, "/other") != "trusted" {
		t.Fatal("trust levels not isolated exactly for the requested keys")
	}
	if entry := document["projects"].(map[string]any)["/repo"].(map[string]any); entry["extra"] != int64(3) {
		t.Fatal("unrelated project field lost")
	}
	info, err := os.Stat(path)
	if err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("config permissions %v %v", info, err)
	}
	entries, _ := os.ReadDir(home)
	if len(entries) != 1 {
		t.Fatalf("interrupted-write temporary left behind: %d entries", len(entries))
	}
	before, _ := os.ReadFile(path)
	beforeInfo, _ := os.Stat(path)
	if err := isolateHomeConfig(home, []string{"/repo", "/new"}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	afterInfo, _ := os.Stat(path)
	if !bytes.Equal(before, after) || !afterInfo.ModTime().Equal(beforeInfo.ModTime()) || !os.SameFile(beforeInfo, afterInfo) {
		t.Fatal("an already isolated configuration was rewritten")
	}
}

func TestIsolateHomeConfigCreatesMissingFileAndRejectsInvalidConfiguration(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codex")
	if err := security.PrivateDir(home); err != nil {
		t.Fatal(err)
	}
	if err := isolateHomeConfig(home, []string{"/repo"}); err != nil || isolationProjectLevel(t, home, "/repo") != "untrusted" {
		t.Fatal("missing configuration was not created", err)
	}
	for name, content := range map[string]string{"syntax": "projects = [", "projects-scalar": "projects = 3\n", "entry-scalar": "[projects]\n\"/repo\" = 3\n"} {
		t.Run(name, func(t *testing.T) {
			bad := filepath.Join(t.TempDir(), "codex")
			if err := security.PrivateDir(bad); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(bad, "config.toml")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if isolateHomeConfig(bad, []string{"/repo"}) == nil {
				t.Fatal("invalid native configuration was accepted")
			}
			if raw, _ := os.ReadFile(path); string(raw) != content {
				t.Fatal("invalid native configuration was overwritten")
			}
		})
	}
	if isolateHomeConfig("relative", []string{"/repo"}) == nil || isolateHomeConfig(home, nil) == nil {
		t.Fatal("invalid request accepted")
	}
}

func TestIsolateHomeConfigSerializesConcurrentSharedMembers(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codex")
	if err := security.PrivateDir(home); err != nil {
		t.Fatal(err)
	}
	const members = 16
	var wg sync.WaitGroup
	for i := 0; i < members; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := isolateHomeConfig(home, []string{fmt.Sprintf("/repo-%d", i), "/shared"}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	for i := 0; i < members; i++ {
		if isolationProjectLevel(t, home, fmt.Sprintf("/repo-%d", i)) != "untrusted" {
			t.Fatalf("concurrent member %d lost its isolation", i)
		}
	}
	entries, _ := os.ReadDir(home)
	if len(entries) != 1 {
		t.Fatal("concurrent writers left temporary files")
	}
}

type isolationRun struct {
	err     error
	capture []map[string]any
	home    string
	logs    string
	client  *Client
	params  map[string]any
}

func runManagedIsolation(t *testing.T, mode string, resume bool, mutate func(*ThreadSettings)) isolationRun {
	t.Helper()
	settings := threadSettings(t)
	settings.Provider = "openai"
	if mutate != nil {
		mutate(&settings)
	}
	config := fixtureConfig(t, mode)
	config.Mode, config.ManagedAuthentication = ThreadProtocol, true
	var logs bytes.Buffer
	config.Process.Logger = slog.New(slog.NewTextHandler(&logs, nil))
	capture := filepath.Join(config.Process.Cwd, "thread-operations.jsonl")
	config.Process.Env = append(config.Process.Env, "DELIDEV_CODEX_CAPTURE="+capture)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	client, err := Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if resume {
		_, err = client.ResumeThread(ctx, domain.NewID(), domain.NewID(), settings)
	} else {
		_, err = client.StartThread(ctx, domain.NewID(), settings)
	}
	run := isolationRun{err: err, capture: capturedThreads(t, capture), home: config.Home, logs: logs.String(), client: client}
	for _, row := range run.capture {
		if row["method"] == "thread/start" || row["method"] == "thread/resume" {
			run.params, _ = row["params"].(map[string]any)
		}
	}
	return run
}

func TestManagedThreadIsolationRowsPinDefaultsAndRecordUntrustedProject(t *testing.T) {
	rows := []struct {
		name      string
		resume    bool
		mutate    func(*ThreadSettings)
		sandbox   any
		approval  any
		effective SandboxType
	}{
		{"start-explicit-workspace-write", false, nil, "workspace-write", "on-request", WorkspaceWrite},
		{"resume-explicit-workspace-write", true, nil, "workspace-write", "on-request", WorkspaceWrite},
		{"start-explicit-full-access-never", false, func(s *ThreadSettings) {
			s.Options.Permission, s.Options.ApprovalPolicy = domain.PermissionFullAccess, "never"
		}, "danger-full-access", "never", FullAccess},
		{"start-explicit-read-only", false, func(s *ThreadSettings) { s.Options.Permission = domain.PermissionReadOnly }, "read-only", "on-request", ReadOnly},
		{"start-default-permission-empty-approval", false, func(s *ThreadSettings) {
			s.Options.Permission, s.Options.ApprovalPolicy = domain.PermissionDefault, ""
		}, "read-only", "on-request", ReadOnly},
		{"resume-default-permission-empty-approval", true, func(s *ThreadSettings) {
			s.Options.Permission, s.Options.ApprovalPolicy = domain.PermissionDefault, ""
		}, "read-only", "on-request", ReadOnly},
		{"start-workspace-write-empty-approval", false, func(s *ThreadSettings) { s.Options.ApprovalPolicy = "" }, "workspace-write", "on-request", WorkspaceWrite},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			var cwd string
			run := runManagedIsolation(t, "thread-managed-mcp-empty", row.resume, func(s *ThreadSettings) {
				if row.mutate != nil {
					row.mutate(s)
				}
				cwd = s.Cwd
			})
			if run.err != nil {
				t.Fatal(run.err)
			}
			if run.params["sandbox"] != row.sandbox || run.params["approvalPolicy"] != row.approval {
				t.Fatalf("thread request sandbox=%v approval=%v, want %v/%v", run.params["sandbox"], run.params["approvalPolicy"], row.sandbox, row.approval)
			}
			if isolationProjectLevel(t, run.home, cwd) != "untrusted" {
				t.Fatal("project was not recorded untrusted before the thread")
			}
			methods := []string{}
			for _, entry := range run.capture {
				methods = append(methods, entry["method"].(string))
			}
			operation := "thread/start"
			if row.resume {
				operation = "thread/resume"
			}
			if !slices.Equal(methods, []string{operation, "config/read"}) {
				t.Fatalf("native order %v", methods)
			}
			if run.capture[1]["params"].(map[string]any)["cwd"] != cwd {
				t.Fatal("effective configuration was not read for the thread directory")
			}
		})
	}
}

func TestManagedThreadIsolationAcceptsAbsentNullAndEmptyInventory(t *testing.T) {
	for _, mode := range []string{"thread-managed-ready", "thread-managed-mcp-null", "thread-managed-mcp-empty"} {
		t.Run(mode, func(t *testing.T) {
			if run := runManagedIsolation(t, mode, false, nil); run.err != nil {
				t.Fatal(run.err)
			}
		})
	}
}

func TestManagedThreadIsolationRejectsUnselectedInventoryBeforeAnyInput(t *testing.T) {
	rows := []struct {
		mode   string
		reason isolationReason
	}{
		{"thread-managed-mcp-nonempty", isolationInventoryNonEmpty},
		{"thread-managed-mcp-malformed", isolationInventoryUnreadable},
		{"thread-managed-mcp-error", isolationInventoryUnreadable},
	}
	for _, row := range rows {
		for _, resume := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/resume=%v", row.mode, resume), func(t *testing.T) {
				run := runManagedIsolation(t, row.mode, resume, nil)
				problem := domain.SafeError(run.err)
				diagnostic := domain.CodexErrorDiagnostic(run.err)
				if run.err == nil || problem.Code != domain.Unsupported || diagnostic == nil || diagnostic.Phase != domain.CodexProfile {
					t.Fatalf("inventory was accepted or misclassified: %v", run.err)
				}
				if strings.Contains(problem.Message+problem.Guidance, "inherited") || strings.Contains(run.logs, "inherited") || strings.Contains(run.logs, "fixture-protected") {
					t.Fatal("diagnostic exposed native configuration content")
				}
				if !strings.Contains(run.logs, "codex_native_isolation_rejected") || !strings.Contains(run.logs, "stage=mcp-inventory-verification") || !strings.Contains(run.logs, "reason="+string(row.reason)) {
					t.Fatalf("structured warning missing: %s", run.logs)
				}
				for _, entry := range run.capture {
					if entry["method"] == "turn/start" {
						t.Fatal("input reached the native process")
					}
				}
				// The latched connection refuses input and any second thread.
				if _, err := run.client.StartTurn(context.Background(), domain.NewID(), domain.NewID(), domain.SessionInput{Mode: domain.ExecuteMode, Prompt: "must not send"}); err == nil {
					t.Fatal("a rejected isolation accepted input")
				}
				if _, err := run.client.StartThread(context.Background(), domain.NewID(), threadSettings(t)); err == nil {
					t.Fatal("a rejected isolation allowed another thread")
				}
				if err := run.client.Close(); err != nil {
					t.Fatal("owned native cleanup was not confirmed", err)
				}
			})
		}
	}
}

func TestManagedThreadIsolationRejectsUnwritableConfigurationBeforeThreadStart(t *testing.T) {
	config := fixtureConfig(t, "thread-managed-mcp-empty")
	config.Mode, config.ManagedAuthentication = ThreadProtocol, true
	var logs bytes.Buffer
	config.Process.Logger = slog.New(slog.NewTextHandler(&logs, nil))
	capture := filepath.Join(config.Process.Cwd, "thread-operations.jsonl")
	config.Process.Env = append(config.Process.Env, "DELIDEV_CODEX_CAPTURE="+capture)
	if err := os.WriteFile(filepath.Join(config.Home, "config.toml"), []byte("projects = ["), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client, err := Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	settings := threadSettings(t)
	settings.Provider = "openai"
	_, err = client.StartThread(ctx, domain.NewID(), settings)
	if diagnostic := domain.CodexErrorDiagnostic(err); diagnostic == nil || diagnostic.Code != domain.Unsupported || diagnostic.Phase != domain.CodexProfile {
		t.Fatalf("unwritable isolation was accepted or misclassified: %v", err)
	}
	if len(capturedThreads(t, capture)) != 0 {
		t.Fatal("a thread was created without project isolation")
	}
	if !strings.Contains(logs.String(), "stage=project-trust-isolation") || !strings.Contains(logs.String(), "reason=trust-unwritable") {
		t.Fatalf("structured warning missing: %s", logs.String())
	}
}

func TestSidechatThreadDoesNotSeedProjectTrust(t *testing.T) {
	client, _ := openThreadFixture(t, "thread-continuation-sidechat")
	settings := threadSettings(t)
	settings.Options = domain.AgentOptions{Permission: domain.PermissionReadOnly, ApprovalPolicy: string(ApprovalNever)}
	if _, err := client.StartThread(context.Background(), domain.NewID(), settings); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(client.home, "config.toml")); err == nil {
		raw, _ := os.ReadFile(filepath.Join(client.home, "config.toml"))
		if strings.Contains(string(raw), "projects") {
			t.Fatal("Sidechat recorded project trust")
		}
	}
}
