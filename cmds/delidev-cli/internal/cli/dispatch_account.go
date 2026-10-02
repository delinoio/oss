// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
)

func dispatchAccount(ctx context.Context, c client, o options, rest []string, streams IO) (int, bool) {
	emit := func(value any, err error) int { return emitResult(streams, o, value, err) }
	if len(rest) > 0 && (rest[0] == "connect" || rest[0] == "disconnect" || rest[0] == "status" || rest[0] == "validate" || rest[0] == "login" || rest[0] == "logout" || rest[0] == "refresh" || rest[0] == "cancel-login" || rest[0] == "login-progress") {
		if rest[0] != "status" && rest[0] != "login-progress" {
			ensureRequest(&o)
		}
		value, err := accountCommand(ctx, c, o, rest, streams)
		return emit(value, err), true
	}

	return 0, false
}
