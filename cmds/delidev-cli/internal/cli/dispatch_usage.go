// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
)

func dispatchUsage(ctx context.Context, c client, o options, rest []string, streams IO) (int, bool) {
	emit := func(value any, err error) int { return emitResult(streams, o, value, err) }
	if len(rest) > 0 && rest[0] == "pricing" {
		if len(rest) > 1 && rest[1] == "set" {
			ensureRequest(&o)
		}
		value, err := pricingCommand(ctx, c, o, rest[1:], streams.In)
		return emit(value, err), true
	}
	if len(rest) > 0 && rest[0] == "summary" {
		value, err := usageCommand(ctx, c, rest)
		return emit(value, err), true
	}

	return 0, false
}
