// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type sleepItem struct {
	Type       string  `json:"type"`
	ID         string  `json:"id"`
	DurationMS *uint64 `json:"durationMs"`
}

// Live and retained history share the official bounded display-item profile.
// Validation never invokes clock.sleep, changes input delivery or settles a turn.
func decodeSleep(raw json.RawMessage) (*sleepItem, error) {
	var fields map[string]json.RawMessage
	if domain.Decode(raw, &fields) != nil || len(fields) != 3 {
		return nil, incompatible()
	}
	for key := range fields {
		if !slices.Contains([]string{"type", "id", "durationMs"}, key) {
			return nil, incompatible()
		}
	}
	var item sleepItem
	if domain.Decode(raw, &item) != nil || item.Type != "sleep" || item.DurationMS == nil || domain.Text(item.ID, "native sleep item", 1024, true) != nil {
		return nil, incompatible()
	}
	return &item, nil
}
