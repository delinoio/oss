package opencode

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
)

// The Worker must register Token against its durable execution before opening
// this private runtime. Format validation here cannot establish registration or
// selected-account readiness. Root comes from independent Worker Git /
// General Chat workspace inspection, never from a native message's own path.
type apiSessionConfig struct {
	Probe        ProbeConfig                               `json:"-"`
	Workspace    string                                    `json:"-"`
	Root         WorkspaceRoot                             `json:"-"`
	NativeRoot   string                                    `json:"-"`
	References   []WorkspaceReference                      `json:"-"`
	ServerOrigin string                                    `json:"-"`
	Token        string                                    `json:"-"`
	Settings     SessionSettings                           `json:"-"`
	Instructions string                                    `json:"-"`
	ContextLimit int64                                     `json:"-"`
	OutputLimit  int64                                     `json:"-"`
	Rejection    RejectionPolicy                           `json:"-"`
	Claim        func(context.Context, SessionClaim) error `json:"-"`
}

func canonicalDirectory(path string) bool {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || domain.Text(path, "native directory", 32768, true) != nil || strings.ContainsAny(path, "\r\n") {
		return false
	}
	actual, err := filepath.EvalSymlinks(path)
	if err != nil || actual != path {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func directoryContains(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func prepareAPISession(config apiSessionConfig) ([]string, *nativeAPIProfile, error) {
	if config.Probe.Version != SupportedVersion {
		return nil, nil, incompatible()
	}
	scope, err := config.workspaceRoot()
	if err != nil {
		return nil, nil, err
	}
	if config.Probe.Process.OwnerID.Validate() != nil || !filepath.IsAbs(config.Probe.Process.Executable) || config.Claim == nil || !apiproxy.ValidToken(config.Token) {
		return nil, nil, sessionInvalid()
	}
	// Only the two native primary profiles have effective-policy evidence.
	// This does not grant agent switching, subagent execution or Plan approval.
	if config.Settings.Agent != BuildAgent && config.Settings.Agent != PlanAgent {
		return nil, nil, incompatible()
	}
	root := filepath.Dir(config.Probe.Home)
	// The new checkpoint profile also requires a local drive-qualified native
	// runtime. Refuse UNC/device/relative contexts before Build can launch;
	// waiting for Plan rules or checkpoint retention would reject too late.
	if scope.windowsGlobal() && (!filepath.IsAbs(root) || filesystemBoundary(root) == "") {
		return nil, nil, sessionInvalid()
	}
	if directoryContains(root, config.Workspace) || directoryContains(config.Workspace, root) || !validWorkspaceReferences(config.References, config.Workspace, root) || len(config.References) > 0 && (scope.kind != gitWorkspaceRoot || scope.native != config.Workspace) {
		return nil, nil, sessionInvalid()
	}
	projectConfig := &projectConfigScope{Directory: config.Workspace, Root: scope.boundary}
	if err := projectConfig.inspect(); err != nil {
		if logger := config.Probe.Process.Logger; logger != nil {
			logger.Warn("opencode_project_config_source_refused", "owner_id", config.Probe.Process.OwnerID, "code", domain.Unsupported)
		}
		return nil, nil, err
	}
	if err := rpc.ValidateEndpoint(config.ServerOrigin); err != nil {
		return nil, nil, err
	}
	origin, err := url.Parse(config.ServerOrigin)
	if err != nil || origin.ForceQuery || origin.RawPath != "" {
		return nil, nil, sessionInvalid()
	}
	origin.Path = apiproxy.Prefix
	settings := config.Settings
	settings.Permission = slices.Clone(settings.Permission)
	profile := &nativeAPIProfile{Settings: settings, BaseURL: origin.String(), Token: config.Token, ContextLimit: config.ContextLimit, OutputLimit: config.OutputLimit, Rejection: config.Rejection, Instructions: config.Instructions}
	profile.ProjectConfig = projectConfig
	profile.WorkspaceRoot = &scope
	profile.References = slices.Clone(config.References)
	if err := profile.inspectReferences(); err != nil {
		return nil, nil, err
	}
	if config.Instructions != "" {
		profile.InstructionsPath = filepath.Join(root, "instructions.txt")
	}
	project, err := collectProjectInstructions(config.Workspace, scope.instructionRoot())
	if err != nil {
		return nil, nil, err
	}
	profile.ProjectInstructions = project
	raw, err := profile.configBytes()
	if err != nil {
		return nil, nil, err
	}
	env, err := probeEnvironment(config.Probe)
	if err != nil {
		return nil, nil, err
	}
	for i, entry := range env {
		if strings.HasPrefix(entry, "OPENCODE_CONFIG_CONTENT=") {
			env[i] = "OPENCODE_CONFIG_CONTENT=" + string(raw)
		}
	}
	for _, key := range []string{"OPENCODE_DISABLE_DEFAULT_PLUGINS", "OPENCODE_DISABLE_CLAUDE_CODE", "OPENCODE_DISABLE_EXTERNAL_SKILLS", "OPENCODE_DISABLE_LSP_DOWNLOAD", "OPENCODE_EXPERIMENTAL_DISABLE_FILEWATCHER", "OPENCODE_PURE"} {
		env = append(env, key+"=true")
	}
	// Pinned instance initialization attempts plugin dependency installation
	// even in pure mode. A fresh offline cache prevents automatic downloads;
	// remove this workaround only after a verified native no-install profile.
	env = append(env, "npm_config_offline=true", "npm_config_cache="+filepath.Join(root, "cache", "npm"))
	if runtime.GOOS == "windows" {
		programData, err := nativeProgramData()
		if err != nil {
			return nil, nil, err
		}
		env = append(env, "ProgramData="+programData)
	}
	if err := profile.writeInstructions(); err != nil {
		return nil, nil, err
	}
	if err := profile.writeReferences(root, config.Workspace); err != nil {
		return nil, nil, err
	}
	return env, profile, nil
}

func nativeProgramData() (string, error) {
	value := os.Getenv("ProgramData")
	if value == "" {
		value = `C:\ProgramData`
	}
	if !filepath.IsAbs(value) || filepath.Clean(value) != value || strings.ContainsAny(value, "\r\n\x00") {
		return "", sessionInvalid()
	}
	return value, nil
}

func managedConfigPaths() ([]string, error) {
	switch runtime.GOOS {
	case "darwin":
		current, err := user.Current()
		if err != nil || current.Username == "" || strings.ContainsAny(current.Username, "/\\\x00") {
			return nil, incompatible()
		}
		return []string{"/Library/Application Support/opencode", "/Library/Managed Preferences/ai.opencode.managed.plist", filepath.Join("/Library/Managed Preferences", current.Username, "ai.opencode.managed.plist")}, nil
	case "windows":
		root, err := nativeProgramData()
		if err != nil {
			return nil, err
		}
		return []string{filepath.Join(root, "opencode")}, nil
	case "linux":
		return []string{"/etc/opencode"}, nil
	default:
		return nil, incompatible()
	}
}

// Do not read or override administrator policy. This initial isolated profile
// refuses any present content or uninspectable source before native loading.
func inspectManagedConfig(paths []string) error {
	for _, path := range paths {
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return incompatible()
		}
		file, err := os.Open(path)
		if err != nil {
			return incompatible()
		}
		entries, readErr := file.ReadDir(1)
		closeErr := file.Close()
		if len(entries) != 0 || readErr != nil && readErr != io.EOF || closeErr != nil {
			return incompatible()
		}
	}
	return nil
}

func (s *sessionAPI) verifyNativeContext(ctx context.Context, config apiSessionConfig) error {
	if err := s.enter(ctx); err != nil {
		return err
	}
	defer s.leave()
	if !s.apiVerified || s.creation != nil || s.problem != nil {
		return sessionInvalid()
	}
	s.runtimeRead = true
	defer func() { s.runtimeRead = false }()
	scope, err := config.workspaceRoot()
	if err != nil || s.apiProfile.WorkspaceRoot == nil || *s.apiProfile.WorkspaceRoot != scope {
		s.apiVerified, s.problem = false, sessionProblem()
		return s.problem
	}
	root := filepath.Dir(config.Probe.Home)
	paths := map[string]any{"home": root, "state": filepath.Join(root, "state", "opencode"), "config": filepath.Join(root, "config", "opencode"), "worktree": scope.native, "directory": config.Workspace}
	for _, step := range []struct {
		path     string
		validate func([]byte) error
	}{
		{"/path", func(raw []byte) error { return exactPrivateJSON(raw, paths) }},
		{"/agent", func(raw []byte) error {
			return validatePrimaryAgent(raw, config.Settings.Agent, root, scope.native, s.apiProfile.References...)
		}},
	} {
		raw, status, err := s.request(ctx, http.MethodGet, step.path, nil, http.StatusOK)
		if err == nil {
			err = step.validate(raw)
		}
		if err != nil {
			s.apiVerified = false
			s.problem = sessionProblem()
			if s.logger != nil {
				if step.path == "/agent" && len(s.apiProfile.References) > 0 {
					rules, references := observedReferencePolicyCounts(raw, config.Settings.Agent, s.apiProfile.References)
					s.logger.WarnContext(ctx, "opencode_reference_policy_mismatch", "owner_id", s.owner, "expected_references", len(s.apiProfile.References), "observed_references", references, "observed_rules", rules)
				}
				s.logger.WarnContext(ctx, "OpenCode effective native context verification failed", "owner_id", s.owner, "phase", step.path, "http_status", status, "code", domain.SafeError(err).Code)
			}
			return err
		}
	}
	s.runtimeRoot = scope.native
	if s.logger != nil {
		s.logger.InfoContext(ctx, "opencode_workspace_root_verified", "owner_id", s.owner, "root_kind", scope.kind, "windows_global", scope.windowsGlobal())
	}
	if len(s.apiProfile.References) > 0 && s.logger != nil {
		s.logger.InfoContext(ctx, "opencode_workspace_references_verified", "owner_id", s.owner, "additional_repositories", len(s.apiProfile.References))
	}
	return nil
}

func validatePrimaryAgent(raw []byte, selected PrimaryAgent, root, worktree string, references ...WorkspaceReference) error {
	expected, err := expectedPrimaryAgent(selected, root, worktree, references...)
	if err != nil {
		return err
	}
	var agents []json.RawMessage
	if domain.Decode(raw, &agents) != nil || len(agents) != 7 {
		return sessionProblem()
	}
	seen := map[string]bool{}
	for _, raw := range agents {
		fields, err := shape(raw, []string{"name", "mode", "native", "permission", "options"}, []string{"description", "hidden", "prompt", "temperature"})
		if err != nil {
			return err
		}
		name, valid := boundedString(fields["name"], 128, true)
		if !valid || seen[name] || !slices.Contains([]string{"build", "plan", "general", "explore", "compaction", "title", "summary"}, name) || string(fields["native"]) != "true" {
			return sessionProblem()
		}
		seen[name] = true
		if name != string(selected) {
			continue // Their policies do not grant a launch or child capability.
		}
		if err := exactPrivateJSON(raw, expected); err != nil {
			return err
		}
	}
	return nil
}

func expectedPrimaryAgent(agent PrimaryAgent, root, worktree string, references ...WorkspaceReference) (map[string]any, error) {
	glob := filepath.Join(root, "data", "opencode", "tool-output", "*")
	rules := []PermissionRule{
		{"*", "*", PermissionAllow}, {"doom_loop", "*", PermissionAsk},
		{"external_directory", "*", PermissionAsk}, {"external_directory", glob, PermissionAllow},
		{"external_directory", filepath.Join(root, "tmp", "opencode", "*"), PermissionAllow},
	}
	for _, reference := range references {
		rules = append(rules, PermissionRule{"external_directory", filepath.Join(reference.Path, "*"), PermissionAllow})
	}
	rules = append(rules, []PermissionRule{
		{"question", "*", PermissionDeny}, {"plan_enter", "*", PermissionDeny}, {"plan_exit", "*", PermissionDeny},
		{"read", "*", PermissionAllow}, {"read", "*.env", PermissionAsk}, {"read", "*.env.*", PermissionAsk}, {"read", "*.env.example", PermissionAllow},
	}...)
	var description string
	switch agent {
	case BuildAgent:
		description = "The default agent. Executes tools based on configured permissions."
		rules = append(rules, PermissionRule{"question", "*", PermissionAllow}, PermissionRule{"plan_enter", "*", PermissionAllow})
	case PlanAgent:
		description = "Plan mode. Disallows all edit tools."
		plans := filepath.Join(root, "data", "opencode", "plans")
		relative, err := nativePlanRelativePath(worktree, root, filepath.Join(plans, "*.md"))
		if err != nil {
			return nil, incompatible()
		}
		// Preserve native rules and order verbatim; do not interpret globs,
		// synthesize a read-only sandbox or remove native plan-file exceptions.
		rules = append(rules,
			PermissionRule{"question", "*", PermissionAllow}, PermissionRule{"plan_exit", "*", PermissionAllow},
			PermissionRule{"task", "general", PermissionDeny}, PermissionRule{"external_directory", filepath.Join(plans, "*"), PermissionAllow},
			PermissionRule{"edit", "*", PermissionDeny}, PermissionRule{"edit", filepath.Join(".opencode", "plans", "*.md"), PermissionAllow},
			PermissionRule{"edit", relative, PermissionAllow},
		)
	default:
		return nil, incompatible()
	}
	rules = append(rules, PermissionRule{"external_directory", glob, PermissionAllow})
	return map[string]any{"name": agent, "description": description, "mode": "primary", "native": true, "options": map[string]any{}, "permission": rules}, nil
}

// Node/Bun resolves Windows "/" on the original process cwd's drive. The
// initializer enforces runtimeHome == process.Cwd and strips per-drive cwd
// environment entries. Workspace/server/renderer cwd cannot supply this fact.
func nativePlanRelativePath(worktree, processCwd, target string) (string, error) {
	if runtime.GOOS == "windows" && worktree == "/" {
		worktree = filesystemBoundary(processCwd)
		if worktree == "" {
			return "", incompatible()
		}
		// Native path.relative returns an absolute target across drives. Keep
		// this handling confined to the new global profile; existing Git
		// initialization and version-1 checkpoint validation stay unchanged.
		if !strings.EqualFold(filepath.VolumeName(worktree), filepath.VolumeName(target)) {
			return target, nil
		}
	}
	return filepath.Rel(worktree, target)
}
