// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
)

func dispatchConfiguration(ctx context.Context, c client, o options, rest []string, streams IO) (int, bool) {
	emit := func(value any, err error) int { return emitResult(streams, o, value, err) }
	if len(rest) > 0 && rest[0] == "apply" {
		ensureRequest(&o)
	}
	value, err := configurationTransfer(ctx, c, o, rest, streams)
	return emit(value, err), true

}
