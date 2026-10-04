// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"testing"
)

func TestNativeSubagentConfigurationUsesTypedOriginalStartAndResume(t *testing.T) {
	for _, resume := range []bool{false, true} {
		client, capture := openThreadFixture(t, "thread-ready")
		settings := threadSettings(t)
		settings.Options.SubagentModel, settings.Options.SubagentEffort, settings.Options.MaxConcurrency = "child-model", "medium", 64
		var err error
		if resume {
			_, err = client.ResumeThread(context.Background(), domain.NewID(), domain.NewID(), settings)
		} else {
			_, err = client.StartThread(context.Background(), domain.NewID(), settings)
		}
		if err != nil {
			t.Fatal(err)
		}
		config := capturedThreads(t, capture)[0]["params"].(map[string]any)["config"].(map[string]any)
		if config["agents.default_subagent_model"] != "child-model" || config["agents.default_subagent_reasoning_effort"] != "medium" || config["agents.max_concurrent_threads_per_session"] != float64(64) {
			t.Fatal("native configuration omitted or stringified an original option")
		}
		if config["model_reasoning_effort"] != settings.Effort {
			t.Fatal("child settings replaced root effort")
		}
	}
}

func TestNativeSubagentCompatibilityRejectsBeforeThreadSend(t *testing.T) {
	for _, incompatible := range []domain.AgentOptions{{SubagentModel: "unavailable-model"}, {SubagentModel: "child-model", SubagentEffort: "ultra"}} {
		client, capture := openThreadFixture(t, "thread-ready")
		settings := threadSettings(t)
		settings.Options.SubagentModel, settings.Options.SubagentEffort = incompatible.SubagentModel, incompatible.SubagentEffort
		_, err := client.StartThread(context.Background(), domain.NewID(), settings)
		if domain.SafeError(err).Code != domain.Unsupported {
			t.Fatal("unsupported native child configuration admitted", err)
		}
		if entries := capturedThreads(t, capture); len(entries) != 0 {
			t.Fatal("incompatible child configuration sent a native thread request")
		}
	}
}
