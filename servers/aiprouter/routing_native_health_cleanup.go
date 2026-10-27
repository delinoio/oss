// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"time"

	"github.com/delinoio/oss/packages/aiprouter/store"
)

// releaseTerminalNativeHealthPermit is called only after the owned transport
// has returned and closed its response body, or before any transport starts.
// Cancellation ends the request context, not its cleanup obligation. This
// operation releases ownership only; it never records recovery evidence.
// If PostgreSQL is unavailable, the permit remains attached to its admission.
// Atomic admission release retries that durable obligation; after a process
// exit, reconciliation requires proof that the instance's upstream work stopped.
func (a *inferenceAdapter) releaseTerminalNativeHealthPermit(ctx context.Context, l store.Lease, permit store.NativeHealthPermit) error {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	err := a.s.ReleaseTerminalNativeHealthPermit(cleanup, l, permit)
	if err != nil && a.telemetry != nil {
		a.telemetry.Event(cleanup, "native_health_terminal_cleanup_pending")
	}
	return err
}
