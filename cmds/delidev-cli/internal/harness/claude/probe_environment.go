package claude

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func probeEnvironment(config ProbeConfig) ([]string, error) {
	invalid := func() error {
		return domain.Fail(domain.InvalidArgument, "Claude Code discovery requires a fresh private runtime.", "Prepare a dedicated canonical Worker runtime before native protocol validation.")
	}
	if !filepath.IsAbs(config.Home) {
		return nil, invalid()
	}
	home, err := filepath.EvalSymlinks(config.Home)
	if err != nil || home != filepath.Clean(config.Home) || filepath.Base(home) != "claude" {
		return nil, invalid()
	}
	root := filepath.Dir(home)
	if config.Process.Cwd != root {
		return nil, invalid()
	}
	for _, name := range []string{"", "claude", "config", "cache", "data", "state", "tmp"} {
		path := filepath.Join(root, name)
		if err := security.CheckPrivateDir(path); err != nil {
			return nil, invalid()
		}
		actual, err := filepath.EvalSymlinks(path)
		if err != nil || actual != path {
			return nil, invalid()
		}
		// Never probe a retained execution runtime or consume an existing login,
		// hook, plugin, cache or settings file. Version detection uses this same
		// empty runtime before the protocol probe.
		if name != "" {
			entries, err := os.ReadDir(path)
			if err != nil || len(entries) != 0 {
				return nil, invalid()
			}
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
	// Construct the environment from owned paths, not caller-controlled native
	// credentials/config. No OAuth, API key, proxy, loader or shell is inherited.
	// The loopback-only authority prevents accidental remote model traffic;
	// initialization itself does not issue any model request.
	env := []string{
		"PATH=" + lookup["PATH"], "HOME=" + root, "USERPROFILE=" + root,
		"APPDATA=" + filepath.Join(root, "config"), "LOCALAPPDATA=" + filepath.Join(root, "data"),
		"XDG_CONFIG_HOME=" + filepath.Join(root, "config"), "XDG_CACHE_HOME=" + filepath.Join(root, "cache"),
		"XDG_DATA_HOME=" + filepath.Join(root, "data"), "XDG_STATE_HOME=" + filepath.Join(root, "state"),
		"TMPDIR=" + filepath.Join(root, "tmp"), "TMP=" + filepath.Join(root, "tmp"), "TEMP=" + filepath.Join(root, "tmp"),
		"CLAUDE_CONFIG_DIR=" + home, "ANTHROPIC_BASE_URL=http://127.0.0.1:1",
		"CI=1", "NO_COLOR=1", "DISABLE_AUTOUPDATER=1", "DISABLE_UPDATES=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
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
