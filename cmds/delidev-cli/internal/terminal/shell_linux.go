// SPDX-License-Identifier: Apache-2.0
package terminal

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"log/slog"
	"os"
	"strconv"
	"strings"
)

func defaultShell(ctx context.Context, root string, owner domain.ID, logger *slog.Logger) (string, error) {
	uid := strconv.Itoa(os.Geteuid())
	output, err := accountShellQuery(ctx, root, "/usr/bin/getent", owner, []string{"passwd", uid}, logger)
	if err != nil {
		return "", err
	}
	fields := strings.Split(output, ":")
	if len(fields) != 7 || fields[2] != uid || strings.ContainsAny(fields[6], "\r\n") {
		return "", shellError()
	}
	return fields[6], nil
}
func shellExecutable(info os.FileInfo) bool { return info.Mode().Perm()&0111 != 0 }
