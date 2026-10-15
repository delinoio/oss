// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"fmt"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// enabledState names the enabled value an effective MCP entry carries.
type enabledState struct {
	name string
	// raw is the JSON of the enabled value; empty leaves the key absent.
	raw string
	// reason is the closed rejection reason; empty means the entry is inert and
	// the thread is accepted.
	reason isolationReason
}

// enabledStates is the enabled axis of the inventory table: Codex launches an
// entry that is enabled or leaves the key absent, never one with enabled =
// false, and a value of any other type is not a state this adapter classifies.
func enabledStates() []enabledState {
	return []enabledState{
		{"absent", "", isolationInventoryNonEmpty},
		{"true", "true", isolationInventoryNonEmpty},
		{"false", "false", ""},
		{"null", "null", isolationInventoryUnreadable},
		{"string", `"false"`, isolationInventoryUnreadable},
		{"number", "0", isolationInventoryUnreadable},
	}
}

// TestIsolationInventoryEnabledStateCells runs the enabled axis for every layer
// that can contribute an effective entry and both timings, on start and resume.
// A disabled entry never launches, so it is accepted; every other state keeps
// the earlier rejection or is unreadable. Plugin sources are covered by the
// plugin feature rows: an enabled plugin entry is inert while the feature is
// false, and an effective true feature is rejected whatever the plugin entries
// say.
func TestIsolationInventoryEnabledStateCells(t *testing.T) {
	for _, layer := range []isolationLayer{layerSystem, layerManaged, layerUser, layerProfile, layerProject} {
		for _, timing := range []isolationTiming{timingPre, timingPost} {
			for _, state := range enabledStates() {
				for _, resume := range []bool{false, true} {
					name := fmt.Sprintf("%s/%s/enabled=%s/resume=%v", layer, timing, state.name, resume)
					t.Run(name, func(t *testing.T) {
						env := []string{fmt.Sprintf("DELIDEV_CODEX_SURVIVOR=%s:%s:%s", layer, timing, survivorServer)}
						if state.raw != "" {
							env = append(env, "DELIDEV_CODEX_SURVIVOR_ENABLED="+state.raw)
						}
						run := runManagedIsolationWithEnv(t, "thread-managed-ready", resume, nil, env...)
						var threadOperations, inputs int
						for _, entry := range run.capture {
							switch entry["method"] {
							case "thread/start", "thread/resume", "thread/fork":
								threadOperations++
							case "turn/start":
								inputs++
							}
						}
						if inputs != 0 {
							t.Fatal("input reached the native process")
						}
						if state.reason == "" {
							if run.err != nil {
								t.Fatalf("a disabled entry was rejected: %v", run.err)
							}
							if threadOperations != 1 || strings.Contains(run.logs, "codex_native_isolation_rejected") {
								t.Fatalf("disabled entry: %d thread operations, logs %s", threadOperations, run.logs)
							}
							return
						}
						problem := domain.SafeError(run.err)
						diagnostic := domain.CodexErrorDiagnostic(run.err)
						if run.err == nil || problem.Code != domain.Unsupported || diagnostic == nil || diagnostic.Phase != domain.CodexProfile {
							t.Fatalf("entry was accepted or misclassified: %v", run.err)
						}
						stage := isolationStageMCPInventory
						if timing == timingPre {
							stage = isolationStageMCPPreflight
						}
						if !strings.Contains(run.logs, "stage="+string(stage)) || !strings.Contains(run.logs, "reason="+string(state.reason)) {
							t.Fatalf("structured warning missing: %s", run.logs)
						}
						if strings.Contains(run.logs+problem.Message+problem.Guidance, string(layer)+"-server") || strings.Contains(run.logs, "private-") {
							t.Fatal("diagnostic exposed native configuration content")
						}
						if timing == timingPre && threadOperations != 0 || timing == timingPost && threadOperations != 1 {
							t.Fatalf("%d thread operations for the %s rejection", threadOperations, timing)
						}
					})
				}
			}
		}
	}
}
