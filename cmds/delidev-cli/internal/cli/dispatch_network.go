// SPDX-License-Identifier: Apache-2.0
package cli

import "context"

func dispatchNetwork(ctx context.Context, c client, o options, rest []string, streams IO) (int, bool) {
	ensureRequest(&o)
	value, err := networkCommand(ctx, c, o, rest, streams)
	return emitResult(streams, o, value, err), true
}
