package terminal

import (
	"context"
	"errors"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"log/slog"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func defaultShell(_ context.Context, _ string, _ domain.ID, _ *slog.Logger) (string, error) {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\OpenSSH`, registry.QUERY_VALUE)
	if err == nil {
		defer key.Close()
		shell, _, readErr := key.GetStringValue("DefaultShell")
		if readErr == nil {
			return shell, nil
		}
		if !errors.Is(readErr, registry.ErrNotExist) {
			return "", shellError()
		}
	} else if !errors.Is(err, registry.ErrNotExist) {
		return "", shellError()
	}
	// Windows has no passwd account login-shell field. Its native default
	// command interpreter applies only when OpenSSH has no configured shell.
	directory, err := windows.GetSystemDirectory()
	if err != nil {
		return "", shellError()
	}
	return filepath.Join(directory, "cmd.exe"), nil
}
func shellExecutable(info os.FileInfo) bool { return info.Mode().IsRegular() }
