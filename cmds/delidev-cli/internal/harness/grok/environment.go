package grok

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// userAuthoredConfigurationNames lists the entries below the native home that only
// a user (never the native itself) authors. A persistent profile retains native
// caches and history but never consumes these configuration or login surfaces.
var userAuthoredConfigurationNames = []string{"auth.json", "config.toml", "config.json", "settings.json", "GROK.md", "AGENTS.md", "hooks", "plugins", "skills", "commands", "agents", "mcp.json"}

func probeEnvironment(config ProbeConfig) ([]string, error) {
	return runtimeEnvironment(config, false)
}

// subscriptionEnvironment is the environment of a managed-subscription
// execution. It is the discovery environment minus exactly the three loopback
// endpoint blocks: the native process must reach its own subscription endpoints
// with the cached login. Every other isolation variable and the
// GROK_{CLAUDE,CURSOR}_*_ENABLED=0 set remain.
func subscriptionEnvironment(config ProbeConfig) ([]string, error) {
	return runtimeEnvironment(config, true)
}

func runtimeEnvironment(config ProbeConfig, subscription bool) ([]string, error) {
	invalid := func() error {
		return domain.Fail(domain.InvalidArgument, "Grok Build requires a canonical DeliDev-owned private profile.", "Use the persistent profile resolved by DeliDev before native protocol validation.")
	}
	if !filepath.IsAbs(config.Home) || filepath.Base(config.Home) != "grok" {
		return nil, invalid()
	}
	root := filepath.Dir(config.Home)
	if config.Process.Cwd != root {
		return nil, invalid()
	}
	for _, name := range []string{"", "grok", "config", "cache", "data", "state", "tmp"} {
		path := filepath.Join(root, name)
		actual, err := filepath.EvalSymlinks(path)
		if err != nil || actual != path || security.CheckPrivateDir(path) != nil {
			return nil, invalid()
		}
	}
	// A persistent profile root holds only DeliDev-created sibling
	// directories. Reject every root file and unrecognized directory before native scanners
	// can discover project instructions, extensions or credentials there.
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, invalid()
	}
	for _, entry := range entries {
		switch entry.Name() {
		case "grok", "config", "cache", "data", "state", "tmp", "codex", "claude", "opencode":
			path := filepath.Join(root, entry.Name())
			if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || security.CheckPrivateDir(path) != nil {
				return nil, invalid()
			}
		default:
			return nil, invalid()
		}
	}
	for _, name := range userAuthoredConfigurationNames {
		if _, err := os.Lstat(filepath.Join(config.Home, name)); !errors.Is(err, os.ErrNotExist) {
			return nil, invalid()
		}
	}
	lookup := map[string]string{}
	for _, entry := range config.Process.Env {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			return nil, invalid()
		}
		key = strings.ToUpper(key)
		if key != "PATH" && key != "SYSTEMROOT" && key != "WINDIR" {
			continue
		}
		if _, exists := lookup[key]; exists || strings.ContainsRune(value, 0) {
			return nil, invalid()
		}
		lookup[key] = value
	}
	if !probePath(lookup["PATH"]) {
		return nil, invalid()
	}
	// These settings are discovery-only. No authentication or model operation
	// is sent. Loopback endpoints prevent incidental model-catalog refreshes
	// from using a hosted provider; no credential, proxy or loader is inherited.
	env := []string{
		"PATH=" + lookup["PATH"], "HOME=" + root, "USERPROFILE=" + root,
		"APPDATA=" + filepath.Join(root, "config"), "LOCALAPPDATA=" + filepath.Join(root, "data"),
		"XDG_CONFIG_HOME=" + filepath.Join(root, "config"), "XDG_CACHE_HOME=" + filepath.Join(root, "cache"),
		"XDG_DATA_HOME=" + filepath.Join(root, "data"), "XDG_STATE_HOME=" + filepath.Join(root, "state"),
		"TMPDIR=" + filepath.Join(root, "tmp"), "TMP=" + filepath.Join(root, "tmp"), "TEMP=" + filepath.Join(root, "tmp"),
		"GROK_HOME=" + config.Home, "GROK_DISABLE_AUTOUPDATER=1", "CI=1", "NO_COLOR=1",
	}
	if !subscription {
		env = append(env, "GROK_XAI_API_BASE_URL=http://127.0.0.1:1", "GROK_MODELS_BASE_URL=http://127.0.0.1:1", "GROK_MODELS_LIST_URL=http://127.0.0.1:1/models")
	}
	for _, vendor := range []string{"CLAUDE", "CURSOR"} {
		for _, surface := range []string{"SKILLS", "RULES", "AGENTS", "MCPS", "HOOKS"} {
			env = append(env, "GROK_"+vendor+"_"+surface+"_ENABLED=0")
		}
	}
	if runtime.GOOS == "windows" {
		for _, pair := range [][2]string{{"SystemRoot", "SYSTEMROOT"}, {"WINDIR", "WINDIR"}} {
			if value := lookup[pair[1]]; value != "" {
				if !filepath.IsAbs(value) {
					return nil, invalid()
				}
				env = append(env, pair[0]+"="+value)
			}
		}
	}
	return env, nil
}
