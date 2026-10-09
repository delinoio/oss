package domain

import (
	"bytes"
	"encoding/json"
	"slices"
)

type ClaudeInteractionKind string
type ClaudeRuleBehavior string
type ClaudeUpdateKind string
type ClaudeUpdateDestination string

const (
	ClaudeToolPermission    ClaudeInteractionKind   = "tool-permission"
	ClaudeUserQuestion      ClaudeInteractionKind   = "user-question"
	ClaudePlanApproval      ClaudeInteractionKind   = "plan-approval"
	ClaudeRuleAllow         ClaudeRuleBehavior      = "allow"
	ClaudeRuleDeny          ClaudeRuleBehavior      = "deny"
	ClaudeRuleAsk           ClaudeRuleBehavior      = "ask"
	ClaudeAddRules          ClaudeUpdateKind        = "addRules"
	ClaudeReplaceRules      ClaudeUpdateKind        = "replaceRules"
	ClaudeRemoveRules       ClaudeUpdateKind        = "removeRules"
	ClaudeSetMode           ClaudeUpdateKind        = "setMode"
	ClaudeAddDirectories    ClaudeUpdateKind        = "addDirectories"
	ClaudeRemoveDirectories ClaudeUpdateKind        = "removeDirectories"
	ClaudeUserSettings      ClaudeUpdateDestination = "userSettings"
	ClaudeProjectSettings   ClaudeUpdateDestination = "projectSettings"
	ClaudeLocalSettings     ClaudeUpdateDestination = "localSettings"
	ClaudeSessionSettings   ClaudeUpdateDestination = "session"
)

type ClaudePermissionRule struct {
	Tool    string  `json:"toolName"`
	Content *string `json:"ruleContent,omitempty"`
}

// Suggestions describe the original native callback. Publication never applies
// them, persists native settings or grants an approval-response capability.
type ClaudePermissionUpdate struct {
	Kind        ClaudeUpdateKind        `json:"type"`
	Rules       []ClaudePermissionRule  `json:"rules,omitempty"`
	Behavior    ClaudeRuleBehavior      `json:"behavior,omitempty"`
	Mode        ClaudePermissionMode    `json:"mode,omitempty"`
	Directories []string                `json:"directories,omitempty"`
	Destination ClaudeUpdateDestination `json:"destination,omitempty"`
}

func (p ClaudePermissionUpdate) Validate() error {
	if p.Destination != "" && !slices.Contains([]ClaudeUpdateDestination{ClaudeUserSettings, ClaudeProjectSettings, ClaudeLocalSettings, ClaudeSessionSettings}, p.Destination) {
		return invalidInteraction()
	}
	switch p.Kind {
	case ClaudeAddRules, ClaudeReplaceRules, ClaudeRemoveRules:
		if len(p.Rules) == 0 || len(p.Rules) > 128 || p.Mode != "" || p.Directories != nil || !slices.Contains([]ClaudeRuleBehavior{ClaudeRuleAllow, ClaudeRuleDeny, ClaudeRuleAsk}, p.Behavior) {
			return invalidInteraction()
		}
		for _, r := range p.Rules {
			if Text(r.Tool, "native permission tool", 256, true) != nil || r.Content != nil && Text(*r.Content, "native permission rule", 4096, false) != nil {
				return invalidInteraction()
			}
		}
	case ClaudeSetMode:
		if !p.Mode.Valid() || p.Rules != nil || p.Behavior != "" || p.Directories != nil {
			return invalidInteraction()
		}
	case ClaudeAddDirectories, ClaudeRemoveDirectories:
		if len(p.Directories) == 0 || len(p.Directories) > 128 || p.Rules != nil || p.Behavior != "" || p.Mode != "" {
			return invalidInteraction()
		}
		seen := map[string]bool{}
		for _, v := range p.Directories {
			if Text(v, "native permission directory", 4096, true) != nil || seen[v] {
				return invalidInteraction()
			}
			seen[v] = true
		}
	default:
		return invalidInteraction()
	}
	return nil
}

type ClaudeQuestion struct {
	Text     string           `json:"question"`
	Header   string           `json:"header"`
	Options  []QuestionOption `json:"options"`
	Multiple *bool            `json:"multiSelect"`
}
type ClaudeCallbackMetadata struct {
	Suggestions             []ClaudePermissionUpdate `json:"permission_suggestions"`
	BlockedPath             *string                  `json:"blocked_path"`
	DecisionReason          *string                  `json:"decision_reason"`
	DecisionReasonType      *string                  `json:"decision_reason_type"`
	RequiresUserInteraction *bool                    `json:"requires_user_interaction"`
	AgentID                 *string                  `json:"agent_id"`
	Title                   *string                  `json:"title"`
	DisplayName             *string                  `json:"display_name"`
	Description             *string                  `json:"description"`
}

type ClaudeInteractionRequest struct {
	Version         string                 `json:"version"`
	Kind            ClaudeInteractionKind  `json:"kind"`
	ArrivalID       ID                     `json:"arrival_id"`
	Tool            ClaudeToolReference    `json:"tool"`
	MessageID       ID                     `json:"message_id"`
	NativeMessageID string                 `json:"native_message_id"`
	Index           uint32                 `json:"index"`
	Caller          *ClaudeToolCallerKind  `json:"caller"`
	InputJSON       string                 `json:"input_json"`
	Metadata        ClaudeCallbackMetadata `json:"metadata"`
}

type ClaudeInteractionCancellation struct {
	ArrivalID ID `json:"arrival_id"`
}

// Question text is the original native answer key; do not invent question UUIDs,
// secret flags, blocking defaults, automatic resolution or custom-choice policy.
func (r ClaudeInteractionRequest) Questions() ([]ClaudeQuestion, error) {
	var input struct {
		Questions []ClaudeQuestion `json:"questions"`
		Metadata  json.RawMessage  `json:"metadata,omitempty"`
	}
	if Decode([]byte(r.InputJSON), &input) != nil || len(input.Questions) == 0 || len(input.Questions) > 4 {
		return nil, invalidInteraction()
	}
	seen := map[string]bool{}
	for _, q := range input.Questions {
		if Text(q.Text, "native question", 4096, true) != nil || Text(q.Header, "native question header", 256, true) != nil || q.Multiple == nil || len(q.Options) < 2 || len(q.Options) > 4 || seen[q.Text] {
			return nil, invalidInteraction()
		}
		seen[q.Text] = true
		options := map[string]bool{}
		for _, o := range q.Options {
			if Text(o.Label, "native option", 1024, true) != nil || Text(o.Description, "native option description", 4096, false) != nil || options[o.Label] {
				return nil, invalidInteraction()
			}
			options[o.Label] = true
		}
	}
	return input.Questions, nil
}
func (r ClaudeInteractionRequest) Validate(kind InteractionType, request InteractionRequestID, item string) error {
	if (r.Version != "" && !ValidNativeVersionMetadata(r.Version)) || r.ArrivalID.Validate() != nil || r.Tool.Validate() != nil || r.Tool.NativeID != item || r.MessageID.Validate() != nil || r.MessageID == r.Tool.ID || Text(r.NativeMessageID, "native provider message", 1024, true) != nil || r.NativeMessageID == item || r.Index >= 1024 || r.Caller != nil && *r.Caller != ClaudeDirectToolCaller || request.Kind != InteractionTextID || request.Number != nil || !validClaudeToolJSON(r.InputJSON) || len(r.Metadata.Suggestions) > 128 {
		return invalidInteraction()
	}
	if _, err := request.Key(); err != nil {
		return err
	}
	for _, value := range []*string{r.Metadata.BlockedPath, r.Metadata.DecisionReason, r.Metadata.DecisionReasonType, r.Metadata.AgentID, r.Metadata.Title, r.Metadata.DisplayName, r.Metadata.Description} {
		if value != nil && Text(*value, "native callback metadata", 4096, false) != nil {
			return invalidInteraction()
		}
	}
	for _, p := range r.Metadata.Suggestions {
		if p.Validate() != nil {
			return invalidInteraction()
		}
	}
	switch r.Kind {
	case ClaudeToolPermission:
		if kind != NativeApprovalInteraction || r.Tool.Name == "AskUserQuestion" || r.Tool.Name == "ExitPlanMode" {
			return invalidInteraction()
		}
	case ClaudeUserQuestion:
		if kind != UserQuestionInteraction || r.Tool.Name != "AskUserQuestion" {
			return invalidInteraction()
		}
		if _, err := r.Questions(); err != nil {
			return err
		}
	case ClaudePlanApproval:
		if kind != NativeApprovalInteraction || r.Tool.Name != "ExitPlanMode" {
			return invalidInteraction()
		}
		var fields map[string]json.RawMessage
		if Decode([]byte(r.InputJSON), &fields) != nil {
			return invalidInteraction()
		}
		var plan *string
		if Decode(fields["plan"], &plan) != nil || plan == nil || Text(*plan, "native plan", MaxMessageText, true) != nil {
			return invalidInteraction()
		}
		if raw, ok := fields["planFilePath"]; ok {
			var path *string
			if Decode(raw, &path) != nil || path == nil || Text(*path, "native plan path", 4096, true) != nil {
				return invalidInteraction()
			}
		}
	default:
		return invalidInteraction()
	}
	raw, err := json.Marshal(r)
	if err != nil || len(raw) > 512<<10 {
		return invalidInteraction()
	}
	return nil
}

// Native callback serialization can reorder object keys. Match the same original
// values without changing any number spelling, then retain callback bytes apart
// from the independently published native applied input.
func EqualClaudeToolInput(a, b string) bool {
	if !validClaudeToolJSON(a) || !validClaudeToolJSON(b) {
		return false
	}
	canonical := func(value string) []byte {
		var v any
		d := json.NewDecoder(bytes.NewBufferString(value))
		d.UseNumber()
		if d.Decode(&v) != nil {
			return nil
		}
		raw, _ := json.Marshal(v)
		return raw
	}
	return bytes.Equal(canonical(a), canonical(b))
}
