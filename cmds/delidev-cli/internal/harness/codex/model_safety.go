// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

const ModelSafetyObserved MetadataKind = "model-safety-observed"
const maxSafetyDescriptors = 128
const maxModerationBytes = 64 << 10

type modelVerification string

const trustedAccessForCyber modelVerification = "trustedAccessForCyber"

type modelRerouteReason string

const highRiskCyberActivity modelRerouteReason = "highRiskCyberActivity"

// Retain only the latest observation of each family on its original turn.
// Unexported fields keep native descriptors out of generic serialization.
type modelSafetyState struct {
	verification []modelVerification
	buffering    *modelSafetyBuffering
	moderation   json.RawMessage
	reroute      *modelSafetyReroute
}
type modelSafetyBuffering struct {
	model             string
	useCases, reasons []string
	showUI            bool
	fasterModel       json.RawMessage
}
type modelSafetyReroute struct {
	from, to string
	reason   modelRerouteReason
}

func safetyDescriptors(values []string) bool {
	if values == nil || len(values) > maxSafetyDescriptors {
		return false
	}
	for _, value := range values {
		if domain.Text(value, "native safety descriptor", 1024, false) != nil {
			return false
		}
	}
	return true
}

// Native safety metadata cannot change account, model, permission or input
// authority. Foreign and terminal observations keep their existing fences.
func (c *Client) observeModelSafetyLocked(native nativewire.Event) (Event, error) {
	// Decode each family separately: a field belonging to another family is
	// still an unknown field, even though all families share the same scope.
	var thread, turnID domain.ID
	var verification []modelVerification
	var buffering *modelSafetyBuffering
	var moderation json.RawMessage
	var reroute *modelSafetyReroute
	switch native.Method {
	case "model/verification":
		var p struct {
			ThreadID      domain.ID           `json:"threadId"`
			TurnID        domain.ID           `json:"turnId"`
			Verifications []modelVerification `json:"verifications"`
		}
		if domain.Decode(native.Params, &p) != nil || p.Verifications == nil || len(p.Verifications) > maxSafetyDescriptors {
			return Event{}, incompatible()
		}
		for _, v := range p.Verifications {
			if v != trustedAccessForCyber {
				return Event{}, incompatible()
			}
		}
		thread, turnID, verification = p.ThreadID, p.TurnID, p.Verifications
	case "model/safetyBuffering/updated":
		// The installed catalog requires nullable fasterModel; pinned main also
		// permits omission. Keep both original representations without selecting it.
		var p struct {
			ThreadID    domain.ID       `json:"threadId"`
			TurnID      domain.ID       `json:"turnId"`
			Model       *string         `json:"model"`
			UseCases    []string        `json:"useCases"`
			Reasons     []string        `json:"reasons"`
			ShowUI      *bool           `json:"showBufferingUi"`
			FasterModel json.RawMessage `json:"fasterModel"`
		}
		if domain.Decode(native.Params, &p) != nil || p.Model == nil || p.ShowUI == nil || domain.Text(*p.Model, "native safety model", 1024, false) != nil || !safetyDescriptors(p.UseCases) || !safetyDescriptors(p.Reasons) {
			return Event{}, incompatible()
		}
		if len(p.FasterModel) != 0 && string(p.FasterModel) != "null" {
			var model string
			if json.Unmarshal(p.FasterModel, &model) != nil || domain.Text(model, "native faster model", 1024, false) != nil {
				return Event{}, incompatible()
			}
		}
		thread, turnID = p.ThreadID, p.TurnID
		buffering = &modelSafetyBuffering{model: *p.Model, useCases: p.UseCases, reasons: p.Reasons, showUI: *p.ShowUI, fasterModel: append(json.RawMessage(nil), p.FasterModel...)}
	case "turn/moderationMetadata":
		var p struct {
			ThreadID domain.ID       `json:"threadId"`
			TurnID   domain.ID       `json:"turnId"`
			Metadata json.RawMessage `json:"metadata"`
		}
		if domain.Decode(native.Params, &p) != nil || len(p.Metadata) == 0 || len(p.Metadata) > maxModerationBytes {
			return Event{}, incompatible()
		}
		thread, turnID, moderation = p.ThreadID, p.TurnID, append(json.RawMessage(nil), p.Metadata...)
	case "model/rerouted":
		var p struct {
			ThreadID domain.ID           `json:"threadId"`
			TurnID   domain.ID           `json:"turnId"`
			From     *string             `json:"fromModel"`
			To       *string             `json:"toModel"`
			Reason   *modelRerouteReason `json:"reason"`
		}
		if domain.Decode(native.Params, &p) != nil || p.From == nil || p.To == nil || p.Reason == nil || *p.Reason != highRiskCyberActivity || domain.Text(*p.From, "native reroute source", 1024, false) != nil || domain.Text(*p.To, "native reroute destination", 1024, false) != nil {
			return Event{}, incompatible()
		}
		thread, turnID = p.ThreadID, p.TurnID
		reroute = &modelSafetyReroute{from: *p.From, to: *p.To, reason: *p.Reason}
	default:
		return privateNative(native), nil
	}
	if thread.Validate() != nil || turnID.Validate() != nil {
		return Event{}, incompatible()
	}
	if thread != c.thread {
		return privateNative(native), nil
	}
	turn, known := c.execution.turns[turnID]
	if !known {
		return Event{}, incompatible()
	}
	if turn.safety != nil && turn.safety.reroute != nil {
		return Event{}, turnUncertain()
	}
	if turn.Turn.Status.terminal() {
		event := c.metadata(ModelSafetyObserved)
		event.TurnID, event.Late = turnID, true
		return event, nil
	}
	if turnID != c.execution.active {
		return Event{}, incompatible()
	}
	if turn.safety == nil {
		turn.safety = &modelSafetyState{}
	}
	if verification != nil {
		turn.safety.verification = verification
	}
	if buffering != nil {
		turn.safety.buffering = buffering
	}
	if moderation != nil {
		turn.safety.moderation = moderation
	}
	if reroute != nil {
		// Preserve native enforcement and original model attribution. No completion
		// or fresh send can pass until this original execution is reconciled.
		turn.safety.reroute = reroute
		c.execution.turns[turnID] = turn
		c.execution.paused, c.problem = true, turnUncertain()
		return Event{}, c.problem
	}
	c.execution.turns[turnID] = turn
	kind := ModelSafetyObserved
	if verification != nil && len(verification) == 0 {
		kind = ModelVerificationAbsent
	}
	event := c.metadata(kind)
	event.TurnID = turnID
	return event, nil
}
