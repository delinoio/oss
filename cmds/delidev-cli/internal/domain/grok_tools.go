package domain

import (
	"encoding/json"
	"strconv"
)

// GrokCount retains exact native integers in the public JSON document. Native
// reconstruction is observation-only and never acquires a response sender.
type GrokCount string

func (n *GrokCount) UnmarshalJSON(raw []byte) error {
	var value string
	if len(raw) > 0 && raw[0] == '"' {
		if json.Unmarshal(raw, &value) != nil {
			return invalidGrokContent()
		}
	} else {
		value = string(raw)
	}
	if _, err := strconv.ParseUint(value, 10, 64); err != nil || len(value) == 0 || len(value) > 1 && value[0] == '0' {
		return invalidGrokContent()
	}
	*n = GrokCount(value)
	return nil
}

type GrokToolMethod string

const (
	GrokToolUpdateMethod       GrokToolMethod = "session/update"
	GrokToolNotificationMethod GrokToolMethod = "_x.ai/session_notification"
	GrokFilePermissionMethod   GrokToolMethod = "session/request_permission"
	GrokQuestionMethod         GrokToolMethod = "_x.ai/ask_user_question"
	GrokPlanMethod             GrokToolMethod = "_x.ai/exit_plan_mode"
)

// The closed wire graph preserves the original native variants, including
// preview/applied diffs, nullable multi-select, request namespaces and metadata.
// It is inert evidence; neither a file path nor a tool descriptor grants access.
type GrokToolEvent struct {
	Method    GrokToolMethod        `json:"method"`
	RequestID *InteractionRequestID `json:"request_id,omitempty"`
	ArrivalID ID                    `json:"arrival_id,omitempty"`
	Payload   GrokToolPayload       `json:"payload"`
	// Original request bytes bind the native proposal digest. A typed payload
	// alone cannot preserve JSON member order, whitespace or escape spelling.
	ProposalJSON        string          `json:"proposal_json,omitempty"`
	InheritedPermission ID              `json:"inherited_permission,omitempty"`
	PlanOrigin          *GrokPlanOrigin `json:"plan_origin,omitempty"`
}
type GrokPlanOrigin struct {
	EntryToolID  string `json:"entry_tool_id"`
	EntryEventID string `json:"entry_event_id"`
	Revision     uint64 `json:"revision"`
}
type GrokToolPayload struct {
	Session     ID                      `json:"sessionId"`
	Update      *GrokToolUpdate         `json:"update,omitempty"`
	Meta        *GrokToolMetadata       `json:"_meta,omitempty"`
	Tool        *GrokToolUpdate         `json:"toolCall,omitempty"`
	Options     *[]GrokPermissionOption `json:"options,omitempty"`
	ToolID      *string                 `json:"toolCallId,omitempty"`
	Questions   *[]GrokQuestion         `json:"questions,omitempty"`
	Mode        *GrokMode               `json:"mode,omitempty"`
	PlanContent *string                 `json:"planContent,omitempty"`
}
type GrokToolUpdate struct {
	Kind          *string                     `json:"sessionUpdate,omitempty"`
	ID            *string                     `json:"toolCallId,omitempty"`
	InteractionID *string                     `json:"tool_call_id,omitempty"`
	Index         *GrokCount                  `json:"tool_index,omitempty"`
	Name          *string                     `json:"name,omitempty"`
	Arguments     *string                     `json:"arguments_delta,omitempty"`
	Category      *string                     `json:"kind,omitempty"`
	Title         *string                     `json:"title,omitempty"`
	Status        *string                     `json:"status,omitempty"`
	Input         *GrokToolInput              `json:"rawInput,omitempty"`
	Output        *GrokToolOutput             `json:"rawOutput,omitempty"`
	Descriptor    *GrokToolDescriptorEnvelope `json:"_meta,omitempty"`
	Locations     *[]GrokLocation             `json:"locations,omitempty"`
	Content       *[]GrokToolContent          `json:"content,omitempty"`
	Mode          *GrokMode                   `json:"currentModeId,omitempty"`
}
type GrokLocation struct {
	Path string `json:"path"`
}
type GrokToolInput struct {
	Variant    *string         `json:"variant,omitempty"`
	TargetFile *string         `json:"target_file,omitempty"`
	FilePath   *string         `json:"file_path,omitempty"`
	Content    *string         `json:"content,omitempty"`
	Questions  *[]GrokQuestion `json:"questions,omitempty"`
}
type GrokQuestion struct {
	Text            string           `json:"question"`
	Options         []QuestionOption `json:"options"`
	Multiple        *bool            `json:"multiSelect,omitempty"`
	MultiplePresent bool             `json:"-"`
	// Native streamed arguments use another field name. It remains distinct.
	ArgumentMultiple *bool `json:"multi_select,omitempty"`
}
type GrokToolDescriptorEnvelope struct {
	Tool GrokToolDescriptor `json:"x.ai/tool"`
}
type GrokToolDescriptor struct {
	Version   uint32        `json:"version"`
	Name      string        `json:"name"`
	Kind      string        `json:"kind"`
	Namespace string        `json:"namespace"`
	Label     string        `json:"label"`
	ReadOnly  bool          `json:"read_only"`
	Input     *GrokLocation `json:"input,omitempty"`
}
type GrokToolMetadata struct {
	Context   *GrokCount            `json:"totalTokens,omitempty"`
	Event     string                `json:"eventId"`
	Timestamp GrokCount             `json:"agentTimestampMs"`
	Prompt    *string               `json:"promptId,omitempty"`
	Stream    *GrokCount            `json:"streamStartMs,omitempty"`
	Turn      *GrokCount            `json:"turnStartMs,omitempty"`
	Type      *string               `json:"updateType,omitempty"`
	Params    *GrokToolUpdateParams `json:"updateParams,omitempty"`
}
type GrokToolUpdateParams struct {
	ID     string  `json:"toolCallId"`
	Title  *string `json:"title,omitempty"`
	Kind   *string `json:"kind,omitempty"`
	Status *string `json:"status"`
}
type GrokPermissionOption struct {
	ID   string `json:"optionId"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}
type GrokToolContent struct {
	Type    string           `json:"type"`
	Content *GrokContentText `json:"content,omitempty"`
	Path    *string          `json:"path,omitempty"`
	Old     *string          `json:"oldText,omitempty"`
	New     *string          `json:"newText,omitempty"`
	Edits   *GrokEdits       `json:"_meta,omitempty"`
}
type GrokContentText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}
type GrokEdits struct {
	Details []GrokEdit `json:"details"`
}
type GrokEdit struct {
	Old     string    `json:"old_string"`
	OldLine GrokCount `json:"old_line"`
	New     string    `json:"new_string"`
	NewLine GrokCount `json:"new_line"`
	Before  string    `json:"context_before"`
	After   string    `json:"context_after"`
	Prefix  string    `json:"line_prefix"`
}
type GrokToolOutput struct {
	Type   string `json:"type"`
	Answer *struct {
		Message string `json:"message"`
	} `json:"UserAnswered,omitempty"`
	Read    *GrokReadOutput  `json:"FileContent,omitempty"`
	Write   *GrokWriteOutput `json:"EditsApplied,omitempty"`
	Entered *GrokPlanEntered `json:"Entered,omitempty"`
	Ready   *GrokPlanReady   `json:"PlanReady,omitempty"`
}
type GrokReadOutput struct {
	Content string     `json:"content"`
	Concise string     `json:"content_concise"`
	Path    string     `json:"absolute_path"`
	Offset  *GrokCount `json:"offset"`
	Raw     string     `json:"raw_output"`
	Lines   GrokCount  `json:"total_lines"`
}
type GrokWriteOutput struct {
	Old     string    `json:"old_string"`
	New     string    `json:"new_string"`
	Text    string    `json:"tool_output_for_prompt"`
	Concise string    `json:"tool_output_for_prompt_concise"`
	Path    string    `json:"absolute_path"`
	Edits   GrokEdits `json:"edits"`
}
type GrokPlanEntered struct {
	Message string `json:"message"`
	Path    string `json:"plan_file_path"`
	Hints   struct {
		Question string `json:"ask_user"`
		Exit     string `json:"exit_plan"`
		Task     string `json:"task"`
	} `json:"tool_hints"`
	Seed string `json:"plan_file_seed"`
}
type GrokPlanReady struct {
	Message string `json:"message"`
	Content string `json:"plan_content"`
	Path    string `json:"plan_file_path"`
}

type ExecutionGrokToolUpdate struct {
	ID          ID            `json:"id"`
	Observation GrokToolEvent `json:"observation"`
}

func (u ExecutionGrokToolUpdate) Validate() error {
	if u.ID.Validate() != nil || u.Observation.Payload.Session.Validate() != nil {
		return invalidGrokContent()
	}
	raw, err := json.Marshal(u)
	if err != nil || len(raw) > 512<<10 {
		return Fail(ResourceExhausted, "The original Grok tool observation exceeds its bound.", "Retain the complete native evidence without truncating or replaying it.")
	}
	return nil
}

func (q *GrokQuestion) UnmarshalJSON(raw []byte) error {
	type plain GrokQuestion
	var value plain
	var fields map[string]json.RawMessage
	if Decode(raw, &value) != nil || Decode(raw, &fields) != nil {
		return invalidGrokContent()
	}
	*q = GrokQuestion(value)
	_, q.MultiplePresent = fields["multiSelect"]
	return nil
}
func (q GrokQuestion) MarshalJSON() ([]byte, error) {
	value := map[string]any{"question": q.Text, "options": q.Options}
	if q.MultiplePresent {
		value["multiSelect"] = q.Multiple
	}
	if q.ArgumentMultiple != nil {
		value["multi_select"] = q.ArgumentMultiple
	}
	return json.Marshal(value)
}
