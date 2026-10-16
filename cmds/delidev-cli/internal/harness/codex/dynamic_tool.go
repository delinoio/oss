// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strconv"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

type dynamicItem struct {
	Origin    externalToolOrigin
	Turn      domain.ID
	Identity  domain.DynamicToolIdentity
	Completed bool
	Started   bool
}
type dynamicArrival struct {
	Native            nativewire.Event
	Turn              domain.ID
	CallID            string
	Request           domain.DynamicToolRequest
	Reply             *domain.DynamicToolReply
	Claimed, Resolved bool
}

func dynamicCommitment(raw json.RawMessage) (string, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return "", incompatible()
	}
	normal, err := json.Marshal(value)
	if err != nil {
		return "", incompatible()
	}
	sum := sha256.Sum256(normal)
	return hex.EncodeToString(sum[:]), nil
}
func dynamicIdentity(tool string, namespace *string, args json.RawMessage) (domain.DynamicToolIdentity, error) {
	var fields any
	if len(args) == 0 || domain.ValidateJSONUnicodeEscapes(args) != nil || domain.DecodeBounded(args, &fields, 256<<10) != nil {
		return domain.DynamicToolIdentity{}, incompatible()
	}
	digest, err := dynamicCommitment(args)
	identity := domain.DynamicToolIdentity{Tool: tool, Namespace: namespace, ArgumentsDigest: digest}
	if err != nil || identity.Validate() != nil {
		return identity, incompatible()
	}
	return identity, nil
}
func decodeDynamicTool(raw json.RawMessage, completed bool) (string, *domain.DynamicToolSnapshot, error) {
	var fields map[string]json.RawMessage
	var item struct {
		Type       string                   `json:"type"`
		ID         string                   `json:"id"`
		Tool       string                   `json:"tool"`
		Namespace  *string                  `json:"namespace"`
		Arguments  json.RawMessage          `json:"arguments"`
		Status     domain.DynamicToolStatus `json:"status"`
		Content    []json.RawMessage        `json:"contentItems"`
		Success    *bool                    `json:"success"`
		DurationMS *int64                   `json:"durationMs"`
	}
	if len(raw) > 512<<10 || domain.ValidateJSONUnicodeEscapes(raw) != nil || domain.DecodeBounded(raw, &fields, 512<<10) != nil || len(fields) < 5 || len(fields) > 9 || domain.DecodeBounded(raw, &item, 512<<10) != nil || item.Type != "dynamicToolCall" || domain.Text(item.ID, "native dynamic call", 1024, true) != nil {
		return "", nil, incompatible()
	}
	for key := range fields {
		if key != "type" && key != "id" && key != "tool" && key != "namespace" && key != "arguments" && key != "status" && key != "contentItems" && key != "success" && key != "durationMs" {
			return "", nil, incompatible()
		}
	}
	for _, key := range []string{"type", "id", "tool", "arguments", "status"} {
		if fields[key] == nil {
			return "", nil, incompatible()
		}
	}
	identity, err := dynamicIdentity(item.Tool, item.Namespace, item.Arguments)
	if err != nil {
		return "", nil, err
	}
	result := &domain.DynamicToolSnapshot{Identity: identity, Status: item.Status, Success: item.Success, DurationMS: item.DurationMS}
	if item.Content != nil {
		content := make([]domain.DynamicToolContent, 0, len(item.Content))
		if len(item.Content) > 1024 {
			return "", nil, incompatible()
		}
		for _, rawPart := range item.Content {
			var part struct {
				Type  domain.DynamicContentKind `json:"type"`
				Text  *string                   `json:"text"`
				Image *string                   `json:"imageUrl"`
				Audio *string                   `json:"audioUrl"`
			}
			var shape map[string]json.RawMessage
			if domain.Decode(rawPart, &shape) != nil || len(shape) != 2 || domain.Decode(rawPart, &part) != nil {
				return "", nil, incompatible()
			}
			safe := domain.DynamicToolContent{Kind: part.Type}
			switch part.Type {
			case domain.DynamicText:
				if part.Text == nil || shape["text"] == nil {
					return "", nil, incompatible()
				}
				safe.Text = part.Text
			case domain.DynamicImage, domain.DynamicAudio:
				ref := part.Image
				key := "imageUrl"
				if part.Type == domain.DynamicAudio {
					ref = part.Audio
					key = "audioUrl"
				}
				if ref == nil || shape[key] == nil || domain.Text(*ref, "protected native media reference", 256<<10, false) != nil {
					return "", nil, incompatible()
				}
				safe.ReferenceDigest, err = dynamicCommitment(shape[key])
				if err != nil {
					return "", nil, err
				}
			default:
				return "", nil, incompatible()
			}
			content = append(content, safe)
		}
		result.Content = &content
	}
	if result.Validate() != nil || completed == (result.Status == "inProgress") {
		return "", nil, incompatible()
	}
	return item.ID, result, nil
}
func (c *Client) observeDynamicItemLocked(native nativewire.Event, turn domain.ID, raw json.RawMessage) (Event, error) {
	id, snapshot, err := decodeDynamicTool(raw, native.Method == "item/completed")
	if err != nil {
		return Event{}, err
	}
	return c.observeExternalToolSnapshotLocked(native, turn, id, snapshot, dynamicToolOrigin)
}

func (c *Client) observeExternalToolSnapshotLocked(native nativewire.Event, turn domain.ID, id string, snapshot *domain.DynamicToolSnapshot, origin externalToolOrigin) (Event, error) {
	owner, known := c.execution.turns[turn]
	if !known {
		return Event{}, incompatible()
	}
	if c.dynamicItems == nil {
		c.dynamicItems = map[string]dynamicItem{}
	}
	prior, exists := c.dynamicItems[id]
	completed := native.Method == "item/completed"
	if snapshot.Validate() != nil || exists && (prior.Origin != origin || prior.Turn != turn || prior.Completed || prior.Started && !completed || !reflect.DeepEqual(prior.Identity, snapshot.Identity)) || !exists && len(c.dynamicItems) >= 10000 {
		return Event{}, incompatible()
	}
	c.dynamicItems[id] = dynamicItem{Origin: origin, Turn: turn, Identity: cloneDynamicIdentity(snapshot.Identity), Completed: completed, Started: true}
	stage := domain.DynamicStarted
	if completed {
		stage = domain.DynamicCompleted
	}
	return Event{Kind: DynamicToolEvent, ThreadID: c.thread, TurnID: turn, ItemID: id, Correlated: true, Late: owner.Turn.Status.terminal(), DynamicTool: &domain.DynamicToolUpdate{NativeID: id, Stage: stage, Snapshot: snapshot}}, nil
}
func (c *Client) observeDynamicRequestLocked(native nativewire.Event) (Event, error) {
	var fields map[string]json.RawMessage
	var p struct {
		ThreadID  domain.ID       `json:"threadId"`
		TurnID    domain.ID       `json:"turnId"`
		CallID    string          `json:"callId"`
		Tool      string          `json:"tool"`
		Namespace *string         `json:"namespace"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if len(native.Params) > 512<<10 || domain.ValidateJSONUnicodeEscapes(native.Params) != nil || domain.DecodeBounded(native.Params, &fields, 512<<10) != nil || len(fields) < 5 || len(fields) > 6 || domain.DecodeBounded(native.Params, &p, 512<<10) != nil || p.ThreadID.Validate() != nil || p.TurnID.Validate() != nil || domain.Text(p.CallID, "native dynamic call", 1024, true) != nil || native.Token.Validate() != nil {
		return Event{}, incompatible()
	}
	for key := range fields {
		if key != "threadId" && key != "turnId" && key != "callId" && key != "tool" && key != "namespace" && key != "arguments" {
			return Event{}, incompatible()
		}
	}
	for _, key := range []string{"threadId", "turnId", "callId", "tool", "arguments"} {
		if fields[key] == nil {
			return Event{}, incompatible()
		}
	}
	if !c.dynamicToolsEnabled || p.ThreadID != c.thread || c.sidechat != "" || c.mode != ThreadProtocol || c.modelObservation != "" || c.api != nil && c.api.title {
		return privateNative(native), nil
	}
	identity, err := dynamicIdentity(p.Tool, p.Namespace, p.Arguments)
	if err != nil {
		return Event{}, err
	}
	wireID, err := decodeNativeRequestID(native.ID)
	if err != nil {
		return Event{}, err
	}
	if _, known := c.execution.turns[p.TurnID]; !known {
		return Event{}, incompatible()
	}
	prior, exists := c.dynamicItems[p.CallID]
	if exists && (prior.Origin != dynamicToolOrigin || prior.Completed || prior.Turn != p.TurnID || !reflect.DeepEqual(prior.Identity, identity)) || c.problem != nil || c.execution.paused || c.execution.interrupt != "" || c.execution.active != p.TurnID {
		return Event{}, incompatible()
	}
	if !exists && len(c.dynamicItems) >= 10000 {
		return Event{}, incompatible()
	}
	key := requestKey(wireID)
	if c.dynamicArrivals == nil {
		c.dynamicArrivals = map[domain.ID]*dynamicArrival{}
		c.dynamicNative = map[string]domain.ID{}
	}
	if c.dynamicNative[key] != "" || c.dynamicArrivals[native.Token] != nil || c.execution.interactions.native[key] != "" || len(c.dynamicArrivals) >= domain.MaxExecutionInteractions {
		return Event{}, incompatible()
	}
	for _, arrival := range c.dynamicArrivals {
		if arrival.CallID == p.CallID {
			return Event{}, incompatible()
		}
	}
	request := domain.DynamicToolRequest{Token: native.Token, IDKind: domain.InteractionTextID, IDValue: wireID.Text, Identity: identity}
	if wireID.Kind == NumberRequestID {
		request.IDKind = domain.InteractionNumberID
		request.IDValue = strconv.FormatInt(*wireID.Number, 10)
	}
	c.dynamicArrivals[native.Token] = &dynamicArrival{Native: nativewire.Event{Kind: nativewire.ServerRequest, ID: bytes.Clone(native.ID), Token: native.Token}, Turn: p.TurnID, CallID: p.CallID, Request: cloneDynamicRequest(request)}
	if !exists {
		if c.dynamicItems == nil {
			c.dynamicItems = map[string]dynamicItem{}
		}
		c.dynamicItems[p.CallID] = dynamicItem{Turn: p.TurnID, Identity: cloneDynamicIdentity(identity)}
	}
	c.dynamicNative[key] = native.Token
	return Event{Kind: DynamicToolEvent, ThreadID: c.thread, TurnID: p.TurnID, ItemID: p.CallID, Correlated: true, DynamicTool: &domain.DynamicToolUpdate{NativeID: p.CallID, Stage: domain.DynamicRequested, Request: &request}}, nil
}
func (c *Client) observeDynamicResolvedLocked(native nativewire.Event) (Event, bool, error) {
	var p struct {
		ThreadID  domain.ID       `json:"threadId"`
		RequestID json.RawMessage `json:"requestId"`
	}
	if domain.Decode(native.Params, &p) != nil || p.ThreadID.Validate() != nil {
		return Event{}, false, incompatible()
	}
	if p.ThreadID != c.thread {
		return Event{}, false, nil
	}
	id, err := decodeNativeRequestID(p.RequestID)
	if err != nil {
		return Event{}, false, err
	}
	token := c.dynamicNative[requestKey(id)]
	if token == "" {
		return Event{}, false, nil
	}
	owned := c.dynamicArrivals[token]
	if owned == nil || owned.Resolved {
		return Event{}, true, incompatible()
	}
	if _, err := c.wire.RetireRequest(owned.Native); err != nil {
		return Event{}, true, err
	}
	owned.Resolved = true
	turn := c.execution.turns[owned.Turn]
	return Event{Kind: DynamicToolEvent, ThreadID: c.thread, TurnID: owned.Turn, ItemID: owned.CallID, Correlated: true, Late: turn.Turn.Status.terminal(), DynamicTool: &domain.DynamicToolUpdate{NativeID: owned.CallID, Stage: domain.DynamicResolved, Token: token}}, true, nil
}

type dynamicUnavailableResponse struct {
	Success bool                     `json:"success"`
	Content []dynamicUnavailableText `json:"contentItems"`
}
type dynamicUnavailableText struct {
	Type domain.DynamicContentKind `json:"type"`
	Text string                    `json:"text"`
}

// The caller owns a durable original assignment journal. The commit callback
// runs after nativewire's atomic arrival claim and before any pipe write.
func (c *Client) ReplyDynamicUnavailable(ctx context.Context, token, turn, claim domain.ID, commit func() error) (domain.DynamicToolReply, error) {
	reply := domain.DynamicToolReply{Token: token, ClaimID: claim, Delivery: domain.DynamicNotSent, Unavailable: true}
	if token.Validate() != nil || turn.Validate() != nil || claim.Validate() != nil || commit == nil {
		return reply, incompatible()
	}
	if err := c.acquireControl(ctx); err != nil {
		return reply, err
	}
	defer func() { <-c.control }()
	owned := c.dynamicArrivals[token]
	if c.execution == nil || owned == nil || owned.Turn != turn || owned.Claimed || owned.Resolved || c.sidechat != "" || c.modelObservation != "" || c.api != nil && c.api.title || c.problem != nil || c.execution.paused || c.execution.interrupt != "" || c.execution.active != turn {
		return reply, turnConflict()
	}
	owned.Claimed = true
	owned.Reply = &reply
	produced := false
	err := c.wire.ReplyGenerated(ctx, owned.Native, func() (any, error) {
		produced = true
		if err := commit(); err != nil {
			return nil, err
		}
		return dynamicUnavailableResponse{Content: []dynamicUnavailableText{{Type: domain.DynamicText, Text: domain.DynamicToolUnavailableText}}}, nil
	})
	if err == nil {
		reply.Delivery = domain.DynamicTransmitted
	} else if produced {
		reply.Delivery = domain.DynamicUncertain
		c.execution.paused = true
	}
	owned.Reply = &reply
	if c.logger != nil {
		c.logger.InfoContext(ctx, "native_dynamic_unavailable_reply", "owner_id", c.ownerID, "turn_id", turn, "request_token", token, "claim_id", claim, "delivery", reply.Delivery)
	}
	return reply, err
}
func (c *Client) InspectDynamicReply(ctx context.Context, token domain.ID) (domain.DynamicToolReply, error) {
	if err := c.acquireControl(ctx); err != nil {
		return domain.DynamicToolReply{}, err
	}
	defer func() { <-c.control }()
	owned := c.dynamicArrivals[token]
	if owned == nil || owned.Reply == nil {
		return domain.DynamicToolReply{}, turnConflict()
	}
	return *owned.Reply, nil
}

func (c *Client) dynamicRequestsBlockInput() bool {
	for _, item := range c.dynamicItems {
		if !item.Completed {
			return true
		}
	}
	for _, owned := range c.dynamicArrivals {
		if !owned.Resolved || owned.Reply == nil || owned.Reply.Delivery != domain.DynamicTransmitted {
			return true
		}
	}
	return false
}

func cloneDynamicIdentity(i domain.DynamicToolIdentity) domain.DynamicToolIdentity {
	if i.Namespace != nil {
		value := *i.Namespace
		i.Namespace = &value
	}
	return i
}
func cloneDynamicRequest(r domain.DynamicToolRequest) domain.DynamicToolRequest {
	r.Identity = cloneDynamicIdentity(r.Identity)
	return r
}

// DynamicRequestID exposes only the original private arrival spelling to the
// Worker journal. This inspection cannot claim or send a native response.
func (c *Client) DynamicRequestID(ctx context.Context, token domain.ID) (json.RawMessage, error) {
	if err := c.acquireControl(ctx); err != nil {
		return nil, err
	}
	defer func() { <-c.control }()
	owned := c.dynamicArrivals[token]
	if owned == nil {
		return nil, turnConflict()
	}
	return bytes.Clone(owned.Native.ID), nil
}
