// SPDX-License-Identifier: Apache-2.0
package claude

import (
	"encoding/json"
	"strconv"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// This is a provider-response observation, never a delta to add to task or
// parent-inclusive ledgers. Keep all validated native metadata and null fields.
func SubagentProviderUsage(native *ProviderUsage) (*domain.SubagentUsage, error) {
	if native == nil {
		return nil, nil
	}
	raw, err := json.Marshal(native)
	var checked ProviderUsage
	if err != nil || len(raw) > 64<<10 || json.Unmarshal(raw, &checked) != nil {
		return nil, lifecycleUncertain()
	}
	count := func(value *int64) *string {
		if value == nil {
			return nil
		}
		s := strconv.FormatInt(*value, 10)
		return &s
	}
	return &domain.SubagentUsage{Scope: domain.SubagentResponseUsage, Input: count(checked.Input), Output: count(checked.Output), NativeReport: string(raw)}, nil
}
