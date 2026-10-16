// SPDX-License-Identifier: Apache-2.0
package rpc

import (
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// subagentDisplayDocument moves the private retained display name out of the
// closed schema-1 child document and into additive Resource metadata. Older
// desktops therefore receive the unchanged document; an invalid retained name
// is dropped and never leaks into the public document.
func subagentDisplayDocument(id domain.ID, raw []byte) ([]byte, *pb.SubagentDisplayName) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return raw, nil
	}
	private, exists := fields["display_name"]
	if !exists {
		return raw, nil
	}
	delete(fields, "display_name")
	public, err := json.Marshal(fields)
	if err != nil {
		return nil, nil
	}
	var name string
	if json.Unmarshal(private, &name) != nil || !domain.ValidSubagentDisplayName(name) {
		return public, nil
	}
	return public, &pb.SubagentDisplayName{ChildId: string(id), Name: name}
}
