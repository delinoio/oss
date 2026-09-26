package opencode

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestSessionAgentTransitionNeedsOriginalInputAndNeverRollsBack(t *testing.T) {
	for _, scenario := range []string{"valid", "before-input", "stored-before-metadata", "model", "variant"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			f := newSessionFixture(t)
			f.create(t)
			// Native restoration has independently verified the original Build
			// session and the immutable next input's Plan settings.
			f.api.sessionAgent, f.api.creation.settings.Agent = BuildAgent, PlanAgent
			if _, err := f.api.inspectSession(ctx); err != nil {
				t.Fatal("pre-input metadata lost original agent", err)
			}
			if scenario == "before-input" {
				f.mu.Lock()
				f.session["agent"] = PlanAgent
				f.mu.Unlock()
				if _, err := f.api.inspectSession(ctx); err == nil {
					t.Fatal("unclaimed input changed native agent")
				}
				return
			}
			receipt, err := f.api.submit(ctx, domain.NewID(), fixtureMessageID, fixturePartID, "Original mode input")
			if err != nil {
				t.Fatal(err)
			}
			f.setMissing(true)
			pending, err := f.api.inspectInput(ctx)
			if err != nil || pending.Recorded || f.api.sessionAgent != BuildAgent {
				t.Fatal("asynchronous pending input fabricated a mode transition", err)
			}
			f.mu.Lock()
			f.missing = false
			f.input = fixtureInput(receipt, f.api.creation.settings, "Original mode input")
			if scenario != "stored-before-metadata" {
				f.session["agent"] = PlanAgent
			}
			model := map[string]any{"id": fixtureSettings().Model, "providerID": fixtureSettings().Provider, "variant": "default"}
			if scenario == "model" {
				model["id"] = "foreign"
			}
			if scenario == "variant" {
				model["variant"] = "foreign"
			}
			f.session["model"] = model
			f.mu.Unlock()
			accepted, err := f.api.inspectInput(ctx)
			if scenario != "valid" {
				if err == nil || accepted.Recorded {
					t.Fatal("contradictory metadata became input acceptance")
				}
				return
			}
			if err != nil || !accepted.Recorded || f.api.sessionAgent != PlanAgent || f.postCount() != 2 {
				t.Fatal("original mode transition was lost or replayed", err)
			}
			f.mu.Lock()
			f.session["agent"] = BuildAgent
			f.mu.Unlock()
			if _, err := f.api.inspectSession(ctx); err == nil {
				t.Fatal("observed native agent rolled back to predecessor")
			}
		})
	}
}
