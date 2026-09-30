// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
)

func dispatchBackup(ctx context.Context, c client, o options, rest []string, streams IO) (int, bool) {
	emit := func(value any, err error) int { return emitResult(streams, o, value, err) }
	if len(rest) > 0 && (rest[0] == "create" || rest[0] == "delete") {
		ensureRequest(&o)
	}
	value, err := backupCommand(ctx, c, o, rest)
	return emit(value, err), true

}
