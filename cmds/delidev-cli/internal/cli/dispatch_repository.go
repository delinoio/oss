// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
)

func dispatchRepository(ctx context.Context, c client, o options, rest []string, streams IO) (int, bool) {
	emit := func(value any, err error) int { return emitResult(streams, o, value, err) }
	if len(rest) > 0 && rest[0] == "inspect" {
		ensureRequest(&o)
		value, err := repositoryInspect(ctx, c, o, rest[1:])
		return emit(value, err), true
	}

	return 0, false
}
