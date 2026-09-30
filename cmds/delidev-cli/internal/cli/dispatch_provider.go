// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"strings"
)

func dispatchProvider(ctx context.Context, c client, o options, rest []string, streams IO) (int, bool) {
	emit := func(value any, err error) int { return emitResult(streams, o, value, err) }
	presetCreate := false
	if len(rest) > 0 && rest[0] == "create" {
		for _, arg := range rest[1:] {
			name, _, _ := strings.Cut(arg, "=")
			if name == "--preset" {
				presetCreate = true
			}
		}
	}
	if len(rest) > 0 && (rest[0] == "presets" || rest[0] == "inventory" || rest[0] == "discover" || presetCreate) {
		if rest[0] != "presets" && rest[0] != "inventory" {
			ensureRequest(&o)
		}
		value, err := providerCatalog(ctx, c, o, rest)
		return emit(value, err), true
	}

	return 0, false
}
