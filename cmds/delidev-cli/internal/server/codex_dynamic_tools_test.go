// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"strings"
	"testing"
)

func dynamicPublicFixture(stage domain.DynamicToolStage) domain.CodexDynamicTool {
	resolved := false
	return domain.CodexDynamicTool{Version: 1, ID: domain.NewID(), Stage: stage, CallID: "original-call", Tool: "unavailable", Arguments: domain.DynamicArguments{Present: true, Type: domain.DynamicObject, Digest: strings.Repeat("a", 64)}, ArrivalID: domain.NewID(), RequestID: &domain.CodexDynamicRequestID{Kind: domain.InteractionNumberID, Value: "7"}, Delivery: domain.DynamicNotSent, RequestResolved: &resolved}
}
func TestDynamicToolReducerRejectsRebindingAndRepeatedReply(t *testing.T) {
	for _, bad := range []string{"namespace", "arguments", "request-kind", "arrival", "response", "delivery-retry", "negative-success"} {
		t.Run(bad, func(t *testing.T) {
			calls := map[string]*dynamicCallRecord{}
			requests := map[string]string{}
			arrivals := map[domain.ID]string{}
			value := dynamicPublicFixture(domain.DynamicToolRequested)
			if err := applyDynamicObservation(calls, requests, arrivals, value); err != nil {
				t.Fatal(err)
			}
			reply := value
			reply.ID = domain.NewID()
			reply.Stage = domain.DynamicToolReplied
			reply.ResponseID = domain.NewID()
			reply.Delivery = domain.DynamicSendStarted
			negative := false
			reply.NegativeOutcome = &negative
			if err := applyDynamicObservation(calls, requests, arrivals, reply); err != nil {
				t.Fatal(err)
			}
			value = reply
			value.ID = domain.NewID()
			value.Delivery = domain.DynamicTransmitted
			switch bad {
			case "namespace":
				namespace := "changed"
				value.Namespace = &namespace
			case "arguments":
				value.Arguments.Digest = strings.Repeat("b", 64)
			case "request-kind":
				value.RequestID = &domain.CodexDynamicRequestID{Kind: domain.InteractionTextID, Value: "7"}
			case "arrival":
				value.ArrivalID = domain.NewID()
			case "response":
				value.ResponseID = domain.NewID()
			case "delivery-retry":
				value.Delivery = domain.DynamicSendStarted
			case "negative-success":
				positive := true
				value.NegativeOutcome = &positive
			}
			if err := applyDynamicObservation(calls, requests, arrivals, value); err == nil {
				t.Fatal("rebound or repeated original reply accepted")
			}
		})
	}
}
func TestDynamicToolPublicationDurablyRetainsIndependentOutcomeDeliveryResolutionAndRoot(t *testing.T) {
	f := newPublicationFixture(t)
	f.registerGrant(t)
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.dynamic-capability", nil, func(tx *store.Tx) (any, error) {
		r, err := tx.Get(domain.MachineKind, f.input.MachineID)
		if err != nil {
			return nil, err
		}
		m, err := store.Decode[domain.Machine](r)
		if err != nil {
			return nil, err
		}
		m.WorkerCapabilities = append(m.WorkerCapabilities, domain.CodexDynamicToolV1)
		return tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, m)
	})
	if err != nil {
		t.Fatal(err)
	}
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	value := dynamicPublicFixture(domain.DynamicToolRequested)
	publish := func(sequence uint64, v domain.CodexDynamicTool) {
		t.Helper()
		event := f.event(domain.ExecutionCodexDynamicToolObserved, sequence)
		event.CodexDynamicTool = &v
		req := f.publish(t, event)
		if reply, err := f.call(req); err != nil || !reply.Msg.Replayed {
			t.Fatal("receipt replay substituted original observation", err)
		}
	}
	publish(3, value)
	value.ID = domain.NewID()
	value.Stage = domain.DynamicToolReplied
	value.ResponseID = domain.NewID()
	value.Delivery = domain.DynamicSendStarted
	negative := false
	value.NegativeOutcome = &negative
	publish(4, value)
	value.ID = domain.NewID()
	value.Delivery = domain.DynamicTransmitted
	publish(5, value)
	value.ID = domain.NewID()
	value.Stage = domain.DynamicToolResolved
	resolved := true
	value.RequestResolved = &resolved
	// Retain request resolution separately and publish it after root terminal.
	status := domain.DynamicFailed
	success := false
	duration := int64(12)
	originalText := "Original failed item"
	lifecycle := domain.CodexDynamicTool{Version: 1, ID: domain.NewID(), Stage: domain.DynamicToolCompleted, CallID: value.CallID, Tool: value.Tool, Namespace: value.Namespace, Arguments: value.Arguments, Status: &status, Success: &success, DurationMS: &duration, ContentItems: []domain.DynamicContent{{Type: domain.DynamicText, Text: &originalText}}}
	publish(6, lifecycle)
	event := f.event(domain.ExecutionTurnFinished, 7)
	event.Outcome = domain.ExecutionSucceeded
	f.publish(t, event)
	publish(8, value)
	_, err = f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.inspect-dynamic", nil, func(tx *store.Tx) (any, error) {
		messages, err := tx.CodexDynamicToolJournal(f.input.SessionID, f.input.ExecutionID)
		if err != nil {
			return nil, err
		}
		if len(messages) != 5 || messages[2].CodexDynamicTool.Delivery != domain.DynamicTransmitted || *messages[2].CodexDynamicTool.RequestResolved || !*messages[4].CodexDynamicTool.RequestResolved || messages[3].CodexDynamicTool.Status == nil || *messages[3].CodexDynamicTool.Status != domain.DynamicFailed {
			t.Fatal("independent original boundaries lost")
		}
		_, session, err := sessionRecord(tx, f.input.SessionID)
		if err != nil {
			return nil, err
		}
		if session.Execution.Outcome != domain.ExecutionSucceeded || session.Execution.CleanupVerified || session.Execution.CodexDynamicObservations != 5 {
			t.Fatal("tool failure fabricated root outcome or cleanup")
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
