package terminal

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"log/slog"
	"os"
	"os/user"
	"strconv"
	"strings"
)

func defaultShell(ctx context.Context, root string, owner domain.ID, logger *slog.Logger) (string, error) {
	account, err := user.LookupId(strconv.Itoa(os.Geteuid()))
	if err != nil || strings.ContainsAny(account.Username, "/\x00\r\n") {
		return "", shellError()
	}
	output, err := accountShellQuery(ctx, root, "/usr/bin/dscl", owner, []string{"/Search", "-read", "/Users/" + account.Username, "UserShell"}, logger)
	if err != nil {
		return "", err
	}
	shell, ok := strings.CutPrefix(output, "UserShell:")
	if !ok || strings.ContainsAny(shell, "\r\n") {
		return "", shellError()
	}
	return strings.TrimSpace(shell), nil
}
func shellExecutable(info os.FileInfo) bool { return info.Mode().Perm()&0111 != 0 }
