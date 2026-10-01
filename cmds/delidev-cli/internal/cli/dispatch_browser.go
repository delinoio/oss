// SPDX-License-Identifier: Apache-2.0
package cli

import "context"

func dispatchBrowser(ctx context.Context, c client, o options, args []string, streams IO) int {
	ensureRequest(&o)
	value, err := browserCommand(ctx, c, o, args)
	return emitResult(streams, o, value, err)
}
