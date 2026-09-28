package server

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func TestClaudeToolProgressPreservesOriginalReferencesAndAdvisoryState(t *testing.T) {
	for _, kind := range []string{"progress", "summary", "heartbeat"} {
		f, callback, sequence := claudeNamedCallbackPublicationFixture(t, domain.ClaudeToolPermission)
		before, _ := f.service.Store.Get(context.Background(), domain.MessageKind, callback.Claude.Tool.ID)
		e := f.event(domain.ExecutionClaudeProgressObserved, sequence+1)
		v := domain.ClaudeProgressObservation{Kind: domain.ClaudeToolProgress, NativeEventID: string(domain.NewID()), InputAccepted: true, Tool: &domain.ClaudeToolProgressObservation{Tool: callback.Claude.Tool, ElapsedSeconds: "0.0010"}}
		if kind == "heartbeat" {
			beat := true
			v.Tool.ParentToolID, v.Tool.NativeToolID, v.Tool.Heartbeat = &callback.Claude.Tool.NativeID, callback.Claude.Tool.NativeID+"-heartbeat-0", &beat
		}
		if kind == "summary" {
			v.Kind, v.Tool, v.ToolSummary = domain.ClaudeToolSummaryProgress, nil, &domain.ClaudeToolSummaryObservation{Summary: "Original advisory text", Tools: []domain.ClaudeToolReference{callback.Claude.Tool}}
		}
		e.ClaudeProgress = &domain.ExecutionClaudeProgress{ID: domain.NewID(), Observation: v}
		request := f.publish(t, e)
		if replay, err := f.call(request); err != nil || !replay.Msg.Replayed {
			t.Fatal("original receipt changed", err)
		}
		after, _ := f.service.Store.Get(context.Background(), domain.MessageKind, callback.Claude.Tool.ID)
		if after.Revision != before.Revision {
			t.Fatal("advisory progress changed tool")
		}
		row, _ := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
		session, err := store.Decode[domain.Session](row)
		if err != nil || session.Outcome != domain.ExecutionRunning || session.Execution.CleanupVerified || session.Execution.ClaudeTerminal != nil || session.Execution.Waiting != (domain.NativeWaiting{}) {
			t.Fatal("progress fabricated completion, approval or waiting", err)
		}
		stored, _ := f.service.Store.Get(context.Background(), domain.MessageKind, e.ClaudeProgress.ID)
		message, err := store.Decode[domain.ExecutionMessage](stored)
		if err != nil || message.ClaudeProgress.Validate() != nil || message.ClaudeProgress.Kind != v.Kind {
			t.Fatal("original observation lost", err)
		}
	}
}

func TestClaudeToolProgressRejectsForeignAndMixedReferencesAtomically(t *testing.T) {
	for _, scenario := range []string{"product", "native", "name", "early", "parent", "missing", "summary-foreign", "summary-duplicate", "mixed", "heartbeat-parent", "heartbeat-false", "heartbeat-identity", "heartbeat-collision"} {
		t.Run(scenario, func(t *testing.T) {
			f, callback, sequence := claudeNamedCallbackPublicationFixture(t, domain.ClaudeToolPermission)
			e := f.event(domain.ExecutionClaudeProgressObserved, sequence+1)
			v := domain.ClaudeProgressObservation{Kind: domain.ClaudeToolProgress, NativeEventID: string(domain.NewID()), InputAccepted: true, Tool: &domain.ClaudeToolProgressObservation{Tool: callback.Claude.Tool, ElapsedSeconds: "0"}}
			switch scenario {
			case "heartbeat-parent", "heartbeat-false", "heartbeat-identity", "heartbeat-collision":
				parent, beat := v.Tool.Tool.NativeID, true
				v.Tool.ParentToolID, v.Tool.NativeToolID, v.Tool.Heartbeat = &parent, parent+"-heartbeat-0", &beat
				if scenario == "heartbeat-collision" {
					_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.native-collision", nil, func(tx *store.Tx) (any, error) {
						id := domain.NewID()
						_, err := tx.Put(domain.MessageKind, id, 0, f.input.SessionID, "", domain.ExecutionMessage{})
						if err != nil {
							return nil, err
						}
						return nil, tx.BindExecutionMessage(f.input.SessionID, f.input.ExecutionID, id, e.NativeThreadID, e.NativeTurnID, v.Tool.NativeToolID, domain.MessageComplete)
					})
					if err != nil {
						t.Fatal(err)
					}
				}
				if scenario == "heartbeat-parent" {
					parent = "foreign"
				}
				if scenario == "heartbeat-false" {
					beat = false
				}
				if scenario == "heartbeat-identity" {
					v.Tool.NativeToolID = "foreign-heartbeat-0"
				}
			case "product":
				v.Tool.Tool.ID = domain.NewID()
			case "native":
				v.Tool.Tool.NativeID = "foreign"
			case "name":
				v.Tool.Tool.Name = "Read"
			case "early":
				v.InputAccepted = false
			case "parent":
				value := "parent"
				v.Tool.ParentToolID = &value
			case "missing":
				v.Tool.ElapsedSeconds = ""
			case "mixed":
				v.Thinking = &domain.ClaudeThinkingObservation{Tokens: "0", Delta: "0"}
			case "summary-foreign", "summary-duplicate":
				v.Kind, v.Tool, v.ToolSummary = domain.ClaudeToolSummaryProgress, nil, &domain.ClaudeToolSummaryObservation{Summary: "Original summary", Tools: []domain.ClaudeToolReference{callback.Claude.Tool, callback.Claude.Tool}}
				if scenario == "summary-foreign" {
					v.ToolSummary.Tools[1].ID = domain.NewID()
					v.ToolSummary.Tools[1].NativeID = "foreign"
				}
			}
			e.ClaudeProgress = &domain.ExecutionClaudeProgress{ID: domain.NewID(), Observation: v}
			before, _ := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			if _, err := f.call(f.requestEvent(t, e)); err == nil {
				t.Fatal("foreign progress accepted")
			}
			after, _ := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			if before.Revision != after.Revision {
				t.Fatal("invalid progress partially committed")
			}
			if _, err := f.service.Store.Get(context.Background(), domain.MessageKind, e.ClaudeProgress.ID); err == nil {
				t.Fatal("rejected progress retained")
			}
		})
	}
}
