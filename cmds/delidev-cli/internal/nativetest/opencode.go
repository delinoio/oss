// Package nativetest holds opt-in test support for real native harness
// binaries. It is imported only by *_test.go files and has no production
// wiring. It depends on the domain package alone so that tests in
// internal/harness and internal/harness/opencode can import it without an
// import cycle.
package nativetest

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// OpenCodeExecutableEnv selects the installed native OpenCode binary that the
// opt-in native tests exercise. Unset means every native test skips.
const OpenCodeExecutableEnv = "DELIDEV_NATIVE_OPENCODE_EXECUTABLE"

const versionTimeout = 20 * time.Second

// openCodeVersions caches successful observations per executable so one
// `go test` process runs each selected binary's --version once.
var openCodeVersions sync.Map

// NativeOpenCodeExecutable returns the absolute native OpenCode executable
// selected by DELIDEV_NATIVE_OPENCODE_EXECUTABLE together with its observed
// SemVer. It skips when the variable is unset or the binary is below the
// minimum admitted version (domain.HarnessVersionAllowed), and fails the test
// for a relative path, an executable that cannot report a version, or output
// that is not a valid SemVer. Newer admitted versions are returned unchanged;
// callers must use the observed version, never the baseline constant, when
// they describe the real binary.
func NativeOpenCodeExecutable(t testing.TB) (string, string) {
	t.Helper()
	path := os.Getenv(OpenCodeExecutableEnv)
	if path == "" {
		t.Skipf("%s is not set; native OpenCode tests are opt-in", OpenCodeExecutableEnv)
	}
	if !filepath.IsAbs(path) {
		t.Fatalf("%s must be an absolute path, got %q", OpenCodeExecutableEnv, path)
	}
	version, cached := "", false
	if value, ok := openCodeVersions.Load(path); ok {
		version, cached = value.(string), true
	}
	if !cached {
		var err error
		version, err = ObserveOpenCodeVersion(t.TempDir(), path)
		if err != nil {
			t.Fatalf("native OpenCode %s: %v", path, err)
		}
		openCodeVersions.Store(path, version)
	}
	t.Logf("native OpenCode installed_version=%s baseline_version=%s", version, domain.OpenCodeProtocolVersion)
	if !domain.HarnessVersionAllowed(domain.OpenCode, version) {
		t.Skipf("installed OpenCode %s is below the minimum %s; upgrade it to run native OpenCode tests", version, domain.OpenCodeProtocolVersion)
	}
	return path, version
}

// ObserveOpenCodeVersion runs `path --version` with a private runtime
// environment rooted at home and returns the parsed SemVer.
func ObserveOpenCodeVersion(home, path string) (string, error) {
	env, err := PrivateRuntimeEnvironment(home)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), versionTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, path, "--version")
	command.Env, command.Dir = env, home
	output, err := command.Output()
	if err != nil {
		return "", errors.New("--version failed: " + err.Error())
	}
	return ParseVersion(string(output))
}

// ParseVersion accepts one bounded SemVer line with an optional leading "v",
// matching how discovery parses OpenCode.
func ParseVersion(raw string) (string, error) {
	value := strings.TrimPrefix(strings.TrimSpace(raw), "v")
	if !domain.ValidInstallationVersion(value) {
		return "", errors.New("--version output is not a SemVer")
	}
	return value, nil
}

// PrivateRuntimeEnvironment mirrors harness.PrivateRuntimeEnvironment, which
// this package cannot import without a cycle. A parity test in this package
// fails if the two ever diverge.
func PrivateRuntimeEnvironment(home string) ([]string, error) {
	for _, name := range []string{"", "config", "cache", "data", "state", "tmp", "codex", "claude", "opencode", "grok"} {
		if err := os.MkdirAll(filepath.Join(home, name), 0o700); err != nil {
			return nil, err
		}
	}
	env := []string{"PATH=" + cleanPath(), "HOME=" + home, "USERPROFILE=" + home, "APPDATA=" + filepath.Join(home, "config"), "LOCALAPPDATA=" + filepath.Join(home, "data"), "XDG_CONFIG_HOME=" + filepath.Join(home, "config"), "XDG_CACHE_HOME=" + filepath.Join(home, "cache"), "XDG_DATA_HOME=" + filepath.Join(home, "data"), "XDG_STATE_HOME=" + filepath.Join(home, "state"), "TMPDIR=" + filepath.Join(home, "tmp"), "TMP=" + filepath.Join(home, "tmp"), "TEMP=" + filepath.Join(home, "tmp"), "CODEX_HOME=" + filepath.Join(home, "codex"), "CLAUDE_CONFIG_DIR=" + filepath.Join(home, "claude"), "OPENCODE_CONFIG_DIR=" + filepath.Join(home, "opencode"), "NO_COLOR=1", "CI=1", "DISABLE_AUTOUPDATER=1", "DISABLE_UPDATES=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "OPENCODE_DISABLE_AUTOUPDATE=true", "OPENCODE_DISABLE_DEFAULT_PLUGINS=true", "OPENCODE_DISABLE_MODELS_FETCH=true", "OPENCODE_DISABLE_LSP_DOWNLOAD=true", "OPENCODE_DISABLE_CLAUDE_CODE=true", `OPENCODE_CONFIG_CONTENT={"autoupdate":false}`}
	if runtime.GOOS == "windows" {
		for _, name := range []string{"SystemRoot", "WINDIR"} {
			if value := os.Getenv(name); value != "" {
				env = append(env, name+"="+value)
			}
		}
	}
	return append(env, "GROK_HOME="+filepath.Join(home, "grok"), "GROK_DISABLE_AUTOUPDATER=1"), nil
}

func cleanPath() string {
	var paths []string
	for _, entry := range filepath.SplitList(os.Getenv("PATH")) {
		if filepath.IsAbs(entry) {
			paths = append(paths, entry)
		}
	}
	return strings.Join(paths, string(os.PathListSeparator))
}
