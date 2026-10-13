//go:build windows

// SPDX-License-Identifier: Apache-2.0
package security

import "context"

// removeTree removes expected at path. Windows has no descriptor-relative unlink,
// so removal uses the path-based inventory in removeTreeByPath, which re-binds
// the entry and all of its ancestors to their held directories before every
// unlink. That narrows, but does not close, the replacement window.
func removeTree(ctx context.Context, path string, expected PathIdentity) error {
	return removeTreeByPath(ctx, path, expected)
}
