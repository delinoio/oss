// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

type modelSafetyKind string

const (
	safetyVerification modelSafetyKind = "verification"
	safetyBuffering    modelSafetyKind = "buffering"
	safetyModeration   modelSafetyKind = "moderation"
	safetyReroute      modelSafetyKind = "reroute"
)

type modelVerification string

const trustedAccessForCyber modelVerification = "trustedAccessForCyber"

type modelRerouteReason string

const highRiskCyberActivity modelRerouteReason = "highRiskCyberActivity"

type safetyBufferingEvidence struct {
	model             string
	useCases, reasons []string
	showUI            bool
	fasterModel       *string
}
type safetyRerouteEvidence struct {
	from, to string
	reason   modelRerouteReason
}

// ModelSafetyObservation is private execution evidence. Its opaque fields and
// JSON omission prevent native descriptors or moderation content from entering
// generic serialization. It grants no model, account or input authority.
type ModelSafetyObservation struct {
	kind          modelSafetyKind
	thread, turn  domain.ID
	verifications []modelVerification
	buffering     *safetyBufferingEvidence
	moderation    json.RawMessage
	reroute       *safetyRerouteEvidence
	canonical     json.RawMessage
}

// Format and LogValue keep opaque evidence private in diagnostic formatting.
func (o ModelSafetyObservation) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "private native safety observation")
}

func (o ModelSafetyObservation) LogValue() slog.Value {
	return slog.StringValue("private native safety observation")
}

// PassiveFor checks the original binding without exposing native content.
func (o *ModelSafetyObservation) PassiveFor(thread, turn domain.ID) bool {
	if o == nil || o.thread != thread || o.turn != turn || len(o.canonical) == 0 {
		return false
	}
	switch o.kind {
	case safetyVerification, safetyBuffering, safetyModeration:
		return true
	case safetyReroute:
		return o.reroute != nil && o.reroute.from == o.reroute.to
	default:
		return false
	}
}

// MatchesPassiveEvent binds the opaque proof to its typed adapter envelope.
func (o *ModelSafetyObservation) MatchesPassiveEvent(event Event, thread, turn domain.ID) bool {
	if !event.Correlated || event.ThreadID != thread || event.TurnID != turn || !o.PassiveFor(thread, turn) {
		return false
	}
	if o.kind == safetyVerification && len(o.verifications) == 0 {
		return event.Kind == MetadataEvent && event.Metadata == ModelVerificationAbsent
	}
	return event.Kind == ModelSafetyEvent && event.Metadata == ""
}

const (
	maxSafetyEnvelope     = 64 << 10
	maxSafetyObservations = 1024
	maxSafetyBytes        = 1 << 20
)

type modelSafetyLedger struct {
	observations []*ModelSafetyObservation
	bytes        int
	rerouted     map[domain.ID]bool
}

func safetyEvidenceBytes(o *ModelSafetyObservation) int {
	size := len(o.canonical) + len(o.moderation)
	for _, v := range o.verifications {
		size += len(v)
	}
	if b := o.buffering; b != nil {
		size += len(b.model)
		for _, v := range b.useCases {
			size += len(v)
		}
		for _, v := range b.reasons {
			size += len(v)
		}
		if b.fasterModel != nil {
			size += len(*b.fasterModel)
		}
	}
	if r := o.reroute; r != nil {
		size += len(r.from) + len(r.to) + len(r.reason)
	}
	return size
}

func cloneSafety(o *ModelSafetyObservation) *ModelSafetyObservation {
	next := *o
	next.canonical = slices.Clone(o.canonical)
	next.verifications = slices.Clone(o.verifications)
	next.moderation = slices.Clone(o.moderation)
	if o.buffering != nil {
		b := *o.buffering
		b.useCases, b.reasons = slices.Clone(b.useCases), slices.Clone(b.reasons)
		if b.fasterModel != nil {
			value := *b.fasterModel
			b.fasterModel = &value
		}
		next.buffering = &b
	}
	if o.reroute != nil {
		r := *o.reroute
		next.reroute = &r
	}
	return &next
}

// Source: openai/codex c1382380de69521303b416720a52f42d51af6248,
// app-server-protocol/schema/typescript/v2/{ModelVerificationNotification,
// ModelSafetyBufferingUpdatedNotification,TurnModerationMetadataNotification,
// ModelReroutedNotification,ModelVerification,ModelRerouteReason}.ts.
// Pinned native notification schemas use closed verification/reroute enums.
// Metadata remains JSONValue, including arrays, scalars and explicit null.
func decodeSafety(method string, raw json.RawMessage) (*ModelSafetyObservation, error) {
	var fields map[string]json.RawMessage
	if domain.DecodeBounded(raw, &fields, maxSafetyEnvelope) != nil || fields == nil {
		return nil, incompatible()
	}
	names := []string{"threadId", "turnId"}
	kind := safetyVerification
	switch method {
	case "model/verification":
		names = append(names, "verifications")
	case "model/safetyBuffering/updated":
		kind = safetyBuffering
		names = append(names, "model", "useCases", "reasons", "showBufferingUi", "fasterModel")
	case "turn/moderationMetadata":
		kind = safetyModeration
		names = append(names, "metadata")
	case "model/rerouted":
		kind = safetyReroute
		names = append(names, "fromModel", "toModel", "reason")
	default:
		return nil, incompatible()
	}
	if len(fields) != len(names) {
		return nil, incompatible()
	}
	for _, name := range names {
		if _, ok := fields[name]; !ok {
			return nil, incompatible()
		}
	}
	o := &ModelSafetyObservation{kind: kind}
	if domain.Decode(fields["threadId"], &o.thread) != nil || domain.Decode(fields["turnId"], &o.turn) != nil || o.thread.Validate() != nil || o.turn.Validate() != nil {
		return nil, incompatible()
	}
	text := func(name string, required bool) (string, error) {
		var value *string
		if domain.Decode(fields[name], &value) != nil || value == nil || domain.Text(*value, "native safety descriptor", 4096, required) != nil {
			return "", incompatible()
		}
		return *value, nil
	}
	list := func(name string) ([]string, error) {
		var values []*string
		if domain.Decode(fields[name], &values) != nil || values == nil || len(values) > 64 {
			return nil, incompatible()
		}
		result := make([]string, 0, len(values))
		for _, value := range values {
			if value == nil || domain.Text(*value, "native safety descriptor", 4096, false) != nil {
				return nil, incompatible()
			}
			result = append(result, *value)
		}
		return result, nil
	}

	switch kind {
	case safetyVerification:
		var values []string
		var err error
		if values, err = list("verifications"); err != nil {
			return nil, err
		}
		for _, value := range values {
			if modelVerification(value) != trustedAccessForCyber {
				return nil, incompatible()
			}
			o.verifications = append(o.verifications, modelVerification(value))
		}
	case safetyBuffering:
		b := &safetyBufferingEvidence{}
		var err error
		if b.model, err = text("model", true); err != nil {
			return nil, err
		}
		if b.useCases, err = list("useCases"); err != nil {
			return nil, err
		}
		if b.reasons, err = list("reasons"); err != nil {
			return nil, err
		}
		var show *bool
		if domain.Decode(fields["showBufferingUi"], &show) != nil || show == nil || domain.Decode(fields["fasterModel"], &b.fasterModel) != nil || b.fasterModel != nil && domain.Text(*b.fasterModel, "native safety model", 4096, true) != nil {
			return nil, incompatible()
		}
		b.showUI = *show
		o.buffering = b
	case safetyModeration:
		// Decode validates duplicate keys and nesting before preserving JSONValue.
		o.moderation = slices.Clone(fields["metadata"])
	case safetyReroute:
		r := &safetyRerouteEvidence{}
		var err error
		if r.from, err = text("fromModel", true); err != nil {
			return nil, err
		}
		if r.to, err = text("toModel", true); err != nil {
			return nil, err
		}
		reason, err := text("reason", true)
		if err != nil || modelRerouteReason(reason) != highRiskCyberActivity {
			return nil, incompatible()
		}
		r.reason = modelRerouteReason(reason)
		o.reroute = r
	}
	// Normalize object ordering and whitespace without rounding JSON numbers.
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return nil, incompatible()
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, incompatible()
	}
	o.canonical = canonical
	return o, nil
}

func (c *Client) observeSafetyLocked(native nativewire.Event) (Event, error) {
	o, err := decodeSafety(native.Method, native.Params)
	if err != nil {
		return Event{}, err
	}
	if o.thread != c.thread {
		return privateNative(native), nil
	}
	turn, known := c.execution.turns[o.turn]
	if !known {
		return Event{}, incompatible()
	}
	if o.reroute != nil && o.reroute.from != c.execution.settings.Model {
		return Event{}, incompatible()
	}
	// Buffering evidence is bound to the immutable selected model; fasterModel is
	// advisory and is never compared or adopted.
	if o.buffering != nil && o.buffering.model != c.execution.settings.Model {
		return Event{}, incompatible()
	}
	late := turn.Turn.Status.terminal()
	if late {
		replay := false
		for _, prior := range c.safety.observations {
			if prior.kind == o.kind && bytes.Equal(prior.canonical, o.canonical) {
				replay = true
				break
			}
		}
		if !replay {
			return Event{}, incompatible()
		}
	} else {
		if len(c.safety.observations) >= maxSafetyObservations || c.safety.bytes+safetyEvidenceBytes(o) > maxSafetyBytes {
			if changesModel(o) {
				// The reroute guard is independent of evidence retention: latch it
				// before rejecting the overflowed evidence so completion cannot
				// publish selected-model success.
				return Event{}, c.latchRerouteLocked(o.turn)
			}
			return Event{}, incompatible()
		}
		c.safety.observations = append(c.safety.observations, cloneSafety(o))
		c.safety.bytes += safetyEvidenceBytes(o)
	}
	if changesModel(o) {
		return Event{}, c.latchRerouteLocked(o.turn)
	}
	event := Event{Kind: ModelSafetyEvent, ThreadID: o.thread, TurnID: o.turn, Correlated: true, Late: late, Safety: cloneSafety(o)}
	if o.kind == safetyVerification && len(o.verifications) == 0 {
		event.Kind, event.Metadata = MetadataEvent, ModelVerificationAbsent
	}
	return event, nil
}

// changesModel reports whether the observation records an actual model change.
func changesModel(o *ModelSafetyObservation) bool {
	return o.reroute != nil && o.reroute.from != o.reroute.to
}

// latchRerouteLocked records that the turn ran on a changed model, keeps the first
// recovery problem and pauses the execution. It returns the active problem.
func (c *Client) latchRerouteLocked(turn domain.ID) error {
	if c.safety.rerouted == nil {
		c.safety.rerouted = make(map[domain.ID]bool)
	}
	c.safety.rerouted[turn] = true
	if c.problem == nil {
		c.problem = domain.Fail(domain.RecoveryRequired, "Native safety changed the execution model.", "Reconcile the original execution and independent cleanup before another input; preserve the native safety decision.")
	}
	c.execution.paused = true
	return c.problem
}

func (c *Client) safetyCompletionProblem(turn domain.ID, status TurnStatus) error {
	if status == TurnCompleted && c.safety.rerouted[turn] {
		// Retain native completion internally without asserting selected-model success.
		return c.problem
	}
	return nil
}
