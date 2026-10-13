// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

const MCPToolKind ToolKind = "mcp"

// MCPCall is a passive native observation bound to an original selected
// generation. It carries no arguments and cannot authorize a retry.
type MCPCall struct {
	Identity      domain.MCPServerIdentity
	Tool          string
	ReadOnlyHint  *bool
	DurationMS    *int64
	Error         *string
	ResultText    *string
	ContentBlocks uint32
}

// mcpCall is adapter-private lifecycle state. An unfinished call that outlives
// its turn is uncertain: the adapter exposes no operation that could replay it.
type mcpCall struct {
	identity  domain.MCPServerIdentity
	tool      string
	turn      domain.ID
	completed bool
	uncertain bool
}

const MetadataMCPStartup MetadataKind = "managed-mcp-startup-observed"
const MetadataMCPElicitationRefused MetadataKind = "managed-mcp-elicitation-refused"

func (c *Client) observeMCPToolLocked(native nativewire.Event, turnID domain.ID, raw json.RawMessage, completed bool) (Event, error) {
	var item struct {
		Type         string          `json:"type"`
		ID           string          `json:"id"`
		Server       string          `json:"server"`
		Tool         string          `json:"tool"`
		Status       string          `json:"status"`
		Arguments    json.RawMessage `json:"arguments"`
		Result       json.RawMessage `json:"result"`
		Error        json.RawMessage `json:"error"`
		DurationMS   *int64          `json:"durationMs"`
		ReadOnlyHint *bool           `json:"readOnlyHint"`
		PluginID     *string         `json:"pluginId"`
	}
	if json.Unmarshal(raw, &item) != nil || item.Type != "mcpToolCall" || domain.Text(item.ID, "native MCP call identity", 1024, true) != nil || domain.Text(item.Server, "native MCP server", 1024, true) != nil || domain.Text(item.Tool, "native MCP tool", 1024, true) != nil {
		return Event{}, incompatible()
	}
	binding, selected := c.mcpBinding(item.Server)
	if !selected {
		// Built-in connectors and every foreign server retain their private boundary.
		return privateNative(native), nil
	}
	if item.PluginID != nil || len(c.managedMCP) == 0 || item.DurationMS != nil && *item.DurationMS < 0 {
		return Event{}, incompatible()
	}
	if a := c.codeReview; a != nil && a.childTurn != "" && turnID == a.childTurn {
		turnID = a.turn
	}
	turn, known := c.execution.turns[turnID]
	if !known && c.problem == nil {
		return Event{}, incompatible()
	}
	tool := &Tool{ID: item.ID, Kind: MCPToolKind, MCP: &MCPCall{Identity: binding.server.Identity, Tool: item.Tool, ReadOnlyHint: item.ReadOnlyHint, DurationMS: item.DurationMS}}
	switch item.Status {
	case "inProgress":
		tool.Status = ToolRunning
	case "completed":
		tool.Status = ToolCompleted
	case "failed":
		tool.Status = ToolFailed
	default:
		return Event{}, incompatible()
	}
	c.mcpMu.Lock()
	if c.mcpCalls == nil {
		c.mcpCalls = map[string]*mcpCall{}
	}
	prior := c.mcpCalls[item.ID]
	if !completed {
		if prior != nil || tool.Status != ToolRunning || len(c.mcpCalls) >= maxTrackedMCPCalls {
			c.mcpMu.Unlock()
			return Event{}, incompatible()
		}
		c.mcpCalls[item.ID] = &mcpCall{identity: binding.server.Identity, tool: item.Tool, turn: turnID}
	} else {
		if prior == nil || prior.completed || prior.identity != binding.server.Identity || prior.tool != item.Tool || prior.turn != turnID || tool.Status == ToolRunning {
			c.mcpMu.Unlock()
			return Event{}, incompatible()
		}
		if err := projectMCPResult(tool.MCP, item.Result, item.Error, tool.Status); err != nil {
			c.mcpMu.Unlock()
			return Event{}, err
		}
		prior.completed = true
	}
	c.mcpMu.Unlock()
	eventKind := ToolStartedEvent
	if completed {
		eventKind = ToolCompletedEvent
	}
	if c.logger != nil {
		c.logger.InfoContext(c.logContext(), "Codex managed MCP call observed", "owner_id", c.ownerID, "definition_id", binding.server.Identity.DefinitionID, "revision", binding.server.Identity.Revision, "status", tool.Status)
	}
	return Event{Kind: eventKind, ThreadID: c.thread, TurnID: turnID, ItemID: tool.ID, Tool: tool, Correlated: known, Late: turn.Turn.Status.terminal()}, nil
}

const maxTrackedMCPCalls = 4096

func (c *Client) logContext() context.Context { return context.Background() }

func projectMCPResult(call *MCPCall, result, failure json.RawMessage, status ToolStatus) error {
	if status == ToolFailed {
		message := "The MCP call failed."
		var native *struct {
			Message string `json:"message"`
		}
		if len(failure) != 0 && string(failure) != "null" {
			if json.Unmarshal(failure, &native) != nil || native == nil {
				return incompatible()
			}
			if domain.Text(native.Message, "native MCP error", 16<<10, false) == nil && strings.TrimSpace(native.Message) != "" {
				message = native.Message
			}
		}
		call.Error = &message
	} else if len(failure) != 0 && string(failure) != "null" {
		return incompatible()
	}
	if len(result) == 0 || string(result) == "null" {
		return nil
	}
	var value struct {
		Content []json.RawMessage `json:"content"`
	}
	if json.Unmarshal(result, &value) != nil || len(value.Content) > 4096 {
		return incompatible()
	}
	call.ContentBlocks = uint32(len(value.Content))
	var text []string
	total := 0
	for _, block := range value.Content {
		var content struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(block, &content) != nil {
			return incompatible()
		}
		if content.Type == "text" {
			total += len(content.Text) + 1
			text = append(text, content.Text)
		}
	}
	// Oversized or ill-formed text is represented only by the block count. It is
	// never silently shortened into a different result.
	if total > 0 && total <= 64<<10 {
		joined := strings.Join(text, "\n")
		if domain.Text(joined, "native MCP result", 64<<10, false) == nil {
			call.ResultText = &joined
		}
	}
	return nil
}

// markUncertainMCPCallsLocked records unfinished calls whose turn ended. Their
// native effect cannot be known, so no later operation may replay them.
func (c *Client) markUncertainMCPCallsLocked(turn domain.ID) {
	c.mcpMu.Lock()
	defer c.mcpMu.Unlock()
	for _, call := range c.mcpCalls {
		if call.turn == turn && !call.completed && !call.uncertain {
			call.uncertain = true
			if c.logger != nil {
				c.logger.WarnContext(c.logContext(), "Codex managed MCP call outcome uncertain", "owner_id", c.ownerID, "definition_id", call.identity.DefinitionID, "revision", call.identity.Revision)
			}
		}
	}
}

// UncertainMCPCalls lists unfinished calls that were not replayed. The result
// has no native arguments and is evidence for operator reconciliation only.
func (c *Client) UncertainMCPCalls() []domain.MCPServerIdentity {
	c.mcpMu.Lock()
	defer c.mcpMu.Unlock()
	// A call still unfinished after the native connection failed has an unknown effect.
	lost := c.wire != nil && c.wire.Err() != nil
	var result []domain.MCPServerIdentity
	for _, call := range c.mcpCalls {
		if call.uncertain || lost && !call.completed {
			result = append(result, call.identity)
		}
	}
	return result
}

// observeMCPStartupLocked binds one native startup notification to an exact
// selected generation. A server outside the frozen selection is native drift.
func (c *Client) observeMCPStartupLocked(name string, status nativeMCPStartupState, reason *nativeMCPFailureReason) (Event, bool, error) {
	if len(c.managedMCP) == 0 {
		return Event{}, false, nil
	}
	binding, selected := c.mcpBinding(name)
	if !selected {
		if name == "codex_apps" && c.managedHome != "" {
			return Event{}, false, nil
		}
		return Event{}, true, incompatible()
	}
	state := mcpStartupState(status, reason)
	c.setMCPOutcome(name, state)
	event := c.metadata(MetadataMCPStartup)
	event.MCPStartup = &MCPServerOutcome{Identity: binding.server.Identity, State: state}
	return event, true, nil
}

type elicitationKind string

const (
	elicitationString  elicitationKind = "string"
	elicitationNumber  elicitationKind = "number"
	elicitationInteger elicitationKind = "integer"
	elicitationBoolean elicitationKind = "boolean"
	elicitationEnum    elicitationKind = "enum"
	elicitationMulti   elicitationKind = "multi"
)

const elicitationActionQuestion = "action"

type elicitationField struct {
	Key        string
	QuestionID string
	Kind       elicitationKind
	Required   bool
	Options    []string
	Minimum    *float64
	Maximum    *float64
	MinLength  *uint64
	MaxLength  *uint64
	MinItems   *uint64
	MaxItems   *uint64
}

// elicitationRequest is the closed form subset the product can answer. It keeps
// the original server identity and message separate from user-entered content.
type elicitationRequest struct {
	Identity domain.MCPServerIdentity
	Fields   []elicitationField
}

type nativeElicitationResponse struct {
	Action  string         `json:"action"`
	Content map[string]any `json:"content"`
}

type schemaConst struct {
	Const string `json:"const"`
	Title string `json:"title"`
}

type elicitationProperty struct {
	Type        string        `json:"type"`
	Title       *string       `json:"title"`
	Description *string       `json:"description"`
	Enum        []string      `json:"enum"`
	OneOf       []schemaConst `json:"oneOf"`
	Items       *struct {
		Enum  []string      `json:"enum"`
		AnyOf []schemaConst `json:"anyOf"`
	} `json:"items"`
	Minimum   *float64        `json:"minimum"`
	Maximum   *float64        `json:"maximum"`
	MinLength *uint64         `json:"minLength"`
	MaxLength *uint64         `json:"maxLength"`
	MinItems  *uint64         `json:"minItems"`
	MaxItems  *uint64         `json:"maxItems"`
	Format    *string         `json:"format"`
	Default   json.RawMessage `json:"default"`
}

func constLabels(values []schemaConst) []string {
	labels := make([]string, 0, len(values))
	for _, v := range values {
		labels = append(labels, v.Const)
	}
	return labels
}

func uniqueOptions(values []string) bool {
	if len(values) == 0 || len(values) > 128 {
		return false
	}
	seen := map[string]bool{}
	for _, v := range values {
		if domain.Text(v, "MCP option", 4096, true) != nil || seen[v] {
			return false
		}
		seen[v] = true
	}
	return true
}

// observeElicitationLocked turns a native form request into the existing
// once-only user-question interaction. It never answers on the user's behalf
// except for refusals of shapes this profile cannot present.
func (c *Client) observeElicitationLocked(native nativewire.Event) (Event, error) {
	var params struct {
		ThreadID      domain.ID       `json:"threadId"`
		TurnID        *domain.ID      `json:"turnId"`
		ServerName    string          `json:"serverName"`
		Mode          string          `json:"mode"`
		Message       string          `json:"message"`
		Schema        json.RawMessage `json:"requestedSchema"`
		URL           *string         `json:"url"`
		ElicitationID *string         `json:"elicitationId"`
		Meta          json.RawMessage `json:"_meta"`
	}
	if json.Unmarshal(native.Params, &params) != nil || params.ThreadID.Validate() != nil || domain.Text(params.ServerName, "native MCP server", 1024, true) != nil || domain.Text(params.Message, "native MCP message", domain.MaxMessageText, false) != nil || native.Token.Validate() != nil || (params.TurnID != nil && params.TurnID.Validate() != nil) {
		return Event{}, incompatible()
	}
	if params.ThreadID != c.thread {
		return privateNative(native), nil
	}
	binding, selected := c.mcpBinding(params.ServerName)
	if !selected {
		// An unselected or built-in server cannot receive a product answer.
		return privateNative(native), nil
	}
	nativeID, err := decodeNativeRequestID(native.ID)
	if err != nil {
		return Event{}, err
	}
	turnID := c.execution.active
	if params.TurnID != nil {
		turnID = *params.TurnID
	}
	form, supported := decodeElicitationSchema(params.Mode, params.Schema)
	turn, known := c.execution.turns[turnID]
	if !supported || turnID == "" || !known && c.problem == nil {
		if err := c.refuseElicitationLocked(native, binding); err != nil {
			// An unconfirmed cancel can leave the native request pending with no
			// retained interaction, so the failure must pause the execution instead
			// of reporting a refusal that was never delivered.
			return Event{}, err
		}
		event := c.metadata(MetadataMCPElicitationRefused)
		event.MCPStartup = &MCPServerOutcome{Identity: binding.server.Identity, State: c.mcpOutcome(binding.name)}
		return event, nil
	}
	form.Identity = binding.server.Identity
	questions := buildElicitationQuestions(binding.name, params.Message, form)
	itemID := "mcp-elicitation/" + requestKey(nativeID)
	request := &QuestionRequest{Blocking: true, Questions: questions}
	if domain.Text(itemID, "native elicitation item", 1024, true) != nil {
		return Event{}, incompatible()
	}
	interaction := &Interaction{ID: native.Token, NativeID: nativeID, Kind: UserInputInteraction, Questions: request, elicitation: form}
	if err := c.retainInteractionLocked(native, turnID, itemID, interaction, known && !turn.Turn.Status.terminal()); err != nil {
		return Event{}, err
	}
	return Event{Kind: InteractionRequestedEvent, ThreadID: c.thread, TurnID: turnID, ItemID: itemID, Correlated: known, Late: turn.Turn.Status.terminal(), Interaction: interaction}, nil
}

func (c *Client) refuseElicitationLocked(native nativewire.Event, binding mcpBinding) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := c.wire.Reply(ctx, native, nativeElicitationResponse{Action: "cancel"})
	if c.logger != nil {
		code := "ok"
		if err != nil {
			code = string(domain.SafeError(err).Code)
		}
		c.logger.WarnContext(ctx, "Codex managed MCP elicitation refused", "owner_id", c.ownerID, "definition_id", binding.server.Identity.DefinitionID, "revision", binding.server.Identity.Revision, "code", code)
	}
	return err
}

func decodeElicitationSchema(mode string, raw json.RawMessage) (*elicitationRequest, bool) {
	if mode != "form" {
		return nil, false
	}
	var schema struct {
		Type       string                         `json:"type"`
		Properties map[string]elicitationProperty `json:"properties"`
		Required   []string                       `json:"required"`
	}
	if json.Unmarshal(raw, &schema) != nil || schema.Type != "object" || schema.Properties == nil || len(schema.Properties) > 64 {
		return nil, false
	}
	required := map[string]bool{}
	for _, key := range schema.Required {
		if _, known := schema.Properties[key]; !known {
			return nil, false
		}
		required[key] = true
	}
	keys := make([]string, 0, len(schema.Properties))
	for key := range schema.Properties {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	form := &elicitationRequest{}
	for _, key := range keys {
		prop := schema.Properties[key]
		questionID := "field:" + key
		if domain.Text(key, "MCP field", 100, true) != nil || strings.ContainsAny(key, "\n\r") || len(questionID) > 128 {
			return nil, false
		}
		field := elicitationField{Key: key, QuestionID: questionID, Required: required[key], Minimum: prop.Minimum, Maximum: prop.Maximum, MinLength: prop.MinLength, MaxLength: prop.MaxLength, MinItems: prop.MinItems, MaxItems: prop.MaxItems}
		switch prop.Type {
		case "boolean":
			field.Kind, field.Options = elicitationBoolean, []string{"true", "false"}
		case "number":
			field.Kind = elicitationNumber
		case "integer":
			field.Kind = elicitationInteger
		case "string":
			switch {
			case len(prop.Enum) > 0:
				field.Kind, field.Options = elicitationEnum, prop.Enum
			case len(prop.OneOf) > 0:
				field.Kind, field.Options = elicitationEnum, constLabels(prop.OneOf)
			default:
				field.Kind = elicitationString
			}
		case "array":
			if prop.Items == nil {
				return nil, false
			}
			field.Kind = elicitationMulti
			if len(prop.Items.Enum) > 0 {
				field.Options = prop.Items.Enum
			} else {
				field.Options = constLabels(prop.Items.AnyOf)
			}
		default:
			return nil, false
		}
		if field.Kind == elicitationEnum || field.Kind == elicitationMulti {
			if !uniqueOptions(field.Options) {
				return nil, false
			}
		}
		form.Fields = append(form.Fields, field)
	}
	return form, true
}

func buildElicitationQuestions(server, message string, form *elicitationRequest) []Question {
	questions := []Question{{
		ID: elicitationActionQuestion, Header: "MCP request from " + server, Text: message,
		Options: []QuestionOption{
			{Label: "accept", Description: "Provide the requested values to this MCP server."},
			{Label: "decline", Description: "Refuse this request. The server receives no values and the action is not performed."},
			{Label: "cancel", Description: "Dismiss this request without answering."},
		},
	}}
	for _, field := range form.Fields {
		q := Question{ID: field.QuestionID, Header: field.Key, Text: field.Key}
		switch field.Kind {
		case elicitationBoolean, elicitationEnum, elicitationMulti:
			for _, option := range field.Options {
				q.Options = append(q.Options, QuestionOption{Label: option})
			}
		default:
			q.Other = true
		}
		if field.Required {
			q.Text += " (required)"
		}
		questions = append(questions, q)
	}
	return questions
}

// validateElicitationAnswers maps the owner's answers to the exact native form
// response. Decline and cancel carry no content, so a refused write has no
// value that could be misread as consent.
func validateElicitationAnswers(request *elicitationRequest, answers QuestionAnswers) (nativeElicitationResponse, error) {
	invalid := func() (nativeElicitationResponse, error) {
		return nativeElicitationResponse{}, domain.Fail(domain.InvalidArgument, "The answer does not match the original MCP request.", "Answer accept, decline or cancel and provide each offered field only when accepting.")
	}
	if request == nil || len(answers.Answers) != len(request.Fields)+1 {
		return invalid()
	}
	action := answers.Answers[elicitationActionQuestion]
	if len(action) != 1 || !slices.Contains([]string{"accept", "decline", "cancel"}, action[0]) {
		return invalid()
	}
	if action[0] != "accept" {
		for _, field := range request.Fields {
			if len(answers.Answers[field.QuestionID]) != 0 {
				return invalid()
			}
		}
		return nativeElicitationResponse{Action: action[0]}, nil
	}
	content := map[string]any{}
	for _, field := range request.Fields {
		values, present := answers.Answers[field.QuestionID]
		if !present || values == nil {
			return invalid()
		}
		if len(values) == 0 {
			if field.Required {
				return invalid()
			}
			continue
		}
		value, ok := nativeElicitationValue(field, values)
		if !ok {
			return invalid()
		}
		content[field.Key] = value
	}
	return nativeElicitationResponse{Action: "accept", Content: content}, nil
}

func nativeElicitationValue(field elicitationField, values []string) (any, bool) {
	if field.Kind != elicitationMulti && len(values) != 1 {
		return nil, false
	}
	switch field.Kind {
	case elicitationString:
		length := uint64(len([]rune(values[0])))
		if field.MinLength != nil && length < *field.MinLength || field.MaxLength != nil && length > *field.MaxLength {
			return nil, false
		}
		return values[0], true
	case elicitationBoolean:
		switch values[0] {
		case "true":
			return true, true
		case "false":
			return false, true
		}
		return nil, false
	case elicitationEnum:
		return values[0], slices.Contains(field.Options, values[0])
	case elicitationNumber, elicitationInteger:
		number, err := strconv.ParseFloat(strings.TrimSpace(values[0]), 64)
		if err != nil || math.IsNaN(number) || math.IsInf(number, 0) || field.Minimum != nil && number < *field.Minimum || field.Maximum != nil && number > *field.Maximum {
			return nil, false
		}
		if field.Kind == elicitationInteger {
			if number != math.Trunc(number) || math.Abs(number) > 1<<53 {
				return nil, false
			}
			return int64(number), true
		}
		return number, true
	case elicitationMulti:
		count := uint64(len(values))
		if field.MinItems != nil && count < *field.MinItems || field.MaxItems != nil && count > *field.MaxItems {
			return nil, false
		}
		seen := map[string]bool{}
		for _, value := range values {
			if !slices.Contains(field.Options, value) || seen[value] {
				return nil, false
			}
			seen[value] = true
		}
		return slices.Clone(values), true
	}
	return nil, false
}
