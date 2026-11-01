// SPDX-License-Identifier: Apache-2.0
package session

import "golang.org/x/sys/windows"

func cancelOwner(pid int) error {
	// The native supervisor starts in its own console process group.
	return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(pid))
}
