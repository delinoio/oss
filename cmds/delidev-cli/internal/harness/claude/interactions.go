package claude

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type InteractionKind string
type InteractionEventKind string
type PermissionBehavior string
type PermissionUpdateKind string
type PermissionDestination string

const (
	ToolPermission         InteractionKind       = "tool-permission"
	UserQuestion           InteractionKind       = "user-question"
	PlanApproval           InteractionKind       = "plan-approval"
	InteractionRequested   InteractionEventKind  = "requested"
	InteractionCanceled    InteractionEventKind  = "canceled"
	InteractionReplyEchoed InteractionEventKind  = "reply-echoed"
	PermissionAllow        PermissionBehavior    = "allow"
	PermissionDeny         PermissionBehavior    = "deny"
	PermissionAsk          PermissionBehavior    = "ask"
	AddRules               PermissionUpdateKind  = "addRules"
	ReplaceRules           PermissionUpdateKind  = "replaceRules"
	RemoveRules            PermissionUpdateKind  = "removeRules"
	SetMode                PermissionUpdateKind  = "setMode"
	AddDirectories         PermissionUpdateKind  = "addDirectories"
	RemoveDirectories      PermissionUpdateKind  = "removeDirectories"
	UserSettings           PermissionDestination = "userSettings"
	ProjectSettings        PermissionDestination = "projectSettings"
	LocalSettings          PermissionDestination = "localSettings"
	SessionSettings        PermissionDestination = "session"
)

type PermissionRule struct {
	Tool    string  `json:"toolName"`
	Content *string `json:"ruleContent,omitempty"`
}
type PermissionUpdate struct {
	Kind        PermissionUpdateKind  `json:"type"`
	Rules       []PermissionRule      `json:"rules,omitempty"`
	Behavior    PermissionBehavior    `json:"behavior,omitempty"`
	Mode        NativePermission      `json:"mode,omitempty"`
	Directories []string              `json:"directories,omitempty"`
	Destination PermissionDestination `json:"destination,omitempty"`
}

func (p *PermissionUpdate) UnmarshalJSON(raw []byte) error {
	type plain PermissionUpdate
	var value plain
	if decodeNativeObject(raw, &value) != nil {
		return lifecycleUncertain()
	}
	if value.Destination != "" && !slices.Contains([]PermissionDestination{UserSettings, ProjectSettings, LocalSettings, SessionSettings}, value.Destination) {
		return lifecycleUncertain()
	}
	switch value.Kind {
	case AddRules, ReplaceRules, RemoveRules:
		if len(value.Rules) == 0 || len(value.Rules) > 128 || value.Mode != "" || value.Directories != nil || !slices.Contains([]PermissionBehavior{PermissionAllow, PermissionDeny, PermissionAsk}, value.Behavior) {
			return lifecycleUncertain()
		}
		for _, rule := range value.Rules {
			if domain.Text(rule.Tool, "native permission tool", 256, true) != nil || (rule.Content != nil && domain.Text(*rule.Content, "native permission rule", 4096, false) != nil) {
				return lifecycleUncertain()
			}
		}
	case SetMode:
		if !validNativePermission(value.Mode) || value.Rules != nil || value.Behavior != "" || value.Directories != nil {
			return lifecycleUncertain()
		}
	case AddDirectories, RemoveDirectories:
		if len(value.Directories) == 0 || !uniqueText(value.Directories, 128, 4096) || value.Rules != nil || value.Behavior != "" || value.Mode != "" {
			return lifecycleUncertain()
		}
	default:
		return lifecycleUncertain()
	}
	*p = PermissionUpdate(value)
	return nil
}

type NativeQuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}
type NativeQuestion struct {
	Question    string                 `json:"question"`
	Header      string                 `json:"header"`
	Options     []NativeQuestionOption `json:"options"`
	MultiSelect *bool                  `json:"multiSelect"`
}

// Callback input and metadata remain private until the product-specific
// question/approval adapter grants a bounded public representation. Suggestions
// are original native data, not permission to persist settings or grant access.
type NativeInteraction struct {
	DecisionReasonType      *string            `json:"-"`
	RequiresUserInteraction *bool              `json:"-"`
	Kind                    InteractionKind    `json:"-"`
	ArrivalID               domain.ID          `json:"-"`
	RequestID               string             `json:"-"`
	ToolID                  string             `json:"-"`
	ToolName                string             `json:"-"`
	ParentToolID            string             `json:"-"`
	Input                   json.RawMessage    `json:"-"`
	Suggestions             []PermissionUpdate `json:"-"`
	BlockedPath             *string            `json:"-"`
	DecisionReason          *string            `json:"-"`
	AgentID                 *string            `json:"-"`
	Title                   *string            `json:"-"`
	DisplayName             *string            `json:"-"`
	Description             *string            `json:"-"`
	Questions               []NativeQuestion   `json:"-"`
	Plan                    *string            `json:"-"`
	PlanPath                *string            `json:"-"`
}
type InteractionObservation struct {
	Kind      InteractionEventKind
	Request   *NativeInteraction `json:"-"`
	ArrivalID domain.ID
	Canceled  bool
	// Exact native echo proves only the original reply was echoed. It does not
	// prove tool success, plan execution or acceptance of an input turn.
}
type interactionState struct {
	request                    NativeInteraction
	event                      StreamEvent
	reply                      [sha256.Size]byte
	prepared, echoed, canceled bool
	retainedBytes              int
	questions                  map[string]bool
}

func (b *ExecutionBinding) observeInteraction(event StreamEvent) (*InteractionObservation, error) {
	if event.ArrivalID.Validate() != nil || domain.Text(event.RequestID, "native request identity", 128, true) != nil {
		return nil, lifecycleUncertain()
	}
	if b.interactions == nil {
		b.interactions = map[domain.ID]*interactionState{}
	}
	retained := b.interactions[event.ArrivalID]
	if event.Kind != NativeRequest {
		if retained == nil || retained.request.RequestID != event.RequestID {
			return nil, lifecycleUncertain()
		}
		result := &InteractionObservation{ArrivalID: event.ArrivalID, Canceled: retained.canceled}
		switch event.Kind {
		case NativeCancellation:
			if retained.canceled {
				return nil, lifecycleUncertain()
			}
			retained.canceled = true
			b.releaseInteractionInput(retained)
			result.Canceled = true
			result.Kind = InteractionCanceled
		case NativeReplyEcho:
			digest, err := streamReplyDigest(event.Response.Result)
			if !retained.prepared || retained.echoed || event.Response.Failed || err != nil || digest != retained.reply {
				return nil, lifecycleUncertain()
			}
			retained.echoed = true
			result.Kind = InteractionReplyEchoed
		default:
			return nil, lifecycleUncertain()
		}
		return result, nil
	}
	if !b.initialized || !b.accepted || retained != nil || len(b.interactions) >= maxStreamIdentities {
		return nil, lifecycleUncertain()
	}
	var request struct {
		DecisionReasonType      *string            `json:"decision_reason_type"`
		Subtype                 string             `json:"subtype"`
		RequiresUserInteraction *bool              `json:"requires_user_interaction"`
		Tool                    string             `json:"tool_name"`
		ID                      string             `json:"tool_use_id"`
		Input                   json.RawMessage    `json:"input"`
		Suggestions             []PermissionUpdate `json:"permission_suggestions"`
		BlockedPath             *string            `json:"blocked_path"`
		DecisionReason          *string            `json:"decision_reason"`
		AgentID                 *string            `json:"agent_id"`
		Title                   *string            `json:"title"`
		DisplayName             *string            `json:"display_name"`
		Description             *string            `json:"description"`
	}
	if decodeNativeObject(event.Body, &request) != nil || request.Subtype != "can_use_tool" || len(request.Input) > domain.MaxMessageText || len(request.Suggestions) > 128 {
		return nil, lifecycleUncertain()
	}
	for _, text := range []*string{request.DecisionReasonType, request.BlockedPath, request.DecisionReason, request.AgentID, request.Title, request.DisplayName, request.Description} {
		if text != nil && domain.Text(*text, "native permission metadata", 4096, false) != nil {
			return nil, lifecycleUncertain()
		}
	}
	var input map[string]json.RawMessage
	if domain.Decode(request.Input, &input) != nil || input == nil {
		return nil, lifecycleUncertain()
	}
	tool, exists := b.content.tools[request.ID]
	digest, err := streamReplyDigest(request.Input)
	if !exists || tool.finished || tool.name != request.Tool || err != nil || digest != tool.input || (b.finished && !b.continuing && !b.activeChildTask(tool.parent)) {
		return nil, lifecycleUncertain()
	}
	open := 0
	for _, old := range b.interactions {
		if !old.prepared && !old.canceled {
			open++
		}
		if old.request.RequestID == event.RequestID || (!old.canceled && old.request.ToolID == request.ID && !old.echoed) {
			return nil, lifecycleUncertain()
		}
	}
	if open >= maxStreamPending || 2*len(request.Input) > maxBufferedContent-b.interactionBytes {
		return nil, lifecycleUncertain()
	}
	value := NativeInteraction{DecisionReasonType: request.DecisionReasonType, RequiresUserInteraction: request.RequiresUserInteraction, Kind: ToolPermission, ArrivalID: event.ArrivalID, RequestID: event.RequestID, ToolID: request.ID, ToolName: request.Tool, ParentToolID: tool.parent, Input: bytes.Clone(request.Input), Suggestions: request.Suggestions, BlockedPath: request.BlockedPath, DecisionReason: request.DecisionReason, AgentID: request.AgentID, Title: request.Title, DisplayName: request.DisplayName, Description: request.Description}
	switch request.Tool {
	case "AskUserQuestion":
		var questions struct {
			Questions []NativeQuestion `json:"questions"`
			Answers   json.RawMessage  `json:"answers,omitempty"`
			Metadata  json.RawMessage  `json:"metadata,omitempty"`
		}
		if decodeNativeObject(request.Input, &questions) != nil || len(questions.Questions) == 0 || len(questions.Questions) > 4 || len(questions.Answers) != 0 {
			return nil, lifecycleUncertain()
		}
		seen := map[string]bool{}
		for _, q := range questions.Questions {
			if domain.Text(q.Question, "native question", 4096, true) != nil || domain.Text(q.Header, "native question header", 256, true) != nil || q.MultiSelect == nil || len(q.Options) < 2 || len(q.Options) > 4 || seen[q.Question] {
				return nil, lifecycleUncertain()
			}
			seen[q.Question] = true
			labels := map[string]bool{}
			for _, option := range q.Options {
				if domain.Text(option.Label, "native option", 1024, true) != nil || domain.Text(option.Description, "native option description", 4096, false) != nil || labels[option.Label] {
					return nil, lifecycleUncertain()
				}
				labels[option.Label] = true
			}
		}
		value.Kind = UserQuestion
		value.Questions = questions.Questions
	case "ExitPlanMode":
		// Native completion enriches the original proposal with the actual plan.
		// Read only these fields; preserve the complete input for exact replies.
		var plan, path *string
		if json.Unmarshal(input["plan"], &plan) != nil || plan == nil || domain.Text(*plan, "native plan", domain.MaxMessageText, true) != nil {
			return nil, lifecycleUncertain()
		}
		if raw, ok := input["planFilePath"]; ok {
			if json.Unmarshal(raw, &path) != nil || path == nil || domain.Text(*path, "native plan path", 4096, true) != nil {
				return nil, lifecycleUncertain()
			}
		}
		value.Kind = PlanApproval
		value.Plan = plan
		value.PlanPath = path
	}
	// Retain independent ownership: a display consumer cannot change the input
	// later used for an authorized native response.
	retainedValue := NativeInteraction{Kind: value.Kind, RequestID: value.RequestID, ToolID: value.ToolID, Input: bytes.Clone(value.Input)}
	questionKeys := map[string]bool{}
	for _, question := range value.Questions {
		questionKeys[question.Question] = true
	}
	b.interactions[event.ArrivalID] = &interactionState{request: retainedValue, questions: questionKeys, retainedBytes: 2 * len(value.Input), event: StreamEvent{Kind: NativeRequest, RequestID: event.RequestID, ArrivalID: event.ArrivalID}}
	b.interactionBytes += 2 * len(value.Input)
	return &InteractionObservation{Kind: InteractionRequested, ArrivalID: event.ArrivalID, Request: &value}, nil
}

// PermissionReply selects one original callback. General/Plan approvals keep
// the exact tool input; question answers replace only the native answers map.
// Settings changes and arbitrary edited tool inputs need separate product
// contracts and cannot be injected through this boundary.
type PermissionReply struct {
	Behavior  PermissionBehavior
	Answers   map[string]string
	Message   string
	Interrupt bool
}

func (b *ExecutionBinding) PreparePermissionReply(arrival domain.ID, reply PermissionReply) (StreamEvent, json.RawMessage, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	value := b.interactions[arrival]
	if b.problem != nil || value == nil || value.prepared || value.canceled {
		return StreamEvent{}, nil, lifecycleUncertain()
	}
	tool, exists := b.content.tools[value.request.ToolID]
	if !exists || tool.finished || (b.finished && !b.continuing && !b.activeChildTask(tool.parent)) {
		return StreamEvent{}, nil, lifecycleUncertain()
	}
	var response any
	switch reply.Behavior {
	case PermissionAllow:
		if reply.Message != "" || reply.Interrupt {
			return StreamEvent{}, nil, lifecycleUncertain()
		}
		input := bytes.Clone(value.request.Input)
		if value.request.Kind == UserQuestion {
			if reply.Answers == nil || len(reply.Answers) > len(value.questions) {
				return StreamEvent{}, nil, lifecycleUncertain()
			}
			for question, answer := range reply.Answers {
				if !value.questions[question] || domain.Text(answer, "native answer", domain.MaxMessageText, false) != nil {
					return StreamEvent{}, nil, lifecycleUncertain()
				}
			}
			var fields map[string]json.RawMessage
			if domain.Decode(input, &fields) != nil {
				return StreamEvent{}, nil, lifecycleUncertain()
			}
			fields["answers"], _ = json.Marshal(reply.Answers)
			input, _ = json.Marshal(fields)
		} else if reply.Answers != nil {
			return StreamEvent{}, nil, lifecycleUncertain()
		}
		response = struct {
			Behavior PermissionBehavior `json:"behavior"`
			Input    json.RawMessage    `json:"updatedInput"`
		}{PermissionAllow, input}
	case PermissionDeny:
		if reply.Answers != nil || domain.Text(reply.Message, "native denial", 4096, true) != nil {
			return StreamEvent{}, nil, lifecycleUncertain()
		}
		response = struct {
			Behavior  PermissionBehavior `json:"behavior"`
			Message   string             `json:"message"`
			Interrupt bool               `json:"interrupt,omitempty"`
		}{PermissionDeny, reply.Message, reply.Interrupt}
	default:
		return StreamEvent{}, nil, lifecycleUncertain()
	}
	raw, err := streamJSON(response)
	if err != nil {
		return StreamEvent{}, nil, err
	}
	digest, err := streamReplyDigest(raw)
	if err != nil {
		return StreamEvent{}, nil, err
	}
	b.releaseInteractionInput(value)
	value.prepared = true
	value.reply = digest
	if b.logger != nil {
		b.logger.Info("Claude Code permission reply prepared", "owner_id", b.owner, "arrival_id", arrival, "behavior", reply.Behavior)
	}
	return value.event, raw, nil
}

func (b *ExecutionBinding) releaseInteractionInput(value *interactionState) {
	b.interactionBytes -= value.retainedBytes
	value.retainedBytes = 0
	value.request.Input = nil
	value.questions = nil
}
func (r *PermissionRule) UnmarshalJSON(raw []byte) error {
	type plain PermissionRule
	var value plain
	if decodeNativeObject(raw, &value) != nil {
		return lifecycleUncertain()
	}
	*r = PermissionRule(value)
	return nil
}
func (q *NativeQuestion) UnmarshalJSON(raw []byte) error {
	type plain NativeQuestion
	var value plain
	if decodeNativeObject(raw, &value) != nil {
		return lifecycleUncertain()
	}
	*q = NativeQuestion(value)
	return nil
}
func (q *NativeQuestionOption) UnmarshalJSON(raw []byte) error {
	type plain NativeQuestionOption
	var value plain
	if decodeNativeObject(raw, &value) != nil {
		return lifecycleUncertain()
	}
	*q = NativeQuestionOption(value)
	return nil
}
