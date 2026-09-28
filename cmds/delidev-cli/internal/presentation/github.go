// Package presentation owns closed local OS actions, never server business logic.
package presentation

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"time"
)

func launchEnvironment() []string {
	result := []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin"}
	for _, name := range []string{"HOME", "USER", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "SystemRoot", "WINDIR", "TEMP", "TMP", "TMPDIR", "LANG", "DISPLAY", "XAUTHORITY", "XDG_RUNTIME_DIR", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CURRENT_DESKTOP", "DESKTOP_SESSION", "DBUS_SESSION_BUS_ADDRESS"} {
		if value, ok := os.LookupEnv(name); ok {
			result = append(result, name+"="+value)
		}
	}
	return result
}

func launchCommand(raw string) (string, []string, error) {
	switch runtime.GOOS {
	case "darwin":
		return "/usr/bin/open", []string{raw}, nil
	case "linux":
		return "/usr/bin/xdg-open", []string{raw}, nil
	case "windows":
		executable, err := os.Executable()
		return executable, []string{"presentation", "dispatch-github", "--url", raw}, err
	default:
		return "", nil, domain.Fail(domain.Unsupported, "Native GitHub opening is unavailable on this platform.", "Open the prepared address on a supported desktop.")
	}
}

func OpenGitHub(ctx context.Context, raw string) error {
	return openGitHub(ctx, raw, launchCommand)
}

func openGitHub(ctx context.Context, raw string, selectCommand func(string) (string, []string, error)) error {
	if err := domain.ValidateGitHubPresentationURL(raw); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return domain.SafeError(err)
	}
	executable, args, err := selectCommand(raw)
	if err != nil {
		return domain.SafeError(err)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, args...)
	command.Env = launchEnvironment()
	// OS launchers may intentionally leave a browser running. Never lend them
	// CLI/sidecar pipes, tokens, provider environment or a shell command string.
	command.Stdin, command.Stdout, command.Stderr = nil, nil, nil
	configureCommand(command)
	if err := command.Run(); err != nil {
		slog.WarnContext(ctx, "github_open_failed", "platform", runtime.GOOS, "timed_out", ctx.Err() != nil)
		return domain.Fail(domain.Unavailable, "The browser launch was not confirmed.", "Inspect your browser before explicitly trying again; the operating system may already have opened the address.")
	}
	slog.InfoContext(ctx, "github_open_dispatched", "platform", runtime.GOOS)
	return nil
}
