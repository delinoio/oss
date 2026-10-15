package runmoor

import (
	"context"
	"os"
	"runtime"
	"strings"
)

func validLocalDockerEndpoint(endpoint, goos string) bool {
	if strings.ContainsAny(endpoint, "\x00\r\n") {
		return false
	}
	if goos == "windows" {
		const prefix = "npipe:////./pipe/"
		return strings.HasPrefix(endpoint, prefix) && safeName.MatchString(strings.TrimPrefix(endpoint, prefix))
	}
	return strings.HasPrefix(endpoint, "unix:///")
}
func validWindowsRunnerPath(p string) bool {
	if len(p) < 4 || p[1:3] != `:\` || p[0] < 'A' || p[0] > 'Z' {
		return false
	}
	for _, part := range strings.Split(p[3:], `\`) {
		if !safeWindowsArchiveName(part) {
			return false
		}
	}
	return !strings.EqualFold(p, `C:\Windows`) && !strings.EqualFold(p, `C:\Program Files`)
}
func validPoolRunnerPath(p, goos string) bool {
	if goos == "windows" {
		return validWindowsRunnerPath(p)
	}
	return validRunnerPath(p)
}

// Windows receives only the selectors required by native tools. Management
// credentials are resolved separately and never forwarded into job processes.
func windowsEnvironment() []string {
	var env []string
	for _, name := range []string{"PATH", "SystemRoot", "WINDIR", "COMSPEC", "PATHEXT", "LOCALAPPDATA", "APPDATA", "PROGRAMDATA", "USERPROFILE"} {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	return env
}
func prepareHostWorkspace(root *os.Root) error {
	if runtime.GOOS == "windows" {
		return root.Mkdir("runner/_work", 0700)
	}
	return root.Symlink("../work", "runner/_work")
}

// Other hosts never select these Windows implementation hooks.
var windowsPlatform = func(context.Context) error {
	return problem(ErrPlatform, "Windows is unavailable.", "Use a supported host.")
}
var newPowerController = func() PowerController { return &PowerManager{} }

var hostRootIdentity = func(root *os.Root, info os.FileInfo) string { return hostFileIdentity(info) }

var windowsServiceOperation = func(context.Context, string, string, Config, CommandExecutor) error { return nil }
var windowsReloadOperation = func(context.Context, string, Config) error { return nil }
var windowsStartupOperation = func(_ context.Context, _ string, c Config) (Config, bool, *serviceReloadJournal, error) {
	return c, false, nil, nil
}
