// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
)

func dispatchInbox(ctx context.Context, c client, o options, rest []string, streams IO) (int, bool) {
	emit := func(value any, err error) int { return emitResult(streams, o, value, err) }
	if len(rest) > 0 && rest[0] != "snapshot" {
		if rest[0] == "mark-read" || rest[0] == "mark-unread" {
			ensureRequest(&o)
		}
		value, err := inboxCommand(ctx, c, o, rest)
		return emit(value, err), true
	}

	return 0, false
}
