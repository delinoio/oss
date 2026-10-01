// SPDX-License-Identifier: Apache-2.0
package rpc

import (
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Accepted bytes remain private dispatch data until the owning Worker reports
// their once-only outcome. Every resource projection, including generic reads
// and stream metadata, must omit them even if terminal echo is disabled.
func terminalResourceDocument(raw []byte) []byte {
	var value domain.Terminal
	if domain.Decode(raw, &value) != nil {
		// A malformed terminal must never fall back to its private document.
		return []byte(`{}`)
	}
	if value.Pending == nil || len(value.Pending.Input) == 0 {
		return raw
	}
	operation := *value.Pending
	operation.Input = nil
	value.Pending = &operation
	public, err := json.Marshal(value)
	if err != nil {
		return []byte(`{}`)
	}
	return public
}
