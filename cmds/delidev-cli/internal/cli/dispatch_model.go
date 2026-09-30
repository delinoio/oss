// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
)

func dispatchModel(ctx context.Context, c client, o options, rest []string, streams IO) (int, bool) {
	emit := func(value any, err error) int { return emitResult(streams, o, value, err) }
	if len(rest) > 0 && (rest[0] == "search" || rest[0] == "resolve") {
		value, err := modelCatalog(ctx, c, rest)
		return emit(value, err), true
	}

	return 0, false
}
