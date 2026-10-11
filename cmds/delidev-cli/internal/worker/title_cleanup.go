// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// titleRuntimeCleanup retains one cleanup outcome across explicit and deferred
// cleanup. An inference error cannot suppress uncertainty or authorize replay.
type titleRuntimeCleanup struct {
	closeNative, closeProxy, reconcileOwner func() error
	removeRuntime, syncParent               func() error
	reportFailure                           func(string)
	done                                    bool
	problem                                 error
}

func (c *titleRuntimeCleanup) run() error {
	if c.done {
		return c.problem
	}
	c.done = true
	step := func(stage, message string, action func() error) {
		if action != nil && action() != nil {
			if c.reportFailure != nil {
				c.reportFailure(stage)
			}
			if c.problem == nil {
				c.problem = domain.Fail(domain.RecoveryRequired, message, "Retain the original title ownership and reconcile cleanup without another inference attempt.")
			}
		}
	}
	// Join each independent owner even if another join fails. Preserve the
	// private runtime when a native owner has not proved its shutdown.
	step("native-close", "The private title runtime could not be closed cleanly.", c.closeNative)
	step("proxy-close", "The private title proxy could not be closed cleanly.", c.closeProxy)
	step("owner-reconciliation", "The original title process cleanup could not be verified.", c.reconcileOwner)
	if c.problem != nil {
		return c.problem
	}
	step("runtime-removal", "The private automatic title runtime could not be removed.", c.removeRuntime)
	if c.problem == nil {
		step("parent-sync", "The automatic title runtime removal could not be synchronized.", c.syncParent)
	}
	return c.problem
}

func (c *titleRuntimeCleanup) finish(output json.RawMessage, returned error) (json.RawMessage, error) {
	if err := c.run(); err != nil {
		return nil, err
	}
	return output, returned
}
