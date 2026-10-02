// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
)

func dispatchModel(ctx context.Context, c client, o options, rest []string, streams IO) (int, bool) {
	emit := func(value any, err error) int { return emitResult(streams, o, value, err) }
	if len(rest) > 0 && (rest[0] == "native-discover" || rest[0] == "native-observation" || rest[0] == "native-list" || rest[0] == "native-cancel") {
		value, err := nativeModelCatalog(ctx, c, o, rest)
		return emit(value, err), true
	}
	if len(rest) > 0 && (rest[0] == "search" || rest[0] == "resolve") {
		value, err := modelCatalog(ctx, c, rest)
		return emit(value, err), true
	}

	return 0, false
}
