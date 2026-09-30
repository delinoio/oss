// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
)

func dispatchSession(ctx context.Context, c client, o options, rest []string, streams IO) (int, bool) {
	emit := func(value any, err error) int { return emitResult(streams, o, value, err) }
	if len(rest) > 0 && rest[0] == "fork" {
		ensureRequest(&o)
		value, err := sessionForkCommand(ctx, c, o, rest[1:])
		return emit(value, err), true
	}
	if len(rest) > 0 && rest[0] == "budget" {
		if len(rest) > 1 && rest[1] != "get" {
			ensureRequest(&o)
		}
		value, err := budgetCommand(ctx, c, o, rest[1:])
		return emit(value, err), true
	}
	if len(rest) > 0 && rest[0] != "get" && rest[0] != "inspect" && rest[0] != "snapshot" {
		if rest[0] != "list" && rest[0] != "subagents" {
			ensureRequest(&o)
		}
		value, err := sessionCommand(ctx, c, o, rest, streams)
		return emit(value, err), true
	}

	return 0, false
}
