package grok

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

func TestPublicToolJournalRetainsForeignPlanLocator(t *testing.T) {
	for _, locator := range []string{"/foreign/runtime/session/plan.md", `C:\foreign\runtime\session\plan.md`, `\\foreign\share\session\plan.md`} {
		for _, scenario := range []string{"approved", "cancelled", "abandoned", "revised"} {
			t.Run(locator+"/"+scenario, func(t *testing.T) {
				original, events := planningEvents(t, scenario)
				journal, err := NewToolJournal(turnFixtureSession, turnFixturePrompt, domain.GrokDefaultMode, "")
				if err != nil {
					t.Fatal(err)
				}
				bound, err := NewToolJournal(turnFixtureSession, turnFixturePrompt, domain.GrokDefaultMode, locator)
				if err != nil {
					t.Fatal("prebound foreign locator", err)
				}
				for i, event := range events {
					fact, err := original.observe(event)
					if err != nil {
						t.Fatal("original", i, err)
					}
					projection, err := publicToolEvent(event, fact)
					if err != nil {
						t.Fatal("projection", i, err)
					}
					raw, _ := json.Marshal(projection)
					var value any
					if json.Unmarshal(raw, &value) != nil {
						t.Fatal("projection JSON", i)
					}
					raw, _ = json.Marshal(rewritePlanningLocator(value, original.plans.path, locator))
					var retained domain.GrokToolEvent
					if domain.Decode(raw, &retained) != nil {
						t.Fatal("retained JSON", i)
					}
					if retained.RequestID != nil {
						var proposal any
						if json.Unmarshal([]byte(retained.ProposalJSON), &proposal) != nil {
							t.Fatal("original request JSON", i)
						}
						encoded, err := json.Marshal(rewritePlanningLocator(proposal, original.plans.path, locator))
						if err != nil {
							t.Fatal(err)
						}
						retained.ProposalJSON = string(encoded)
					}
					if u := retained.Payload.Update; u != nil && u.Output != nil && u.Output.Ready != nil {
						var changed domain.GrokToolEvent
						if domain.Decode(raw, &changed) != nil {
							t.Fatal("changed locator JSON", i)
						}
						changed.Payload.Update.Output.Ready.Path += ".changed"
						for _, reducer := range []*ToolJournal{journal, bound} {
							before := planningSnapshot(reducer.observer)
							if reducer.Observe(changed) == nil || !reflect.DeepEqual(before, reducer.observer) {
								t.Fatal("changed locator accepted or changed original evidence")
							}
						}
					}
					for _, reducer := range []*ToolJournal{journal, bound} {
						if err := reducer.Observe(retained); err != nil {
							t.Fatal("foreign journal", i, err)
						}
					}
					if fact.Plan != nil && fact.Plan.Interaction != nil && fact.Plan.Interaction.Stage == planApprovalResolved {
						outcome := PlanOutcome(scenario)
						if scenario == "revised" {
							outcome = PlanCancelled
							if original.plans.tools[fact.Plan.Interaction.ID].artifact.origin.Revision == 2 {
								outcome = PlanApproved
							}
						}
						if err := original.plans.bindResponse(fact.Plan.Interaction.ID, outcome); err != nil {
							t.Fatal(err)
						}
						for _, reducer := range []*ToolJournal{journal, bound} {
							if err := reducer.ObservePlanDecision(fact.Plan.Interaction.ID, outcome); err != nil {
								t.Fatal(err)
							}
						}
					}
				}
				for _, reducer := range []*ToolJournal{journal, bound} {
					if !reducer.Settled() || reducer.observer.plans.path != locator {
						t.Fatal("original Plan lifecycle or exact foreign locator changed")
					}
				}
			})
		}
	}
}

func TestPublicToolJournalBoundsRetainedPlanLocator(t *testing.T) {
	for _, locator := range []string{"nul\x00locator", strings.Repeat("p", 8193), string([]byte{0xff})} {
		if _, err := NewToolJournal(turnFixtureSession, turnFixturePrompt, domain.GrokDefaultMode, locator); err == nil {
			t.Fatal("invalid retained Plan locator accepted")
		}
	}
}

func TestPublicToolJournalPreservesOriginalMixedAndPlanFacts(t *testing.T) {
	for _, scenario := range []string{"mixed", "approved", "cancelled", "abandoned", "revised"} {
		t.Run(scenario, func(t *testing.T) {
			original, events := planningEvents(t, "approved")
			if scenario == "mixed" {
				original, _ = newMixedTools(turnFixtureSession, turnFixturePrompt)
				events = mixedFixtureEvents(t)
			} else {
				original, events = planningEvents(t, scenario)
			}
			journal, err := NewToolJournal(turnFixtureSession, turnFixturePrompt, domain.GrokDefaultMode, "")
			if err != nil {
				t.Fatal(err)
			}
			for i, event := range events {
				fact, err := original.observe(event)
				if err != nil {
					t.Fatal(i, err)
				}
				projection, err := publicToolEvent(event, fact)
				if err != nil {
					t.Fatalf("projection %d: %v; original=%s", i, err, event.Params)
				}
				raw, err := json.Marshal(projection)
				if err != nil {
					t.Fatal(err)
				}
				var retained domain.GrokToolEvent
				if domain.Decode(raw, &retained) != nil {
					t.Fatal("public decode", i)
				}
				native, err := nativeToolPayload(retained.Payload)
				if err != nil {
					t.Fatal(err)
				}
				var want, got any
				decoder := json.NewDecoder(bytes.NewReader(event.Params))
				decoder.UseNumber()
				_ = decoder.Decode(&want)
				decoder = json.NewDecoder(bytes.NewReader(native))
				decoder.UseNumber()
				_ = decoder.Decode(&got)
				if !reflect.DeepEqual(want, got) {
					t.Fatalf("native shape changed at %d:\n%s\n%s", i, event.Params, native)
				}
				if err := journal.Observe(retained); err != nil {
					t.Fatal("journal", i, err)
				}
				outcome := PlanOutcome(scenario)
				if scenario == "revised" {
					outcome = PlanCancelled
					if fact.Plan != nil && fact.Plan.Artifact != nil && fact.Plan.Artifact.origin.Revision == 2 {
						outcome = PlanApproved
					}
				}
				if fact.Plan != nil && fact.Plan.Interaction != nil && fact.Plan.Interaction.Stage == planApprovalResolved {
					// Fixture request claims are independently supplied by the reply tests.
					// This reducer tests preservation of the same explicit decision.
					outcome = original.plans.tools[fact.Plan.Interaction.ID].outcome
					if outcome == "" {
						outcome = PlanOutcome(scenario)
						if scenario == "revised" {
							outcome = PlanCancelled
							if original.plans.tools[fact.Plan.Interaction.ID].artifact.origin.Revision == 2 {
								outcome = PlanApproved
							}
						}
					}
					if err := journal.ObservePlanDecision(fact.Plan.Interaction.ID, outcome); err != nil {
						t.Fatal(err)
					}
				}
				if fact.Plan != nil && fact.Plan.Interaction != nil && fact.Plan.Interaction.Stage == planApprovalResolved {
					if err := original.plans.bindResponse(fact.Plan.Interaction.ID, outcome); err != nil {
						t.Fatal(err)
					}
				}
			}
			if !journal.Settled() {
				t.Fatal("original tools remained unsettled")
			}
		})
	}
}

func TestPublicNumericRequestPreservesOriginalObserverIdentity(t *testing.T) {
	for _, spelling := range []string{"0", "-0", "9223372036854775808", "-9223372036854775809", "9999999999999999999"} {
		t.Run(spelling, func(t *testing.T) {
			original, _ := newMixedTools(turnFixtureSession, turnFixturePrompt)
			journal, err := NewToolJournal(turnFixtureSession, turnFixturePrompt, domain.GrokDefaultMode, "")
			if err != nil {
				t.Fatal(err)
			}
			replaced := false
			for _, event := range mixedFixtureEvents(t) {
				if event.Kind == nativewire.ServerRequest && !replaced {
					event.ID = json.RawMessage(spelling)
					replaced = true
				}
				fact, err := original.observe(event)
				if err != nil {
					t.Fatal("original native observer", err)
				}
				projection, err := publicToolEvent(event, fact)
				if err != nil {
					t.Fatal("public projection", err)
				}
				raw, _ := json.Marshal(projection)
				var retained domain.GrokToolEvent
				if domain.Decode(raw, &retained) != nil || journal.Observe(retained) != nil {
					t.Fatal("retained original journal rejected")
				}
				if retained.RequestID != nil && retained.RequestID.Kind == domain.InteractionDecimalID {
					key, err := retained.RequestID.Key()
					digest, digestErr := PublicRequestDigest(*retained.RequestID)
					if err != nil || digestErr != nil || key != "n:"+spelling || digest != fileDigest([]byte("n:"+spelling)) {
						t.Fatal("numeric identity or native reply digest changed")
					}
				}
			}
			if !replaced || !journal.Settled() {
				t.Fatal("original native lifecycle was lost")
			}
		})
	}
}
