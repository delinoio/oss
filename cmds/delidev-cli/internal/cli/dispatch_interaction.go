// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
)

func dispatchInteraction(ctx context.Context, c client, o options, rest []string, streams IO) (int, bool) {
	emit := func(value any, err error) int { return emitResult(streams, o, value, err) }
	if len(rest) > 0 && rest[0] == "approve" {
		ensureRequest(&o)
		value, err := respondApproval(ctx, c, o, rest[1:], streams)
		return emit(value, err), true
	}
	if len(rest) > 0 && rest[0] == "respond" {
		ensureRequest(&o)
		value, err := respondQuestion(ctx, c, o, rest[1:], streams)
		return emit(value, err), true
	}

	return 0, false
}
