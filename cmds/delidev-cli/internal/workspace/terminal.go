// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"os"
	"time"
)

// Terminals use the immutable primary root but have their own process owner.
// They neither take an agent lease nor authorize preparation or recovery.
func (m *Manager) WithTerminalDirectory(ctx context.Context, input PrepareRequest, manifest Manifest, owner ReadRequest, launch func(string) error) error {
	owner.Preparation, owner.Manifest = input, manifest
	owner.Deadline = time.Now().Add(15 * time.Second)
	// Verify every original repository before native startup. The shared scope
	// still anchors and rechecks the primary directory around launch. A second
	// full Git verification after Resume would both spend another read budget
	// and treat legitimate shell startup changes as a failed creation; it cannot
	// retroactively grant native authority. File observations keep their separate
	// before/after identity contract.
	return m.observeWorkspace(ctx, owner, input.PrimaryRepository, false, func(_ context.Context, _ Git, _ Manifest, _ *os.Root, path string) error { return launch(path) })
}
