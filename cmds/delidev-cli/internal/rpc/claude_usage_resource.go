// SPDX-License-Identifier: Apache-2.0
package rpc

import (
	"bytes"
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// claudeUsageResourceDocument removes the reviewed OAuth identity text of an
// unlinked Claude usage confirmation from a generic Account projection. The
// email, organization and plan are short-lived presentation data for the client
// that requested the login progress; until the user links the identity, no
// ordinary account read may disclose them. Only the opaque commitment remains,
// so a client can still detect a changed review. A linked identity is the
// persistent, user-confirmed presentation of the connection and stays visible.
// A document that cannot be parsed, or that holds no confirming section, is
// returned unchanged.
func claudeUsageResourceDocument(raw []byte) []byte {
	if !bytes.Contains(raw, []byte(`"claude_usage"`)) {
		return raw
	}
	var account map[string]json.RawMessage
	if json.Unmarshal(raw, &account) != nil {
		return raw
	}
	var subscription map[string]json.RawMessage
	if json.Unmarshal(account["subscription"], &subscription) != nil {
		return raw
	}
	var usage map[string]json.RawMessage
	if json.Unmarshal(subscription["claude_usage"], &usage) != nil {
		return raw
	}
	var connection domain.ClaudeUsageConnection
	if json.Unmarshal(usage["connection"], &connection) != nil || connection != domain.ClaudeUsageConfirming {
		return raw
	}
	var identity struct {
		Commitment string `json:"commitment"`
	}
	if _, present := usage["identity"]; !present {
		return raw
	}
	// A malformed identity still loses its text: only a well-formed commitment
	// is carried over.
	_ = json.Unmarshal(usage["identity"], &identity)
	redacted, err := json.Marshal(identity)
	if err != nil {
		return []byte(`{}`)
	}
	usage["identity"] = redacted
	encodedUsage, err := json.Marshal(usage)
	if err != nil {
		return []byte(`{}`)
	}
	subscription["claude_usage"] = encodedUsage
	encoded, err := json.Marshal(subscription)
	if err != nil {
		return []byte(`{}`)
	}
	account["subscription"] = encoded
	public, err := json.Marshal(account)
	if err != nil {
		return []byte(`{}`)
	}
	return public
}
