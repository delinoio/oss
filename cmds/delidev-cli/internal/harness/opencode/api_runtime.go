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
// selected-account readiness. NativeRoot comes from independent Worker Git /
// General Chat workspace inspection, never from a native message's own path.
type apiSessionConfig struct {
	Probe        ProbeConfig                               `json:"-"`
	Workspace    string                                    `json:"-"`
	NativeRoot   string                                    `json:"-"`
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
	if config.Probe.Process.OwnerID.Validate() != nil || !filepath.IsAbs(config.Probe.Process.Executable) || config.Claim == nil || !apiproxy.ValidToken(config.Token) || !canonicalDirectory(config.Workspace) || !canonicalDirectory(config.NativeRoot) || !directoryContains(config.NativeRoot, config.Workspace) {
		return nil, nil, sessionInvalid()
	}
	// This first owned API initializer proves the native build agent. Other
	// agents need their own effective policy evidence before launch authority.
	if config.Settings.Agent != "build" {
		return nil, nil, incompatible()
	}
	root := filepath.Dir(config.Probe.Home)
	if directoryContains(root, config.Workspace) || directoryContains(config.Workspace, root) {
		return nil, nil, sessionInvalid()
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
	if config.Instructions != "" {
		profile.InstructionsPath = filepath.Join(root, "instructions.txt")
	}
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
	root := filepath.Dir(config.Probe.Home)
	paths := map[string]any{"home": root, "state": filepath.Join(root, "state", "opencode"), "config": filepath.Join(root, "config", "opencode"), "worktree": config.NativeRoot, "directory": config.Workspace}
	for _, step := range []struct {
		path     string
		validate func([]byte) error
	}{
		{"/path", func(raw []byte) error { return exactPrivateJSON(raw, paths) }},
		{"/agent", func(raw []byte) error { return validateBuildAgent(raw, root) }},
	} {
		raw, status, err := s.request(ctx, http.MethodGet, step.path, nil, http.StatusOK)
		if err == nil {
			err = step.validate(raw)
		}
		if err != nil {
			s.apiVerified = false
			s.problem = sessionProblem()
			if s.logger != nil {
				s.logger.WarnContext(ctx, "OpenCode effective native context verification failed", "owner_id", s.owner, "phase", step.path, "http_status", status, "code", domain.SafeError(err).Code)
			}
			return err
		}
	}
	s.runtimeRoot = config.NativeRoot
	return nil
}

func validateBuildAgent(raw []byte, root string) error {
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
		if name != "build" {
			continue // Their policies do not grant a launch or child capability.
		}
		expected := expectedBuildAgent(root)
		if err := exactPrivateJSON(raw, expected); err != nil {
			return err
		}
	}
	return nil
}

func expectedBuildAgent(root string) map[string]any {
	glob := filepath.Join(root, "data", "opencode", "tool-output", "*")
	rules := []PermissionRule{
		{"*", "*", PermissionAllow}, {"doom_loop", "*", PermissionAsk},
		{"external_directory", "*", PermissionAsk}, {"external_directory", glob, PermissionAllow},
		{"external_directory", filepath.Join(root, "tmp", "opencode", "*"), PermissionAllow},
		{"question", "*", PermissionDeny}, {"plan_enter", "*", PermissionDeny}, {"plan_exit", "*", PermissionDeny},
		{"read", "*", PermissionAllow}, {"read", "*.env", PermissionAsk}, {"read", "*.env.*", PermissionAsk}, {"read", "*.env.example", PermissionAllow},
		{"question", "*", PermissionAllow}, {"plan_enter", "*", PermissionAllow}, {"external_directory", glob, PermissionAllow},
	}
	return map[string]any{"name": "build", "description": "The default agent. Executes tools based on configured permissions.", "mode": "primary", "native": true, "options": map[string]any{}, "permission": rules}
}
