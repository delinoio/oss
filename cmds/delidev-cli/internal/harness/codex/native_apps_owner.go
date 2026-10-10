// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
	"strings"
)

type ownedNativeAppCall struct {
	turn      domain.ID
	original  *decodedNativeAppsTool
	proof     domain.NativeAppCallProof
	name      string
	completed bool
	choice    domain.NativeAppsApprovalChoice
}

// Inventory is observed from the original loaded process while control remains
// held. It cannot create an original call owner from a connector name or result.
func (c *Client) enrichNativeAppsEventLocked(ctx context.Context, event *Event) error {
	if event.Tool != nil && event.Tool.Kind == NativeAppsTool {
		source := event.Tool.NativeApps
		if source == nil || c.nativeApps == nil || c.nativeApps.Validate() != nil || !event.Correlated || event.Late {
			return incompatible()
		}
		if c.nativeAppCalls == nil {
			c.nativeAppCalls = map[string]*ownedNativeAppCall{}
		}
		original := c.nativeAppCalls[source.ID]
		if event.Kind == ToolStartedEvent {
			if original != nil || len(c.nativeAppCalls) >= maxTrackedInteractions {
				return incompatible()
			}
			call := c.nativeAppsCaller
			if call == nil {
				call = c.wire.Call
			}
			inventory, err := readAppsInventory(ctx, c.nativeApps.Scope, c.thread, false, call)
			if err != nil || domain.AdmitNativeAppCall(*c.nativeApps, *c.nativeApps, inventory, source.AppID) != nil {
				return domain.NativeAppsUnavailable()
			}
			name := ""
			for _, app := range inventory.Discovered {
				if app.ID == source.AppID {
					name = app.Name
				}
			}
			if name == "" {
				return incompatible()
			}
			original = &ownedNativeAppCall{turn: event.TurnID, original: source, name: name, proof: domain.NativeAppCallProof{Scope: c.nativeApps.Scope, Selection: *cloneNativeApps(c.nativeApps), Inventory: inventory, NativeItemID: source.ID, AppID: source.AppID}}
			c.nativeAppCalls[source.ID] = original
		} else if event.Kind != ToolCompletedEvent || original == nil || original.completed || original.turn != event.TurnID || !sameNativeAppsIdentity(original.original, source) || original.choice == "" && !(source.Status == ToolFailed && source.result == nil && source.errorPresent) {
			return incompatible()
		}
		if event.Kind == ToolCompletedEvent {
			original.completed = true
			if original.choice == domain.NativeAppsCancel && source.Status == ToolCompleted {
				return incompatible()
			}
		}
		projected, err := source.projection(original.name)
		if err != nil {
			return err
		}
		event.Tool.Apps = projected
	}
	if event.Kind == InteractionRequestedEvent && event.Interaction != nil && event.Interaction.Kind == UserInputInteraction {
		q := event.Interaction.Questions
		marker := false
		for _, item := range q.Questions {
			if strings.HasPrefix(item.ID, "mcp_tool_call_approval_") {
				marker = true
			}
		}
		owner := c.nativeAppCalls[event.ItemID]
		if owner == nil {
			if marker {
				return incompatible()
			}
			return nil
		}
		if owner.completed || owner.turn != event.TurnID || owner.choice != "" || !event.Correlated || event.Late {
			return incompatible()
		}
		request := nativeAppsDomainQuestion(q)
		if domain.ValidateNativeAppsQuestion(owner.proof, request) != nil {
			return incompatible()
		}
		proof := owner.proof
		event.Interaction.NativeApps = &proof
	}
	return nil
}

func sameNativeAppsIdentity(a, b *decodedNativeAppsTool) bool {
	return a != nil && b != nil && a.ID == b.ID && a.Server == b.Server && a.ToolName == b.ToolName && a.AppID == b.AppID && bytes.Equal(a.Arguments, b.Arguments) && bytes.Equal(a.AppContext, b.AppContext) && bytes.Equal(a.Identity, b.Identity)
}
func nativeAppsDomainQuestion(q *QuestionRequest) *domain.QuestionRequest {
	if q == nil {
		return nil
	}
	result := &domain.QuestionRequest{Blocking: q.Blocking, AutoResolutionMS: q.AutoResolutionMS, Questions: []domain.Question{}}
	for _, source := range q.Questions {
		item := domain.Question{ID: source.ID, Header: source.Header, Text: source.Text, Other: source.Other, Secret: source.Secret}
		if source.Options != nil {
			item.Options = []domain.QuestionOption{}
		}
		for _, option := range source.Options {
			item.Options = append(item.Options, domain.QuestionOption{Label: option.Label, Description: option.Description})
		}
		result.Questions = append(result.Questions, item)
	}
	return result
}
func (c *Client) nativeAppsAnswerChoice(owned *trackedInteraction, answers QuestionAnswers) error {
	owner := c.nativeAppCalls[owned.status.ItemID]
	if owner == nil {
		return nil
	}
	if owner.completed || owner.turn != owned.status.TurnID || owner.choice != "" {
		return interactionConflict()
	}
	value := domain.ExecutionInteraction{Type: domain.UserQuestionInteraction, NativeItemID: owned.status.ItemID, NativeApps: &owner.proof, Questions: nativeAppsDomainQuestion(owned.questions)}
	// Use the original closed question answer shape; generic user input cannot
	// turn an unknown or remembered option into an App effect or cleanup choice.
	var input domain.QuestionResponseInput
	input.Answers = answers.Answers
	value.Response = &domain.QuestionResponse{Input: input}
	allow, err := domain.NativeAppsResponseAdmitsEffect(value)
	if err != nil {
		return interactionConflict()
	}
	owner.choice = domain.NativeAppsCancel
	if allow {
		owner.choice = domain.NativeAppsAllow
	}
	return nil
}

func (c *Client) observeNativeAppsToolLocked(native nativewire.Event, turnID domain.ID, raw json.RawMessage) (Event, error) {
	if c.nativeApps == nil {
		return privateNative(native), nil
	}
	completed := native.Method == "item/completed"
	source, err := decodeNativeAppsTool(raw, completed)
	if err != nil {
		return Event{}, err
	}
	turn, known := c.execution.turns[turnID]
	if !known {
		return Event{}, incompatible()
	}
	kind := ToolStartedEvent
	if completed {
		kind = ToolCompletedEvent
	}
	return Event{Kind: kind, ThreadID: c.thread, TurnID: turnID, ItemID: source.ID, Correlated: known, Late: turn.Turn.Status.terminal(), Tool: &Tool{ID: source.ID, Kind: NativeAppsTool, Status: source.Status, NativeApps: source}}, nil
}
