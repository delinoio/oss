// SPDX-License-Identifier: Apache-2.0
package rpc

import (
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Every SUBAGENT read/change/event uses Resource. The private display name stays
// outside the closed schema-1 document so older strict readers are unaffected;
// an invalid retained name is dropped and never leaks into the document.
func subagentDisplayNameDocument(raw []byte) ([]byte, string) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return raw, ""
	}
	private, exists := fields["display_name"]
	if !exists {
		return raw, ""
	}
	delete(fields, "display_name")
	public, err := json.Marshal(fields)
	if err != nil {
		return nil, ""
	}
	var name string
	if json.Unmarshal(private, &name) != nil || !domain.ValidSubagentDisplayName(name) {
		return public, ""
	}
	return public, name
}
