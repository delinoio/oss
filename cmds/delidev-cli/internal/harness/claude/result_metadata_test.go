// SPDX-License-Identifier: Apache-2.0
package claude

import (
	"encoding/json"
	"testing"
)

func TestNativeSubagentCountersRequireCompleteBoundedShape(t *testing.T) {
	const fixture = `{"spawned":0,"requested":{"background":0,"foreground":0,"unset":0},"started_in_background":0,"max_depth":0,"spawned_by_subagents":0,"completed":0,"failed":0,"killed":{"parent":0,"user":0,"system":0},"refused":{"depth_limit":0,"concurrency_limit":0,"budget":0},"by_type":{}}`
	for _, name := range []string{"valid", "missing", "negative", "unknown", "null"} {
		t.Run(name, func(t *testing.T) {
			var value map[string]any
			if err := json.Unmarshal([]byte(fixture), &value); err != nil {
				t.Fatal(err)
			}
			switch name {
			case "missing":
				delete(value, "spawned")
			case "negative":
				value["completed"] = -1
			case "unknown":
				value["executions"] = 1
			case "null":
				value["by_type"] = nil
			}
			raw, _ := json.Marshal(value)
			if validNativeSubagentStats(raw) != (name == "valid") {
				t.Fatal("unexpected native aggregate acceptance")
			}
		})
	}
}

func TestNativeModelUsagePreservesThinkingAndCostBasis(t *testing.T) {
	for _, basis := range []string{"list", "managed", "unknown", "foreign"} {
		var usage NativeModelUsage
		raw, _ := json.Marshal(map[string]any{"thinkingTokens": 2, "costBasis": basis})
		err := json.Unmarshal(raw, &usage)
		if basis == "foreign" {
			if err == nil {
				t.Fatal("unknown cost basis accepted")
			}
			continue
		}
		if err != nil || usage.Thinking == nil || *usage.Thinking != 2 || usage.CostBasis == nil || string(*usage.CostBasis) != basis {
			t.Fatal("native model metadata changed", err)
		}
	}
	var usage NativeModelUsage
	if json.Unmarshal([]byte(`{"thinkingTokens":-1,"costBasis":"list"}`), &usage) == nil {
		t.Fatal("negative native thinking count accepted")
	}
}
