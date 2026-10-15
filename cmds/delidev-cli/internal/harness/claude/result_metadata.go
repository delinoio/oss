// SPDX-License-Identifier: Apache-2.0
package claude

import (
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Native aggregate counters describe observations, not task ownership or outcomes.
// Task lifecycle events remain the only authority for publishing subagent state.
func validNativeSubagentStats(raw json.RawMessage) bool {
	var stats struct {
		Spawned   *uint64 `json:"spawned"`
		Requested struct {
			Background *uint64 `json:"background"`
			Foreground *uint64 `json:"foreground"`
			Unset      *uint64 `json:"unset"`
		} `json:"requested"`
		StartedBackground  *uint64           `json:"started_in_background"`
		ByType             map[string]uint64 `json:"by_type"`
		MaxDepth           *uint64           `json:"max_depth"`
		SpawnedBySubagents *uint64           `json:"spawned_by_subagents"`
		Completed          *uint64           `json:"completed"`
		Failed             *uint64           `json:"failed"`
		Killed             struct {
			Parent *uint64 `json:"parent"`
			User   *uint64 `json:"user"`
			System *uint64 `json:"system"`
		} `json:"killed"`
		Refused struct {
			Depth       *uint64 `json:"depth_limit"`
			Concurrency *uint64 `json:"concurrency_limit"`
			Budget      *uint64 `json:"budget"`
		} `json:"refused"`
	}
	if !requireExactNativeKeys(raw, &stats) || domain.Decode(raw, &stats) != nil || stats.ByType == nil || len(stats.ByType) > 256 {
		return false
	}
	for _, count := range []*uint64{stats.Spawned, stats.Requested.Background, stats.Requested.Foreground, stats.Requested.Unset, stats.StartedBackground, stats.MaxDepth, stats.SpawnedBySubagents, stats.Completed, stats.Failed, stats.Killed.Parent, stats.Killed.User, stats.Killed.System, stats.Refused.Depth, stats.Refused.Concurrency, stats.Refused.Budget} {
		if count == nil {
			return false
		}
	}
	for name := range stats.ByType {
		if domain.Text(name, "native subagent type", 256, true) != nil {
			return false
		}
	}
	return true
}
