// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
)

func dispatchActivity(ctx context.Context, c client, o options, rest []string, streams IO) (int, bool) {
	emit := func(value any, err error) int { return emitResult(streams, o, value, err) }
	value, err := activityCommand(ctx, c, rest)
	return emit(value, err), true

}
