package worker

import (
	"os"
	"os/user"
	"runtime"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func terminalEnvironment() ([]string, error) {
	account, err := user.Current()
	if err != nil || strings.ContainsAny(account.HomeDir+account.Username, "\x00") {
		return nil, domain.Fail(domain.Unavailable, "The Worker's native account context is unavailable.", "Inspect the Worker account before creating a terminal.")
	}
	env := []string{"TERM=xterm-256color", "HOME=" + account.HomeDir, "USER=" + account.Username, "LOGNAME=" + account.Username}
	// Inherit only ordinary interactive tool/session locations, never pairing,
	// inference or provider credentials from the Worker's environment.
	keys := []string{"PATH", "LANG", "LC_ALL", "LC_CTYPE", "TMPDIR"}
	if runtime.GOOS == "windows" {
		keys = append(keys, "SystemRoot", "SystemDrive", "WINDIR", "TEMP", "TMP", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "PATHEXT")
	}
	for _, key := range keys {
		if value, ok := os.LookupEnv(key); ok && !strings.ContainsRune(value, 0) {
			env = append(env, key+"="+value)
		}
	}
	return env, nil
}
