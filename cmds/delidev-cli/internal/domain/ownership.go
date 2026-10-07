// SPDX-License-Identifier: Apache-2.0
package domain

import "log/slog"

// OwnershipCheck names metadata observations. None grants or denies access.
type OwnershipCheck string

const (
	OwnershipActor    OwnershipCheck = "actor"
	OwnershipDevice   OwnershipCheck = "device"
	OwnershipMachine  OwnershipCheck = "machine"
	OwnershipInstance OwnershipCheck = "instance"
	OwnershipResource OwnershipCheck = "resource"
	OwnershipCleanup  OwnershipCheck = "cleanup"
)

// ObserveOwnership records mismatched attribution without logging native values.
// IDs are optional and must be validated before they can enter diagnostics.
func ObserveOwnership(check OwnershipCheck, id ID) {
	if id.Validate() != nil {
		id = ""
	}
	slog.Warn("ownership_observation", "operation_id", id, "check", check,
		"result", "unconfirmed", "next_action", "continue")
}
