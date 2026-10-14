package opencode

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
var userAuthoredConfigurationNames = []string{"opencode.json", "opencode.jsonc", "config.json", "AGENTS.md", "agent", "agents", "command", "commands", "plugin", "plugins", "tool", "tools", "skill", "skills", "mode", "modes", "themes"}

// userAuthoredRootNames are retained login files below the profile root.
var userAuthoredRootNames = []string{"data/auth.json", "data/opencode/auth.json", "data/opencode/mcp-auth.json"}

func probeEnvironment(config ProbeConfig) ([]string, error) {
	invalid := func() error {
		return domain.Fail(domain.InvalidArgument, "OpenCode requires a canonical DeliDev-owned private profile.", "Use the persistent profile resolved by DeliDev before native protocol validation.")
	}
	if !filepath.IsAbs(config.Home) || filepath.Base(config.Home) != "opencode" {
		return nil, invalid()
	}
	root := filepath.Dir(config.Home)
	if config.Process.Cwd != root {
		return nil, invalid()
	}
	for _, name := range []string{"", "opencode", "config", "cache", "data", "state", "tmp"} {
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
	for _, name := range userAuthoredRootNames {
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(name))); !errors.Is(err, os.ErrNotExist) {
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
	// This fresh server performs only global readiness reads. Per-project
	// configuration, credentials and automatic model refresh are not inherited.
	env := []string{
		"PATH=" + lookup["PATH"], "HOME=" + root, "USERPROFILE=" + root,
		"APPDATA=" + filepath.Join(root, "config"), "LOCALAPPDATA=" + filepath.Join(root, "data"),
		"XDG_CONFIG_HOME=" + filepath.Join(root, "config"), "XDG_CACHE_HOME=" + filepath.Join(root, "cache"),
		"XDG_DATA_HOME=" + filepath.Join(root, "data"), "XDG_STATE_HOME=" + filepath.Join(root, "state"),
		"TMPDIR=" + filepath.Join(root, "tmp"), "TMP=" + filepath.Join(root, "tmp"), "TEMP=" + filepath.Join(root, "tmp"),
		"OPENCODE_CONFIG_DIR=" + config.Home, "OPENCODE_DISABLE_AUTOUPDATE=true", "CI=1", "NO_COLOR=1",
		"OPENCODE_DISABLE_MODELS_FETCH=true", "OPENCODE_DISABLE_PROJECT_CONFIG=true", `OPENCODE_CONFIG_CONTENT={"autoupdate":false}`,
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

func probePath(value string) bool {
	if value == "" || strings.ContainsRune(value, 0) {
		return false
	}
	for _, entry := range filepath.SplitList(value) {
		if !filepath.IsAbs(entry) {
			return false
		}
	}
	return true
}
