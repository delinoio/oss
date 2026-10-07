//go:build !darwin

// SPDX-License-Identifier: Apache-2.0
package credentials

import "context"

// Other native stores do not use macOS executable signature tracking.
func CheckRuntime(ctx context.Context) error { return ctx.Err() }
