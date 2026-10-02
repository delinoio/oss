package grok

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

func TestPublicProposalDigestPreservesOriginalRequestBytes(t *testing.T) {
	for _, scenario := range []string{"mixed", "approved"} {
		t.Run(scenario, func(t *testing.T) {
			original, events := planningEvents(t, "approved")
			if scenario == "mixed" {
				original, _ = newMixedTools(turnFixtureSession, turnFixturePrompt)
				events = mixedFixtureEvents(t)
			}
			journal, err := NewToolJournal(turnFixtureSession, turnFixturePrompt, domain.GrokDefaultMode, "")
			if err != nil {
				t.Fatal(err)
			}
			requests := 0
			for i, event := range events {
				if event.Kind == nativewire.ServerRequest {
					// These bytes are valid native JSON but cannot be recovered by
					// serializing the typed projection back into a new JSON object.
					event.Params = append(json.RawMessage("\n\t "), event.Params...)
				}
				fact, err := original.observe(event)
				if err != nil {
					t.Fatal(i, err)
				}
				projection, err := publicToolEvent(event, fact)
				if err != nil {
					t.Fatal(i, err)
				}
				encoded, _ := json.Marshal(projection)
				var retained domain.GrokToolEvent
				if domain.Decode(encoded, &retained) != nil || journal.Observe(retained) != nil {
					t.Fatal("original proposal did not survive public retention", i)
				}
				if event.Kind == nativewire.ServerRequest {
					requests++
					if retained.ProposalJSON != string(event.Params) {
						t.Fatal("original proposal bytes changed")
					}
					request := &domain.GrokInteractionRequest{Event: retained, ProposalDigest: fileDigest(event.Params)}
					tool := ""
					if retained.Payload.Tool != nil {
						tool = *retained.Payload.Tool.ID
					} else {
						tool = *retained.Payload.ToolID
					}
					if p := fact.Plan; p != nil {
						a := p.Artifact
						request.Plan = &domain.GrokPlanProposal{Origin: domain.GrokPlanOrigin{EntryToolID: a.origin.EntryToolID, EntryEventID: a.origin.EntryEventID, Revision: a.origin.Revision}, WriteToolID: a.writeToolID, ContentDigest: fmt.Sprintf("%x", a.contentDigest)}
					}
					if journal.ValidateRequest(request, tool) != nil {
						t.Fatal("original byte digest rejected")
					}
					request.ProposalDigest = strings.Repeat("0", 64)
					if journal.ValidateRequest(request, tool) == nil {
						t.Fatal("changed proposal digest accepted")
					}
					changed := retained
					changed.ProposalJSON = strings.Replace(retained.ProposalJSON, string(retained.Payload.Session), string(domain.NewID()), 1)
					before := journal.events
					if journal.Observe(changed) == nil || journal.events != before {
						t.Fatal("foreign original bytes accepted or advanced the journal")
					}
				}
				if p := fact.Plan; p != nil && p.Interaction != nil && p.Interaction.Stage == planApprovalResolved {
					if original.plans.bindResponse(p.Interaction.ID, PlanApproved) != nil || journal.ObservePlanDecision(p.Interaction.ID, PlanApproved) != nil {
						t.Fatal("original Plan decision rejected")
					}
				}
			}
			if requests == 0 || !journal.Settled() {
				t.Fatal("original request lifecycle was not exercised")
			}
		})
	}
}
