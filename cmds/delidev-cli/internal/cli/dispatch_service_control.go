// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
)

func dispatchServiceControl(ctx context.Context, c client, o options, rest []string, streams IO) (int, bool) {
	emit := func(value any, err error) int { return emitResult(streams, o, value, err) }
	ensureRequest(&o)
	value, err := serviceRPC(ctx, c, o, rest)
	return emit(value, err), true

}
