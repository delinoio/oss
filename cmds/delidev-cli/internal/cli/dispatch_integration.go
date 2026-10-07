// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
)

func dispatchIntegration(ctx context.Context, c client, o options, rest []string, streams IO) (int, bool) {
	emit := func(value any, err error) int { return emitResult(streams, o, value, err) }
	if len(rest) > 0 && rest[0] != "list" && rest[0] != "get" && rest[0] != "snapshot" {
		ensureRequest(&o)
		value, err := integrationCommand(ctx, c, o, rest, streams)
		return emit(value, err), true
	}

	return 0, false
}
