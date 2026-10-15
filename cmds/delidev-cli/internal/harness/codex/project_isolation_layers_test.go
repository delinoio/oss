// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// isolationLayer names a Codex configuration layer that can contribute an MCP
// server or re-enable plugins.
type isolationLayer string

const (
	layerSystem  isolationLayer = "system"
	layerManaged isolationLayer = "managed"
	layerUser    isolationLayer = "user"
	layerProfile isolationLayer = "profile"
	layerProject isolationLayer = "project"
)

// isolationTiming names when the inventory is read relative to the native
// thread operation.
type isolationTiming string

const (
	timingPre  isolationTiming = "pre"
	timingPost isolationTiming = "post"
)

type survivorKind string

const (
	survivorServer  survivorKind = "server"
	survivorPlugins survivorKind = "plugins"
)

type layerCell struct {
	layer  isolationLayer
	timing isolationTiming
	kind   survivorKind
	reason isolationReason
}

// layerCells is the layer x timing x kind table of survivors: a definition
// that is still effective after the private-home rewrite. The system and
// managed layers are outside the private home, so the rewrite never removes
// them. The user, profile and project rows model a rewrite that failed to
// neutralize the layer, or drift after the check; the verification must fail
// closed for them too. Pre-thread survivors must stop before any native
// thread operation; post-bind survivors must stop before any input.
func layerCells() []layerCell {
	var cells []layerCell
	for _, layer := range []isolationLayer{layerSystem, layerManaged, layerUser, layerProfile, layerProject} {
		for _, timing := range []isolationTiming{timingPre, timingPost} {
			cells = append(cells, layerCell{layer, timing, survivorServer, isolationInventoryNonEmpty})
		}
	}
	for _, layer := range []isolationLayer{layerSystem, layerManaged} {
		for _, timing := range []isolationTiming{timingPre, timingPost} {
			cells = append(cells, layerCell{layer, timing, survivorPlugins, isolationPluginsEnabled})
		}
	}
	return cells
}

func TestIsolationLayerTimingCellsFailClosed(t *testing.T) {
	for _, cell := range layerCells() {
		for _, resume := range []bool{false, true} {
			name := fmt.Sprintf("%s/%s/%s/resume=%v", cell.layer, cell.timing, cell.kind, resume)
			t.Run(name, func(t *testing.T) {
				survivor := fmt.Sprintf("DELIDEV_CODEX_SURVIVOR=%s:%s:%s", cell.layer, cell.timing, cell.kind)
				run := runManagedIsolationWithEnv(t, "thread-managed-ready", resume, nil, survivor)
				problem := domain.SafeError(run.err)
				diagnostic := domain.CodexErrorDiagnostic(run.err)
				if run.err == nil || problem.Code != domain.Unsupported || diagnostic == nil || diagnostic.Phase != domain.CodexProfile {
					t.Fatalf("survivor was accepted or misclassified: %v", run.err)
				}
				stage := isolationStageMCPInventory
				if cell.timing == timingPre {
					stage = isolationStageMCPPreflight
				}
				if !strings.Contains(run.logs, "codex_native_isolation_rejected") || !strings.Contains(run.logs, "stage="+string(stage)) || !strings.Contains(run.logs, "reason="+string(cell.reason)) {
					t.Fatalf("structured warning missing: %s", run.logs)
				}
				if strings.Contains(run.logs+problem.Message+problem.Guidance, string(cell.layer)+"-server") || strings.Contains(run.logs, "private-") {
					t.Fatal("diagnostic exposed native configuration content")
				}
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
				if cell.timing == timingPre && threadOperations != 0 {
					t.Fatal("a native thread operation ran before the survivor was rejected")
				}
				if cell.timing == timingPost && threadOperations != 1 {
					t.Fatalf("post-bind survivor ran %d thread operations, want 1", threadOperations)
				}
				// A pre-thread rejection does not latch the connection to a thread;
				// a post-bind rejection does, so no input or second thread follows.
				if cell.timing == timingPost {
					if _, err := run.client.StartTurn(context.Background(), domain.NewID(), domain.NewID(), domain.SessionInput{Mode: domain.ExecuteMode, Prompt: "must not send"}); err == nil {
						t.Fatal("a rejected isolation accepted input")
					}
				}
				if err := run.client.Close(); err != nil {
					t.Fatal("owned native cleanup was not confirmed", err)
				}
			})
		}
	}
}

// TestIsolationProbeAcceptsRepositoryLayerOnlyBeforeProjectIsolation is the
// accepting cell of the project layer: the repository inventory is visible
// only to the read that precedes isolation, never to the pre-thread probe or
// the post-bind check.
func TestIsolationProbeAcceptsRepositoryLayerOnlyBeforeProjectIsolation(t *testing.T) {
	for _, resume := range []bool{false, true} {
		t.Run(fmt.Sprintf("resume=%v", resume), func(t *testing.T) {
			settings := threadSettings(t)
			run := runManagedIsolationWithEnv(t, "thread-managed-mcp-inventory", resume, func(s *ThreadSettings) { *s = settings; s.Provider = "openai" }, "DELIDEV_CODEX_MANAGED_WORKSPACE="+settings.Cwd)
			if run.err != nil {
				t.Fatalf("repository layer reached the isolated probe: %v", run.err)
			}
			if _, present := run.client.mcpStartupServers["project-server"]; !present {
				t.Fatal("the pre-isolation read lost the repository inventory")
			}
		})
	}
}
