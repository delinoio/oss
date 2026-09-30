// SPDX-License-Identifier: Apache-2.0
package cli

import "context"

func dispatchStorage(ctx context.Context, c client, o options, rest []string, streams IO) (int, bool) {
	ensureRequest(&o)
	value, err := workspaceStorageCommand(ctx, c, o, rest)
	return emitResult(streams, o, value, err), true
}
