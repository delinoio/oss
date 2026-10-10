// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

const dynamicUnavailableText = "This dynamic tool is unavailable in DeliDev."

// Original content belongs to protected native history. Projection never reads
// media locations or treats an observed native false as a root outcome.
type DynamicToolCall struct {
	Namespace    *string
	Tool         string
	Arguments    json.RawMessage
	ContentItems []json.RawMessage
	Success      *bool
	DurationMS   *int64
}

func requiredDynamicFields(raw json.RawMessage, names ...string) bool {
	var fields map[string]json.RawMessage
	if domain.DecodeBounded(raw, &fields, domain.MaxMessageText) != nil {
		return false
	}
	for _, name := range names {
		if len(fields[name]) == 0 {
			return false
		}
	}
	return true
}
func decodeDynamicTool(raw json.RawMessage, completed bool) (*Tool, error) {
	var v struct {
		Type         string            `json:"type"`
		ID           string            `json:"id"`
		Namespace    *string           `json:"namespace"`
		Tool         string            `json:"tool"`
		Arguments    json.RawMessage   `json:"arguments"`
		Status       ToolStatus        `json:"status"`
		ContentItems []json.RawMessage `json:"contentItems"`
		Success      *bool             `json:"success"`
		DurationMS   *int64            `json:"durationMs"`
	}
	if !requiredDynamicFields(raw, "type", "id", "namespace", "tool", "arguments", "status", "contentItems", "success", "durationMs") || domain.DecodeBounded(raw, &v, domain.MaxMessageText) != nil || v.Type != "dynamicToolCall" || domain.Text(v.ID, "native dynamic call", 1024, true) != nil || !slices.Contains([]ToolStatus{ToolRunning, ToolCompleted, ToolFailed}, v.Status) || completed == (v.Status == ToolRunning) {
		return nil, incompatible()
	}
	call := &DynamicToolCall{Namespace: v.Namespace, Tool: v.Tool, Arguments: slices.Clone(v.Arguments), ContentItems: v.ContentItems, Success: v.Success, DurationMS: v.DurationMS}
	if _, err := call.Projection(v.Status); err != nil {
		return nil, incompatible()
	}
	return &Tool{ID: v.ID, Kind: DynamicTool, Status: v.Status, Dynamic: call}, nil
}
func (v DynamicToolCall) Projection(status ToolStatus) (domain.CodexDynamicObservation, error) {
	result := domain.CodexDynamicObservation{Namespace: copyString(v.Namespace), Tool: v.Tool, ArgumentsJSON: dynamicCanonical(v.Arguments), Success: v.Success, DurationMS: v.DurationMS}
	if v.ContentItems != nil {
		result.ContentItems = make([]domain.DynamicContent, 0, len(v.ContentItems))
	}
	if len(v.ContentItems) > 128 {
		return result, incompatible()
	}
	for _, raw := range v.ContentItems {
		var kind struct {
			Type domain.DynamicContentKind `json:"type"`
		}
		if json.Unmarshal(raw, &kind) != nil {
			return result, incompatible()
		}
		item := domain.DynamicContent{Kind: kind.Type}
		switch kind.Type {
		case domain.DynamicText:
			var content struct {
				Type domain.DynamicContentKind `json:"type"`
				Text *string                   `json:"text"`
			}
			if domain.DecodeBounded(raw, &content, domain.MaxMessageText) != nil || content.Text == nil {
				return result, incompatible()
			}
			item.Text = content.Text
		case domain.DynamicImage, domain.DynamicAudio:
			var fields map[string]json.RawMessage
			if domain.DecodeBounded(raw, &fields, domain.MaxMessageText) != nil || len(fields) != 2 {
				return result, incompatible()
			}
			name := "imageUrl"
			if kind.Type == domain.DynamicAudio {
				name = "audioUrl"
			}
			var source string
			if json.Unmarshal(fields[name], &source) != nil || domain.Text(source, "native dynamic media", domain.MaxMessageText, true) != nil {
				return result, incompatible()
			}
			digest := sha256.Sum256([]byte(source))
			item.Digest = hex.EncodeToString(digest[:])
		default:
			return result, incompatible()
		}
		result.ContentItems = append(result.ContentItems, item)
	}
	mapped := map[ToolStatus]domain.ToolStatus{ToolRunning: domain.ToolRunning, ToolCompleted: domain.ToolCompleted, ToolFailed: domain.ToolFailed}[status]
	if result.Validate(mapped) != nil {
		return result, incompatible()
	}
	return result, nil
}

type DynamicRequest struct {
	ID              domain.ID       `json:"id"`
	NativeID        NativeRequestID `json:"native_id"`
	ThreadID        domain.ID       `json:"thread_id"`
	TurnID          domain.ID       `json:"turn_id"`
	CallID          string          `json:"call_id"`
	Namespace       *string         `json:"namespace"`
	Tool            string          `json:"tool"`
	ArgumentsDigest string          `json:"arguments_digest"`
}

func (r DynamicRequest) Validate() error {
	for _, id := range []domain.ID{r.ID, r.ThreadID, r.TurnID} {
		if id.Validate() != nil {
			return incompatible()
		}
	}
	if domain.Text(r.CallID, "native dynamic call", 1024, true) != nil || domain.Text(r.Tool, "native dynamic tool", 1024, true) != nil || r.Namespace != nil {
		return incompatible()
	}
	digest, err := hex.DecodeString(r.ArgumentsDigest)
	if err != nil || len(digest) != sha256.Size || hex.EncodeToString(digest) != r.ArgumentsDigest {
		return incompatible()
	}
	switch r.NativeID.Kind {
	case TextRequestID:
		if r.NativeID.Number != nil || domain.Text(r.NativeID.Text, "native dynamic request", 128, true) != nil {
			return incompatible()
		}
	case NumberRequestID:
		if r.NativeID.Number == nil || r.NativeID.Text != "" {
			return incompatible()
		}
	default:
		return incompatible()
	}
	return nil
}

type DynamicReplyStage string

const (
	DynamicObserved    DynamicReplyStage = "observed"
	DynamicSendIntent  DynamicReplyStage = "send-intent"
	DynamicTransmitted DynamicReplyStage = "transmitted"
	DynamicUncertain   DynamicReplyStage = "uncertain"
)

// Intent, delivery, resolution and root completion are independent. The exact
// arrival token and typed wire ID cannot gain authority from publication replay.
type DynamicReplyState struct {
	Request DynamicRequest     `json:"request"`
	Stage   DynamicReplyStage  `json:"stage"`
	Closure InteractionClosure `json:"closure"`
}
type trackedDynamicRequest struct {
	state  DynamicReplyState
	native nativewire.Event
}

func dynamicRequestCopy(request DynamicRequest) *DynamicRequest {
	result := request
	result.Namespace = copyString(request.Namespace)
	if request.NativeID.Number != nil {
		number := *request.NativeID.Number
		result.NativeID.Number = &number
	}
	return &result
}
func dynamicStateCopy(state DynamicReplyState) DynamicReplyState {
	state.Request = *dynamicRequestCopy(state.Request)
	return state
}
func (c *Client) observeDynamicRequestLocked(native nativewire.Event) (Event, error) {
	var v struct {
		ThreadID  domain.ID       `json:"threadId"`
		TurnID    domain.ID       `json:"turnId"`
		CallID    string          `json:"callId"`
		Namespace *string         `json:"namespace"`
		Tool      string          `json:"tool"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if native.Kind != nativewire.ServerRequest || native.Method != "item/tool/call" || !requiredDynamicFields(native.Params, "threadId", "turnId", "callId", "namespace", "tool", "arguments") || domain.DecodeBounded(native.Params, &v, domain.MaxMessageText) != nil || v.ThreadID.Validate() != nil || v.TurnID.Validate() != nil || native.Token.Validate() != nil || domain.Text(v.CallID, "native dynamic call", 1024, true) != nil || domain.Text(v.Tool, "native dynamic tool", 1024, true) != nil {
		return Event{}, incompatible()
	}
	// No namespaced backend is selected by this profile. Never adopt one.
	if v.ThreadID != c.thread || v.Namespace != nil || c.sidechat != "" {
		return privateNative(native), nil
	}
	turn, known := c.execution.turns[v.TurnID]
	if !known || turn.Turn.Status.terminal() || c.execution.active != v.TurnID {
		return Event{}, incompatible()
	}
	id, err := decodeNativeRequestID(native.ID)
	if err != nil {
		return Event{}, err
	}
	key := requestKey(id)
	if c.dynamicNative[key] != "" || c.execution.interactions.native[key] != "" || c.dynamicRequests[native.Token] != nil || len(c.dynamicRequests) >= maxTrackedInteractions {
		return Event{}, incompatible()
	}
	for _, prior := range c.dynamicRequests {
		if prior.state.Request.TurnID == v.TurnID && prior.state.Request.CallID == v.CallID {
			return Event{}, incompatible()
		}
	}
	digest := sha256.Sum256([]byte(dynamicCanonical(v.Arguments)))
	request := DynamicRequest{ID: native.Token, NativeID: id, ThreadID: c.thread, TurnID: v.TurnID, CallID: v.CallID, Tool: v.Tool, ArgumentsDigest: hex.EncodeToString(digest[:])}
	if request.Validate() != nil {
		return Event{}, incompatible()
	}
	if item, exists := c.dynamicItems[string(v.TurnID)+"/"+v.CallID]; exists && (item.completed || !dynamicRequestMatches(request, item)) {
		return Event{}, incompatible()
	}
	if c.dynamicRequests == nil {
		c.dynamicRequests = map[domain.ID]*trackedDynamicRequest{}
		c.dynamicNative = map[string]domain.ID{}
	}
	c.dynamicRequests[native.Token] = &trackedDynamicRequest{state: DynamicReplyState{Request: request, Stage: DynamicObserved, Closure: InteractionOpen}, native: nativewire.Event{Kind: nativewire.ServerRequest, ID: slices.Clone(native.ID), Token: native.Token}}
	c.dynamicNative[key] = native.Token
	return Event{Kind: DynamicRequestedEvent, ThreadID: c.thread, TurnID: v.TurnID, ItemID: v.CallID, Correlated: true, DynamicRequest: dynamicRequestCopy(request)}, nil
}
func dynamicReplyUncertain() *domain.Error {
	return domain.Fail(domain.RecoveryRequired, "The original dynamic-tool reply requires reconciliation.", "Inspect its retained original attempt; never resend a claimed or uncertain reply.")
}

// The Worker callback must synchronize the original exclusive journal before
// returning. A claimed send is never retried, including a failed journal/write.
func (c *Client) ReplyDynamicUnavailable(ctx context.Context, id domain.ID, persist func(DynamicReplyState) error) (DynamicReplyState, error) {
	if persist == nil || id.Validate() != nil {
		return DynamicReplyState{}, interactionConflict()
	}
	if err := c.acquireControl(ctx); err != nil {
		return DynamicReplyState{}, err
	}
	defer func() { <-c.control }()
	owned := c.dynamicRequests[id]
	if owned == nil || c.mode != ThreadProtocol || c.sidechat != "" || c.problem != nil || c.execution.paused || c.execution.interrupt != "" || c.execution.active != owned.state.Request.TurnID || owned.state.Stage != DynamicObserved || owned.state.Closure != InteractionOpen {
		return DynamicReplyState{}, interactionConflict()
	}
	if ctx.Err() != nil {
		return dynamicStateCopy(owned.state), domain.SafeError(ctx.Err())
	}
	owned.state.Stage = DynamicSendIntent
	if err := persist(dynamicStateCopy(owned.state)); err != nil {
		c.execution.paused = true
		c.problem = dynamicReplyUncertain()
		return dynamicStateCopy(owned.state), c.problem
	}
	response := struct {
		Success      bool `json:"success"`
		ContentItems []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"contentItems"`
	}{Success: false}
	response.ContentItems = append(response.ContentItems, struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}{"inputText", dynamicUnavailableText})
	err := c.wire.Reply(ctx, owned.native, response)
	owned.state.Stage = DynamicTransmitted
	if err != nil {
		owned.state.Stage = DynamicUncertain
		c.execution.paused = true
		c.problem = dynamicReplyUncertain()
		err = c.problem
	}
	if persist(dynamicStateCopy(owned.state)) != nil {
		c.execution.paused = true
		c.problem = dynamicReplyUncertain()
		err = c.problem
	}
	if c.logger != nil {
		c.logger.InfoContext(ctx, "Codex unavailable dynamic reply observed", "owner_id", c.ownerID, "request_token", id, "stage", owned.state.Stage)
	}
	return dynamicStateCopy(owned.state), err
}
func (c *Client) InspectDynamicReply(ctx context.Context, id domain.ID) (DynamicReplyState, error) {
	if id.Validate() != nil {
		return DynamicReplyState{}, interactionConflict()
	}
	if err := c.acquireControl(ctx); err != nil {
		return DynamicReplyState{}, err
	}
	defer func() { <-c.control }()
	owned := c.dynamicRequests[id]
	if owned == nil {
		return DynamicReplyState{}, interactionConflict()
	}
	return dynamicStateCopy(owned.state), nil
}
func (c *Client) closeDynamicLocked(owned *trackedDynamicRequest, closure InteractionClosure) error {
	if owned.state.Closure != InteractionOpen {
		if closure == InteractionNativeClosed && owned.state.Closure == InteractionTurnEnded {
			owned.state.Closure = InteractionNativeClosed
		}
		return nil
	}
	retired, err := c.wire.RetireRequest(owned.native)
	if err != nil {
		return err
	}
	if !retired && owned.state.Stage == DynamicObserved {
		return incompatible()
	}
	owned.state.Closure = closure
	return nil
}
func (c *Client) dynamicResolutionLocked(native nativewire.Event, id NativeRequestID) (Event, bool, error) {
	token := c.dynamicNative[requestKey(id)]
	if token == "" {
		return Event{}, false, nil
	}
	owned := c.dynamicRequests[token]
	if err := c.closeDynamicLocked(owned, InteractionNativeClosed); err != nil {
		return Event{}, true, err
	}
	state := dynamicStateCopy(owned.state)
	turn := c.execution.turns[state.Request.TurnID]
	return Event{Kind: DynamicResolvedEvent, ThreadID: c.thread, TurnID: state.Request.TurnID, ItemID: state.Request.CallID, Correlated: true, Late: turn.Turn.Status.terminal(), DynamicReply: &state}, true, nil
}
func (c *Client) dynamicBlocksInput() bool {
	for _, owned := range c.dynamicRequests {
		if owned.state.Closure == InteractionOpen || owned.state.Stage == DynamicSendIntent || owned.state.Stage == DynamicUncertain {
			return true
		}
	}
	return false
}

// Normalize JSON object order/whitespace without converting numeric spelling.
func dynamicCanonical(raw json.RawMessage) string {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return ""
	}
	normal, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(normal)
}

type dynamicItemIdentity struct {
	tool            string
	namespace       *string
	argumentsDigest string
	completed       bool
	completedDigest string
}

func dynamicIdentityEqual(a, b dynamicItemIdentity) bool {
	return a.tool == b.tool && a.argumentsDigest == b.argumentsDigest && (a.namespace == nil) == (b.namespace == nil) && (a.namespace == nil || *a.namespace == *b.namespace)
}

func validateRetainedDynamicItem(turnIndex, inheritedTurns int, key string, tool *Tool, items map[string]dynamicItemIdentity) error {
	prior, exists := items[key]
	if !exists {
		if turnIndex >= inheritedTurns {
			return continuationUncertain()
		}
		return nil
	}
	digest := sha256.Sum256([]byte(dynamicCanonical(tool.Dynamic.Arguments)))
	identity := dynamicItemIdentity{tool: tool.Dynamic.Tool, namespace: tool.Dynamic.Namespace, argumentsDigest: hex.EncodeToString(digest[:])}
	if !prior.completed || !dynamicIdentityEqual(prior, identity) || prior.completedDigest != dynamicToolDigest(tool) {
		return continuationUncertain()
	}
	return nil
}

func dynamicRequestMatches(r DynamicRequest, item dynamicItemIdentity) bool {
	return dynamicIdentityEqual(dynamicItemIdentity{tool: r.Tool, namespace: r.Namespace, argumentsDigest: r.ArgumentsDigest}, item)
}
func dynamicToolDigest(tool *Tool) string {
	raw, err := json.Marshal(struct {
		Status ToolStatus
		Call   *DynamicToolCall
	}{tool.Status, tool.Dynamic})
	if err != nil {
		return ""
	}
	digest := sha256.Sum256([]byte(dynamicCanonical(raw)))
	return hex.EncodeToString(digest[:])
}
func (c *Client) observeDynamicIdentity(turn domain.ID, tool *Tool, completed bool) error {
	if tool.Dynamic == nil || c.sidechat != "" {
		return incompatible()
	}
	key := string(turn) + "/" + tool.ID
	digest := sha256.Sum256([]byte(dynamicCanonical(tool.Dynamic.Arguments)))
	identity := dynamicItemIdentity{tool: tool.Dynamic.Tool, namespace: copyString(tool.Dynamic.Namespace), argumentsDigest: hex.EncodeToString(digest[:]), completed: completed}
	if completed {
		identity.completedDigest = dynamicToolDigest(tool)
	}
	for _, request := range c.dynamicRequests {
		r := request.state.Request
		if r.TurnID == turn && r.CallID == tool.ID && !dynamicRequestMatches(r, identity) {
			return incompatible()
		}
	}
	prior, exists := c.dynamicItems[key]
	if completed {
		if !exists || prior.completed || !dynamicIdentityEqual(prior, identity) {
			return incompatible()
		}
	} else if exists || len(c.dynamicItems) >= maxTrackedInteractions {
		return incompatible()
	}
	if c.dynamicItems == nil {
		c.dynamicItems = map[string]dynamicItemIdentity{}
	}
	c.dynamicItems[key] = identity
	return nil
}
func hasDynamicHistory(turns []json.RawMessage) bool {
	for _, raw := range turns {
		var turn turnWire
		if domain.DecodeBounded(raw, &turn, 16<<20) != nil {
			return false
		}
		for _, rawItem := range turn.Items {
			var item struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(rawItem, &item) == nil && item.Type == "dynamicToolCall" {
				return true
			}
		}
	}
	return false
}
func (c *Client) bindDynamicHistoryBase(turns []json.RawMessage) {
	c.dynamicHistoryRequired = hasDynamicHistory(turns)
	c.dynamicHistoryBase = nil
	if c.dynamicHistoryRequired {
		c.dynamicHistoryBase = &ForkHistoryCheckpoint{TurnsCount: uint32(len(turns)), HistoryDigest: historyDigest(turns)}
	}
}

// Retain complete original history before native shutdown. These reads never
// reply, dispatch, reconstruct output or grant another execution authority.
func (c *Client) RetainDynamicHistory(ctx context.Context, source ContinuationCheckpoint) (*ForkHistoryCheckpoint, error) {
	if source.validate(ResumeAfterTerminal) != nil {
		return nil, continuationUncertain()
	}
	if err := c.acquireControl(ctx); err != nil {
		return nil, err
	}
	defer func() { <-c.control }()
	if !c.dynamicHistoryRequired && len(c.dynamicItems) == 0 {
		return nil, nil
	}
	state := c.execution
	if c.sidechat != "" || state == nil || c.problem != nil || state.active != "" || len(state.pending) != 0 || c.dynamicBlocksInput() || state.interactions.blocksInput() || len(c.subagents) != 0 || source.ThreadID != c.thread || source.SessionID != state.thread.SessionID || !sameEffectiveSettings(source.Effective, state.settings) {
		return nil, continuationUncertain()
	}
	tracked, known := state.turns[source.TurnID]
	if !known || tracked.Turn.Status != source.Status {
		return nil, continuationUncertain()
	}
	turns, err := c.compactionTurnsLocked(ctx)
	if err != nil || !hasDynamicHistory(turns) {
		return nil, continuationUncertain()
	}
	inheritedTurns := 0
	if base := c.dynamicHistoryBase; base != nil {
		if int(base.TurnsCount) > len(turns) || !base.matches(turns[:base.TurnsCount]) {
			return nil, continuationUncertain()
		}
		inheritedTurns = int(base.TurnsCount)
	}
	seenItems := map[string]bool{}
	for turnIndex, raw := range turns {
		var turn turnWire
		if domain.DecodeBounded(raw, &turn, 16<<20) != nil {
			return nil, continuationUncertain()
		}
		for _, rawItem := range turn.Items {
			var item struct {
				Type string `json:"type"`
				ID   string `json:"id"`
			}
			if json.Unmarshal(rawItem, &item) != nil {
				return nil, continuationUncertain()
			}
			if item.Type != "dynamicToolCall" {
				continue
			}
			tool, err := decodeDynamicTool(rawItem, true)
			if err != nil {
				return nil, continuationUncertain()
			}
			key := string(turn.ID) + "/" + item.ID
			seenItems[key] = true
			if err := validateRetainedDynamicItem(turnIndex, inheritedTurns, key, tool, c.dynamicItems); err != nil {
				return nil, err
			}
		}
	}
	for key, item := range c.dynamicItems {
		if !item.completed || !seenItems[key] {
			return nil, continuationUncertain()
		}
	}
	c.dynamicHistoryRequired = true
	return &ForkHistoryCheckpoint{TurnsCount: uint32(len(turns)), HistoryDigest: historyDigest(turns)}, nil
}
