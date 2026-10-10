// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

type DynamicReplyDelivery string

const (
	DynamicReplyIntent      DynamicReplyDelivery = "intent"
	DynamicReplyTransmitted DynamicReplyDelivery = "transmitted"
	DynamicReplyUncertain   DynamicReplyDelivery = "uncertain"
)

// Durable ownership metadata excludes native arguments, tool content and URLs.
type DynamicReplyState struct {
	RequestKey    string               `json:"request_key"`
	ArrivalID     domain.ID            `json:"arrival_id"`
	ThreadID      domain.ID            `json:"thread_id"`
	TurnID        domain.ID            `json:"turn_id"`
	CallID        string               `json:"call_id"`
	RequestDigest string               `json:"request_digest"`
	Delivery      DynamicReplyDelivery `json:"delivery"`
	Resolved      bool                 `json:"resolved"`
}
type trackedDynamicReply struct {
	state  DynamicReplyState
	native nativewire.Event
	call   *domain.DynamicToolObservation
}

const DynamicUnavailableResponded MetadataKind = "dynamic-tool-unavailable-response"
const DynamicRequestResolved MetadataKind = "dynamic-tool-request-resolved"

func decodeDynamicTool(raw json.RawMessage, completed bool) (*Tool, error) {
	var item struct {
		Type         string                       `json:"type"`
		ID           string                       `json:"id"`
		Namespace    *string                      `json:"namespace"`
		Tool         string                       `json:"tool"`
		Arguments    json.RawMessage              `json:"arguments"`
		Status       ToolStatus                   `json:"status"`
		ContentItems *[]domain.DynamicToolContent `json:"contentItems"`
		Success      *bool                        `json:"success"`
		DurationMS   *int64                       `json:"durationMs"`
	}
	if domain.Decode(raw, &item) != nil || item.Type != "dynamicToolCall" || domain.Text(item.ID, "native dynamic call", 1024, true) != nil || !slices.Contains([]ToolStatus{ToolRunning, ToolCompleted, ToolFailed}, item.Status) || completed == (item.Status == ToolRunning) {
		return nil, incompatible()
	}
	value := &domain.DynamicToolObservation{Namespace: item.Namespace, Tool: item.Tool, Arguments: slices.Clone(item.Arguments), ContentItems: item.ContentItems, Success: item.Success, DurationMS: item.DurationMS}
	status := map[ToolStatus]domain.ToolStatus{ToolRunning: domain.ToolRunning, ToolCompleted: domain.ToolCompleted, ToolFailed: domain.ToolFailed}[item.Status]
	if value.Validate(status) != nil {
		return nil, incompatible()
	}
	return &Tool{ID: item.ID, Kind: DynamicTool, Status: item.Status, Dynamic: value}, nil
}

// The recorder is provided only by the original ordinary Worker execution. It
// synchronizes immutable reply intent before this exact arrival can reach wire.
// No registry, backend lookup, handler invocation or arbitrary dispatch exists.
func (c *Client) answerDynamicUnavailableLocked(ctx context.Context, native nativewire.Event) (Event, error) {
	var params struct {
		ThreadID  domain.ID       `json:"threadId"`
		TurnID    domain.ID       `json:"turnId"`
		CallID    string          `json:"callId"`
		Namespace json.RawMessage `json:"namespace"`
		Tool      string          `json:"tool"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if native.Kind != nativewire.ServerRequest || native.Method != "item/tool/call" || native.Token.Validate() != nil || len(native.Params) > 384<<10 || domain.Decode(native.Params, &params) != nil || params.ThreadID.Validate() != nil || params.TurnID.Validate() != nil || domain.Text(params.CallID, "native dynamic call", 1024, true) != nil || len(params.Namespace) == 0 {
		return Event{}, incompatible()
	}
	var namespace *string
	if json.Unmarshal(params.Namespace, &namespace) != nil {
		return Event{}, incompatible()
	}
	value := domain.DynamicToolObservation{Namespace: namespace, Tool: params.Tool, Arguments: params.Arguments}
	if value.Validate(domain.ToolRunning) != nil {
		return Event{}, incompatible()
	}
	id, err := decodeNativeRequestID(native.ID)
	if err != nil {
		return Event{}, err
	}
	key := requestKey(id)
	if c.mode != ThreadProtocol || c.sidechat != "" || c.execution == nil || params.ThreadID != c.thread || c.dynamicRecorder == nil {
		return Event{}, incompatible()
	}
	turn, known := c.execution.turns[params.TurnID]
	if !known || turn.Turn.Status.terminal() || c.execution.active != params.TurnID || c.execution.paused || c.problem != nil {
		return Event{}, interactionConflict()
	}
	for _, prior := range c.dynamicReplies {
		if prior.state.TurnID == params.TurnID && prior.state.CallID == params.CallID {
			return Event{}, interactionConflict()
		}
	}
	if c.dynamicReplies[key] != nil || len(c.dynamicReplies) >= maxTrackedInteractions {
		return Event{}, interactionConflict()
	}
	if c.dynamicItems != nil {
		if original := c.dynamicItems[params.CallID]; original != nil && !domain.SameDynamicToolCall(original, &value) {
			return Event{}, incompatible()
		}
	}
	if ctx.Err() != nil {
		return Event{}, domain.SafeError(ctx.Err())
	}
	if err := c.wire.Err(); err != nil {
		return Event{}, err
	}
	digest := sha256.Sum256(native.Params)
	state := DynamicReplyState{RequestKey: key, ArrivalID: native.Token, ThreadID: c.thread, TurnID: params.TurnID, CallID: params.CallID, RequestDigest: hex.EncodeToString(digest[:]), Delivery: DynamicReplyIntent}
	if c.dynamicReplies == nil {
		c.dynamicReplies = map[string]*trackedDynamicReply{}
	}
	owned := &trackedDynamicReply{state: state, native: native, call: domain.CloneDynamicTool(&value)}
	c.dynamicReplies[key] = owned
	if err := c.dynamicRecorder(ctx, state); err != nil {
		return Event{}, interactionUncertain()
	}
	text := "This dynamic tool is unavailable in DeliDev."
	response := struct {
		Success      bool                        `json:"success"`
		ContentItems []domain.DynamicToolContent `json:"contentItems"`
	}{false, []domain.DynamicToolContent{{Type: domain.DynamicText, Text: &text}}}
	err = c.wire.Reply(ctx, native, response)
	if err == nil {
		owned.state.Delivery = DynamicReplyTransmitted
	} else {
		owned.state.Delivery = DynamicReplyUncertain
	}
	if recordErr := c.dynamicRecorder(ctx, owned.state); recordErr != nil || err != nil {
		c.problem = interactionUncertain()
		c.execution.paused = true
		return Event{}, c.problem
	}
	if c.logger != nil {
		c.logger.InfoContext(ctx, "Codex dynamic tool unavailable reply", "delivery", owned.state.Delivery)
	}
	return Event{Kind: MetadataEvent, Metadata: DynamicUnavailableResponded, ThreadID: c.thread, TurnID: params.TurnID, ItemID: params.CallID, Correlated: true}, nil
}

func (c *Client) resolveDynamicRequestLocked(native nativewire.Event) (Event, bool, error) {
	if len(c.dynamicReplies) == 0 {
		return Event{}, false, nil
	}
	var params struct {
		ThreadID  domain.ID       `json:"threadId"`
		RequestID json.RawMessage `json:"requestId"`
	}
	if domain.Decode(native.Params, &params) != nil || params.ThreadID.Validate() != nil {
		return Event{}, true, incompatible()
	}
	if params.ThreadID != c.thread {
		return Event{}, false, nil
	}
	id, err := decodeNativeRequestID(params.RequestID)
	if err != nil {
		return Event{}, true, err
	}
	owned := c.dynamicReplies[requestKey(id)]
	if owned == nil {
		return Event{}, false, nil
	}
	if _, err := c.wire.RetireRequest(owned.native); err != nil {
		return Event{}, true, err
	}
	owned.state.Resolved = true
	return Event{Kind: MetadataEvent, Metadata: DynamicRequestResolved, ThreadID: c.thread, TurnID: owned.state.TurnID, ItemID: owned.state.CallID, Correlated: true}, true, nil
}
