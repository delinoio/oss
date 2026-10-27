// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"

	"github.com/delinoio/oss/packages/aiprouter/inference"
	"github.com/delinoio/oss/packages/aiprouter/store"
)

// Classification follows pinned routing/probe-lease.ts. The store charges a
// probe at grant and initial/retry traffic at the physical dispatch transaction.
// Core's model ordinal excludes support exchanges and unsuccessful write gates.
func nativeRecoveryClass(ctx context.Context, probe bool) store.NativeRecoveryClass {
	if probe {
		return store.NativeRecoveryProbe
	}
	if inference.ModelDispatchAttempt(ctx) > 1 {
		return store.NativeRecoveryRetry
	}
	return store.NativeRecoveryInitial
}
