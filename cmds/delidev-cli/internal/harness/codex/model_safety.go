// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

type ModelSafetyKind string

const (
	ModelVerificationObserved ModelSafetyKind = "verification"
	ModelBufferingObserved    ModelSafetyKind = "buffering"
	ModelModerationObserved   ModelSafetyKind = "moderation"
	ModelRerouteObserved      ModelSafetyKind = "reroute"
)

type ModelVerification string

const TrustedAccessForCyber ModelVerification = "trustedAccessForCyber"

type ModelRerouteReason string

const HighRiskCyberActivity ModelRerouteReason = "highRiskCyberActivity"

type ModelBufferingObservation struct {
	Model           string   `json:"model"`
	UseCases        []string `json:"useCases"`
	Reasons         []string `json:"reasons"`
	ShowBufferingUI *bool    `json:"showBufferingUi"`
	FasterModel     *string  `json:"fasterModel"`
}
type ModelRerouteObservation struct {
	From   string             `json:"fromModel"`
	To     string             `json:"toModel"`
	Reason ModelRerouteReason `json:"reason"`
}

// Private original-execution evidence. No field grants account, model, permission,
// input or cleanup authority. Generic serialization intentionally reveals nothing.
type ModelSafetyObservation struct {
	Kind          ModelSafetyKind            `json:"-"`
	ThreadID      domain.ID                  `json:"-"`
	TurnID        domain.ID                  `json:"-"`
	Verifications []ModelVerification        `json:"-"`
	Buffering     *ModelBufferingObservation `json:"-"`
	Moderation    json.RawMessage            `json:"-"`
	Reroute       *ModelRerouteObservation   `json:"-"`
	Late          bool                       `json:"-"`
}

func (o ModelSafetyObservation) clone() ModelSafetyObservation {
	o.Verifications = slices.Clone(o.Verifications)
	o.Moderation = slices.Clone(o.Moderation)
	if o.Buffering != nil {
		b := *o.Buffering
		b.UseCases, b.Reasons = slices.Clone(b.UseCases), slices.Clone(b.Reasons)
		b.FasterModel = copyString(b.FasterModel)
		if b.ShowBufferingUI != nil {
			v := *b.ShowBufferingUI
			b.ShowBufferingUI = &v
		}
		o.Buffering = &b
	}
	if o.Reroute != nil {
		r := *o.Reroute
		o.Reroute = &r
	}
	return o
}
func safetyText(s string) bool {
	return domain.Text(s, "private native model safety descriptor", 4096, true) == nil
}
func safetyStrings(ss []string) bool {
	if ss == nil || len(ss) > 64 {
		return false
	}
	for _, s := range ss {
		if !safetyText(s) {
			return false
		}
	}
	return true
}

// Pinned 0.162.0 schemas have closed verification/reroute enums but arbitrary
// string buffering descriptors and JsonValue moderation. Bound the JSON before
// decoding; domain.Decode rejects duplicate keys, excess nesting and trailing data.
func (c *Client) observeModelSafetyLocked(native nativewire.Event) (Event, error) {
	if len(native.Params) > 64<<10 {
		return Event{}, incompatible()
	}
	var identity struct {
		ThreadID domain.ID `json:"threadId"`
		TurnID   domain.ID `json:"turnId"`
	}
	var fields map[string]json.RawMessage
	if domain.Decode(native.Params, &fields) != nil || fields == nil ||
		domain.Decode(fields["threadId"], &identity.ThreadID) != nil || domain.Decode(fields["turnId"], &identity.TurnID) != nil ||
		identity.ThreadID.Validate() != nil || identity.TurnID.Validate() != nil {
		return Event{}, incompatible()
	}
	o := ModelSafetyObservation{ThreadID: identity.ThreadID, TurnID: identity.TurnID}
	switch native.Method {
	case "model/verification":
		var p struct {
			ThreadID      domain.ID           `json:"threadId"`
			TurnID        domain.ID           `json:"turnId"`
			Verifications []ModelVerification `json:"verifications"`
		}
		if !closedRecoveryObject(native.Params, []string{"threadId", "turnId", "verifications"}, nil, &p) || p.Verifications == nil || len(p.Verifications) > 64 {
			return Event{}, incompatible()
		}
		for _, v := range p.Verifications {
			if v != TrustedAccessForCyber {
				return Event{}, incompatible()
			}
		}
		o.Kind, o.Verifications = ModelVerificationObserved, p.Verifications
	case "model/safetyBuffering/updated":
		var p struct {
			ThreadID domain.ID `json:"threadId"`
			TurnID   domain.ID `json:"turnId"`
			ModelBufferingObservation
		}
		if !closedRecoveryObject(native.Params, []string{"threadId", "turnId", "model", "useCases", "reasons", "showBufferingUi", "fasterModel"}, nil, &p) ||
			!safetyText(p.Model) || !safetyStrings(p.UseCases) || !safetyStrings(p.Reasons) || p.ShowBufferingUI == nil || (p.FasterModel != nil && !safetyText(*p.FasterModel)) {
			return Event{}, incompatible()
		}
		o.Kind, o.Buffering = ModelBufferingObserved, &p.ModelBufferingObservation
	case "turn/moderationMetadata":
		var p struct {
			ThreadID domain.ID       `json:"threadId"`
			TurnID   domain.ID       `json:"turnId"`
			Metadata json.RawMessage `json:"metadata"`
		}
		if !closedRecoveryObject(native.Params, []string{"threadId", "turnId", "metadata"}, nil, &p) {
			return Event{}, incompatible()
		}
		o.Kind, o.Moderation = ModelModerationObserved, p.Metadata
	case "model/rerouted":
		var p struct {
			ThreadID domain.ID `json:"threadId"`
			TurnID   domain.ID `json:"turnId"`
			ModelRerouteObservation
		}
		if !closedRecoveryObject(native.Params, []string{"threadId", "turnId", "fromModel", "toModel", "reason"}, nil, &p) || !safetyText(p.From) || !safetyText(p.To) || p.Reason != HighRiskCyberActivity {
			return Event{}, incompatible()
		}
		o.Kind, o.Reroute = ModelRerouteObserved, &p.ModelRerouteObservation
	default:
		return Event{}, incompatible()
	}
	if identity.ThreadID != c.thread {
		return privateNative(native), nil
	}
	if c.execution == nil {
		return Event{}, incompatible()
	}
	turn, known := c.execution.turns[identity.TurnID]
	if !known {
		return Event{}, incompatible()
	}
	o.Late = turn.Turn.Status.terminal()
	// Never evict the original reroute or arbitrarily truncate moderation evidence.
	// Saturation requires reconciliation instead of permitting unbounded retention.
	if len(c.execution.modelSafety) >= 256 || c.execution.modelSafetyBytes+len(native.Params) > 1<<20 {
		return Event{}, incompatible()
	}
	c.execution.modelSafety = append(c.execution.modelSafety, o.clone())
	c.execution.modelSafetyBytes += len(native.Params)
	if o.Reroute != nil {
		turn.modelRerouted = true
		c.execution.turns[identity.TurnID] = turn
		// Retain evidence before failing. NextEvent's generic failure path preserves
		// this original execution and cannot publish selected-model completion.
		c.execution.paused = true
		c.problem = turnUncertain()
		return Event{}, incompatible()
	}
	event := c.metadata(ModelSafetyObserved)
	if o.Kind == ModelVerificationObserved && len(o.Verifications) == 0 {
		event.Metadata = ModelVerificationAbsent
	}
	event.TurnID, event.Late, event.ModelSafety = o.TurnID, o.Late, &o
	return event, nil
}
