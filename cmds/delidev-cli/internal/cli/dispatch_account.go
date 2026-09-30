// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
)

func dispatchAccount(ctx context.Context, c client, o options, rest []string, streams IO) (int, bool) {
	emit := func(value any, err error) int { return emitResult(streams, o, value, err) }
	if len(rest) > 0 && (rest[0] == "connect" || rest[0] == "disconnect" || rest[0] == "status" || rest[0] == "validate") {
		if rest[0] != "status" {
			ensureRequest(&o)
		}
		value, err := accountCommand(ctx, c, o, rest, streams)
		return emit(value, err), true
	}

	return 0, false
}
