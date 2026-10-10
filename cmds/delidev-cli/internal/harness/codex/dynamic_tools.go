// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

const dynamicNativeLimit = 4 << 20

// OriginalJSON and OriginalRequest are private original evidence, not a public
// restoration payload. Only the retained wire token authorizes a reply.
type DynamicTool struct {
	Observation     domain.CodexDynamicTool
	OriginalJSON    json.RawMessage   `json:"-"`
	OriginalRequest *nativewire.Event `json:"-"`
}
type trackedDynamicTool struct {
	observation domain.CodexDynamicTool
	native      nativewire.Event
	original    json.RawMessage
	sent        bool
	resolved    bool
}
type dynamicToolState struct {
	arrivals map[domain.ID]*trackedDynamicTool
	requests map[string]domain.ID
	calls    map[string]domain.CodexDynamicTool
	bytes    int
}

func dynamicDigest(raw []byte) string {
	value := sha256.Sum256(raw)
	return hex.EncodeToString(value[:])
}
func dynamicArguments(raw json.RawMessage) (domain.DynamicArguments, error) {
	var value any
	var checked json.RawMessage
	if len(raw) == 0 || domain.DecodeBounded(raw, &checked, 1<<20) != nil {
		return domain.DynamicArguments{}, incompatible()
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return domain.DynamicArguments{}, incompatible()
	}
	kind := domain.DynamicNull
	switch value.(type) {
	case map[string]any:
		kind = domain.DynamicObject
	case []any:
		kind = domain.DynamicArray
	case string:
		kind = domain.DynamicString
	case json.Number:
		kind = domain.DynamicNumber
	case bool:
		kind = domain.DynamicBoolean
	}
	normal, err := json.Marshal(value)
	if err != nil {
		return domain.DynamicArguments{}, incompatible()
	}
	return domain.DynamicArguments{Present: true, Type: kind, Digest: dynamicDigest(normal)}, nil
}
func requireDynamicFields(raw []byte, names ...string) error {
	var fields map[string]json.RawMessage
	if domain.DecodeBounded(raw, &fields, dynamicNativeLimit) != nil || len(fields) != len(names) {
		return incompatible()
	}
	for _, name := range names {
		if len(fields[name]) == 0 {
			return incompatible()
		}
	}
	return nil
}

// DecodeDynamicTool is shared by live items, full context/Fork history and
// continuation validation. Nullable fields must be present in every variant.
func DecodeDynamicTool(raw json.RawMessage, stage domain.DynamicToolStage) (DynamicTool, error) {
	var item struct {
		Type         string                   `json:"type"`
		ID           string                   `json:"id"`
		Namespace    *string                  `json:"namespace"`
		Tool         string                   `json:"tool"`
		Arguments    json.RawMessage          `json:"arguments"`
		Status       domain.DynamicToolStatus `json:"status"`
		ContentItems []json.RawMessage        `json:"contentItems"`
		Success      *bool                    `json:"success"`
		DurationMS   *int64                   `json:"durationMs"`
	}
	if requireDynamicFields(raw, "type", "id", "namespace", "tool", "arguments", "status", "contentItems", "success", "durationMs") != nil || domain.DecodeBounded(raw, &item, dynamicNativeLimit) != nil || item.Type != "dynamicToolCall" {
		return DynamicTool{}, incompatible()
	}
	if stage == "" {
		stage = domain.DynamicToolCompleted
		if item.Status == domain.DynamicInProgress {
			stage = domain.DynamicToolStarted
		}
	}
	args, err := dynamicArguments(item.Arguments)
	if err != nil {
		return DynamicTool{}, err
	}
	observation := domain.CodexDynamicTool{Version: 1, ID: domain.NewID(), Stage: stage, CallID: item.ID, Namespace: item.Namespace, Tool: item.Tool, Arguments: args, Status: &item.Status, Success: item.Success, DurationMS: item.DurationMS}
	if item.ContentItems != nil {
		observation.ContentItems = []domain.DynamicContent{}
	}
	if len(item.ContentItems) > 128 {
		return DynamicTool{}, incompatible()
	}
	for _, content := range item.ContentItems {
		var fields map[string]json.RawMessage
		var kind domain.DynamicContentType
		if domain.DecodeBounded(content, &fields, 1<<20) != nil || len(fields) != 2 || json.Unmarshal(fields["type"], &kind) != nil {
			return DynamicTool{}, incompatible()
		}
		projected := domain.DynamicContent{Type: kind}
		switch kind {
		case domain.DynamicText:
			var text string
			if len(fields["text"]) == 0 || bytes.Equal(fields["text"], []byte("null")) || json.Unmarshal(fields["text"], &text) != nil {
				return DynamicTool{}, incompatible()
			}
			projected.Text = &text
		case domain.DynamicImage, domain.DynamicAudio:
			field := "imageUrl"
			projected.ReferenceKind = "image_url"
			if kind == domain.DynamicAudio {
				field = "audioUrl"
				projected.ReferenceKind = "audio_url"
			}
			var reference string
			if len(fields[field]) == 0 || bytes.Equal(fields[field], []byte("null")) || json.Unmarshal(fields[field], &reference) != nil || domain.Text(reference, "private dynamic reference", 1<<20, false) != nil {
				return DynamicTool{}, incompatible()
			}
			present := reference != ""
			projected.ReferencePresent = &present
			projected.ReferenceDigest = dynamicDigest([]byte(reference))
		default:
			return DynamicTool{}, incompatible()
		}
		observation.ContentItems = append(observation.ContentItems, projected)
	}
	if observation.Validate() != nil {
		return DynamicTool{}, incompatible()
	}
	return DynamicTool{Observation: observation, OriginalJSON: bytes.Clone(raw)}, nil
}
func cloneDynamicObservation(value domain.CodexDynamicTool) domain.CodexDynamicTool {
	raw, _ := json.Marshal(value)
	var copy domain.CodexDynamicTool
	_ = json.Unmarshal(raw, &copy)
	return copy
}
func dynamicDescriptorEqual(a, b domain.CodexDynamicTool) bool {
	return a.CallID == b.CallID && a.Tool == b.Tool && reflect.DeepEqual(a.Namespace, b.Namespace) && a.Arguments == b.Arguments
}
func (c *Client) dynamicState() *dynamicToolState {
	s := &c.execution.dynamicTools
	if s.arrivals == nil {
		s.arrivals = map[domain.ID]*trackedDynamicTool{}
		s.requests = map[string]domain.ID{}
		s.calls = map[string]domain.CodexDynamicTool{}
	}
	return s
}
func (c *Client) bindDynamicDescriptor(turn domain.ID, value domain.CodexDynamicTool) error {
	s := c.dynamicState()
	key := string(turn) + "\x00" + value.CallID
	if existing, ok := s.calls[key]; ok {
		if !dynamicDescriptorEqual(existing, value) {
			return incompatible()
		}
		return nil
	}
	raw, _ := json.Marshal(value)
	if len(s.calls) >= 4096 || s.bytes+len(raw) > 8<<20 {
		return incompatible()
	}
	s.bytes += len(raw)
	s.calls[key] = cloneDynamicObservation(value)
	return nil
}
func (c *Client) observeDynamicItem(native nativewire.Event, turnID domain.ID, raw json.RawMessage) (Event, error) {
	stage := domain.DynamicToolStarted
	if native.Method == "item/completed" {
		stage = domain.DynamicToolCompleted
	}
	tool, err := DecodeDynamicTool(raw, stage)
	if err != nil {
		return Event{}, err
	}
	turn, known := c.execution.turns[turnID]
	if !known || c.bindDynamicDescriptor(turnID, tool.Observation) != nil {
		return Event{}, incompatible()
	}
	return Event{Kind: DynamicToolObservedEvent, ThreadID: c.thread, TurnID: turnID, ItemID: tool.Observation.CallID, DynamicTool: &tool, Correlated: true, Late: turn.Turn.Status.terminal()}, nil
}
func (c *Client) observeDynamicRequestLocked(native nativewire.Event) (Event, error) {
	// Auxiliary profiles have no original request authority, even for a valid
	// root-looking request. This does not widen their ordinary tool policy.
	if c.mode != ThreadProtocol || c.sidechat != "" || c.modelObservation != "" || c.api != nil && c.api.title {
		return Event{}, unsupportedSettings()
	}
	var params struct {
		ThreadID  domain.ID       `json:"threadId"`
		TurnID    domain.ID       `json:"turnId"`
		CallID    string          `json:"callId"`
		Namespace *string         `json:"namespace"`
		Tool      string          `json:"tool"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if requireDynamicFields(native.Params, "threadId", "turnId", "callId", "namespace", "tool", "arguments") != nil || domain.DecodeBounded(native.Params, &params, dynamicNativeLimit) != nil || params.ThreadID.Validate() != nil || params.TurnID.Validate() != nil || native.Token.Validate() != nil {
		return Event{}, incompatible()
	}
	if params.ThreadID != c.thread {
		return Event{}, incompatible()
	}
	turn, known := c.execution.turns[params.TurnID]
	if !known || turn.Turn.Status.terminal() || c.execution.active != params.TurnID || c.execution.paused || c.execution.interrupt != "" {
		return Event{}, interactionConflict()
	}
	requestID, err := decodeNativeRequestID(native.ID)
	if err != nil {
		return Event{}, err
	}
	publicID := domain.InteractionRequestID{Kind: domain.InteractionTextID, Text: requestID.Text}
	if requestID.Kind == NumberRequestID {
		publicID = domain.InteractionRequestID{Kind: domain.InteractionNumberID, Number: requestID.Number}
	}
	args, err := dynamicArguments(params.Arguments)
	if err != nil {
		return Event{}, err
	}
	resolved := false
	value := domain.CodexDynamicTool{Version: 1, ID: domain.NewID(), Stage: domain.DynamicToolRequested, CallID: params.CallID, Namespace: params.Namespace, Tool: params.Tool, Arguments: args, ArrivalID: native.Token, RequestID: &publicID, Delivery: domain.DynamicNotSent, RequestResolved: &resolved}
	if value.Validate() != nil || c.bindDynamicDescriptor(params.TurnID, value) != nil {
		return Event{}, incompatible()
	}
	state := c.dynamicState()
	key := requestKey(requestID)
	if state.requests[key] != "" || state.arrivals[native.Token] != nil {
		return Event{}, interactionUncertain()
	}
	// One original call can have only one native request, regardless of whether
	// a replay invents a different native request id or arrival token.
	for _, owned := range state.arrivals {
		var owner struct {
			TurnID domain.ID `json:"turnId"`
		}
		_ = json.Unmarshal(owned.original, &owner)
		if owner.TurnID == params.TurnID && owned.observation.CallID == params.CallID {
			return Event{}, interactionUncertain()
		}
	}
	if len(state.arrivals) >= 4096 || state.bytes+len(native.Params) > 8<<20 {
		return Event{}, incompatible()
	}
	state.requests[key] = native.Token
	state.bytes += len(native.Params)
	native.ID = bytes.Clone(native.ID)
	native.Params = bytes.Clone(native.Params)
	state.arrivals[native.Token] = &trackedDynamicTool{observation: cloneDynamicObservation(value), native: native, original: bytes.Clone(native.Params)}
	returnedNative := native
	returnedNative.ID = bytes.Clone(native.ID)
	returnedNative.Params = bytes.Clone(native.Params)
	return Event{Kind: DynamicToolRequestedEvent, ThreadID: c.thread, TurnID: params.TurnID, ItemID: params.CallID, DynamicTool: &DynamicTool{Observation: value, OriginalJSON: bytes.Clone(native.Params), OriginalRequest: &returnedNative}, Correlated: true}, nil
}
func (c *Client) resolveDynamicRequest(native nativewire.Event, id NativeRequestID) (Event, bool, error) {
	state := c.dynamicState()
	arrival := state.requests[requestKey(id)]
	owned := state.arrivals[arrival]
	if owned == nil {
		return Event{}, false, nil
	}
	var params struct {
		TurnID domain.ID `json:"turnId"`
	}
	_ = json.Unmarshal(owned.original, &params)
	owned.resolved = true
	value := cloneDynamicObservation(owned.observation)
	value.ID = domain.NewID()
	value.Stage = domain.DynamicToolResolved
	resolved := true
	value.RequestResolved = &resolved
	owned.observation = cloneDynamicObservation(value)
	return Event{Kind: DynamicToolObservedEvent, ThreadID: c.thread, TurnID: params.TurnID, ItemID: value.CallID, DynamicTool: &DynamicTool{Observation: value, OriginalJSON: bytes.Clone(native.Params)}, Correlated: true}, true, nil
}

// RejectDynamicTool replies only through the original live native event. The
// caller must first synchronize its exclusive private send-intent journal.
func (c *Client) RejectDynamicTool(ctx context.Context, arrival, response, turnID domain.ID) (DynamicTool, error) {
	if arrival.Validate() != nil || response.Validate() != nil || turnID.Validate() != nil {
		return DynamicTool{}, interactionConflict()
	}
	if err := c.acquireControl(ctx); err != nil {
		return DynamicTool{}, err
	}
	defer func() { <-c.control }()
	if c.mode != ThreadProtocol || c.execution == nil || c.sidechat != "" || c.modelObservation != "" || c.api != nil && c.api.title {
		return DynamicTool{}, unsupportedSettings()
	}
	owned := c.dynamicState().arrivals[arrival]
	var params struct {
		TurnID domain.ID `json:"turnId"`
	}
	if owned != nil {
		_ = json.Unmarshal(owned.original, &params)
	}
	if owned == nil || owned.sent || owned.resolved || params.TurnID != turnID || c.execution.active != turnID || c.execution.paused || c.execution.interrupt != "" || c.problem != nil {
		return DynamicTool{}, interactionConflict()
	}
	if ctx.Err() != nil {
		return DynamicTool{}, domain.SafeError(ctx.Err())
	}
	owned.sent = true
	value := cloneDynamicObservation(owned.observation)
	value.ID = domain.NewID()
	value.Stage = domain.DynamicToolReplied
	value.ResponseID = response
	value.Delivery = domain.DynamicUncertain
	negative := false
	value.NegativeOutcome = &negative
	// Fixed native negative response; no registry lookup or handler execution.
	reply := struct {
		Success      bool `json:"success"`
		ContentItems []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"contentItems"`
	}{ContentItems: []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}{{Type: "inputText", Text: domain.DynamicUnavailableText}}}
	err := c.wire.Reply(ctx, owned.native, reply)
	if err == nil {
		value.Delivery = domain.DynamicTransmitted
	} else {
		c.execution.paused = true
		c.problem = interactionUncertain()
	}
	owned.observation = cloneDynamicObservation(value)
	if c.logger != nil {
		c.logger.InfoContext(ctx, "Codex dynamic negative reply delivery observed", "owner_id", c.ownerID, "arrival_id", arrival, "response_id", response, "delivery", value.Delivery)
	}
	return DynamicTool{Observation: value}, err
}

// InspectDynamicTool reports the original live attempt without granting a new
// arrival or resend. Recovery can inspect this state but cannot restore a wire
// capability from a private journal or public projection.
func (c *Client) InspectDynamicTool(ctx context.Context, arrival domain.ID) (Event, error) {
	if arrival.Validate() != nil {
		return Event{}, interactionConflict()
	}
	if err := c.acquireControl(ctx); err != nil {
		return Event{}, err
	}
	defer func() { <-c.control }()
	if c.execution == nil {
		return Event{}, interactionConflict()
	}
	owned := c.dynamicState().arrivals[arrival]
	if owned == nil {
		return Event{}, interactionConflict()
	}
	var params struct {
		TurnID domain.ID `json:"turnId"`
	}
	_ = json.Unmarshal(owned.original, &params)
	native := owned.native
	native.ID = bytes.Clone(native.ID)
	native.Params = bytes.Clone(native.Params)
	value := cloneDynamicObservation(owned.observation)
	return Event{Kind: DynamicToolRequestedEvent, ThreadID: c.thread, TurnID: params.TurnID, ItemID: value.CallID, DynamicTool: &DynamicTool{Observation: value, OriginalJSON: bytes.Clone(owned.original), OriginalRequest: &native}, Correlated: true}, nil
}
