// SPDX-License-Identifier: Apache-2.0
package process

import (
	"io"
)

// copyNativeOutput keeps consuming a native pipe after delivery fails. ConPTY
// can continue writing during ClosePseudoConsole, so its reader must remain
// active until the native side closes even when the downstream writer is gone.
func copyNativeOutput(reader io.Reader, writer io.Writer, deliveryFailed func()) error {
	if writer == nil {
		writer = io.Discard
	}
	_, err := io.Copy(writer, reader)
	if err != nil {
		deliveryFailed()
		_, _ = io.Copy(io.Discard, reader)
	}
	return err
}
