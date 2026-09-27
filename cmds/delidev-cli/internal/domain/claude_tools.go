package domain

import (
	"encoding/json"
)

type ClaudeToolCallerKind string

const ClaudeDirectToolCaller ClaudeToolCallerKind = "direct"

type ClaudeToolMutation string

const (
	ClaudeToolStart            ClaudeToolMutation = "start"
	ClaudeToolInputAppend      ClaudeToolMutation = "input-append"
	ClaudeToolProposalComplete ClaudeToolMutation = "proposal-complete"
	ClaudeToolResultObserved   ClaudeToolMutation = "result"
)

// The provider message retains only this ordered reference. Tool input and
// output live in their own role=tool message, never assistant text or actions.
type ClaudeToolReference struct {
	ID       ID     `json:"id"`
	NativeID string `json:"native_id"`
	Name     string `json:"name"`
}

func (r ClaudeToolReference) Validate() error {
	if r.ID.Validate() != nil || Text(r.NativeID, "native tool identity", 1024, true) != nil || Text(r.Name, "native tool name", 256, true) != nil {
		return invalidClaudeTool()
	}
	return nil
}

// JSON is retained as inert original text to preserve number and whitespace
// spellings. A native applied input may enrich the streamed proposal; keep both.
type ClaudeToolProposal struct {
	Proposed string `json:"proposed"`
	Applied  string `json:"applied"`
}
type ClaudeToolResult struct {
	NonExecution  *ClaudeToolNonExecution `json:"non_execution,omitempty"`
	NativeEventID string                  `json:"native_event_id"`
	Error         *bool                   `json:"is_error"`
	Text          *string                 `json:"text"`
	Blocks        []ClaudeTextBlock       `json:"blocks"`
	Structured    *string                 `json:"structured"`
}

type ClaudeNonExecutionKind string

const ClaudePermissionRuleNonExecution ClaudeNonExecutionKind = "permission-rule"

type ClaudeToolNonExecution struct {
	NativeID string                 `json:"id"`
	Kind     ClaudeNonExecutionKind `json:"non_execution_kind"`
}

func (n ClaudeToolNonExecution) Validate() error {
	if n.Kind != ClaudePermissionRuleNonExecution || Text(n.NativeID, "native non-executed tool", 1024, true) != nil {
		return invalidClaudeTool()
	}
	return nil
}

type ClaudeToolUpdate struct {
	Mutation        ClaudeToolMutation  `json:"mutation"`
	Reference       ClaudeToolReference `json:"reference"`
	MessageID       ID                  `json:"message_id"`
	NativeMessageID string              `json:"native_message_id"`
	Index           uint32              `json:"index"`
	// Omitted and explicit direct caller remain distinct. Other ancestries need
	// their separate provider/child ownership graph before publication.
	Caller       *ClaudeToolCallerKind `json:"caller"`
	InitialInput *string               `json:"initial_input,omitempty"`
	Delta        *string               `json:"delta,omitempty"`
	Proposal     *ClaudeToolProposal   `json:"proposal,omitempty"`
	Result       *ClaudeToolResult     `json:"result,omitempty"`
}

type ClaudeToolContent struct {
	Reference       ClaudeToolReference   `json:"reference"`
	MessageID       ID                    `json:"message_id"`
	NativeMessageID string                `json:"native_message_id"`
	Index           uint32                `json:"index"`
	Caller          *ClaudeToolCallerKind `json:"caller"`
	InitialInput    string                `json:"initial_input"`
	InputDelta      *string               `json:"input_delta"`
	Proposal        *ClaudeToolProposal   `json:"proposal"`
	Result          *ClaudeToolResult     `json:"result"`
}

func invalidClaudeTool() *Error {
	return Fail(InvalidArgument, "Invalid original Claude tool observation.", "Preserve the original call, ordered provider block and independent proposal/result evidence.")
}
func validClaudeToolJSON(value string) bool {
	if Text(value, "native tool JSON", MaxMessageText, true) != nil {
		return false
	}
	var object map[string]json.RawMessage
	return Decode([]byte(value), &object) == nil && object != nil
}

// The pinned native CLI emits an object for structured results and an original
// JSON string for tool errors. A string requires explicit native error evidence;
// neither form grants execution, approval or filesystem authority.
func ValidClaudeToolResultMetadata(value string, failed *bool) bool {
	if validClaudeToolJSON(value) {
		return true
	}
	var message *string
	return failed != nil && *failed && Text(value, "native tool metadata", MaxMessageText, true) == nil && Decode([]byte(value), &message) == nil && message != nil && Text(*message, "native tool error metadata", MaxMessageText, false) == nil
}

func (r ClaudeToolResult) Validate() error {
	if r.NonExecution != nil && (r.NonExecution.Validate() != nil || r.Error == nil || !*r.Error) {
		return invalidClaudeTool()
	}
	if NativeIdentity(r.NativeEventID).Validate(ClaudeCode, NativeTurnIdentity) != nil || r.Text != nil && Text(*r.Text, "native tool result", MaxMessageText, false) != nil || r.Text != nil && r.Blocks != nil || len(r.Blocks) > 1024 || r.Structured != nil && !ValidClaudeToolResultMetadata(*r.Structured, r.Error) {
		return invalidClaudeTool()
	}
	size := 0
	for _, block := range r.Blocks {
		if block.Kind != ClaudeText || block.Validate() != nil {
			return invalidClaudeTool()
		}
		size += len(block.Text)
	}
	if size > MaxMessageText {
		return invalidClaudeTool()
	}
	return nil
}
func (u ClaudeToolUpdate) Validate() error {
	if u.Result != nil && u.Result.NonExecution != nil && u.Result.NonExecution.NativeID != u.Reference.NativeID {
		return invalidClaudeTool()
	}
	if u.Reference.Validate() != nil || u.MessageID.Validate() != nil || Text(u.NativeMessageID, "native provider identity", 1024, true) != nil || u.Reference.NativeID == u.NativeMessageID || u.Index >= 1024 || u.Caller != nil && *u.Caller != ClaudeDirectToolCaller {
		return invalidClaudeTool()
	}
	if (u.Mutation == ClaudeToolStart) != (u.InitialInput != nil) || (u.Mutation == ClaudeToolInputAppend) != (u.Delta != nil) || (u.Mutation == ClaudeToolProposalComplete) != (u.Proposal != nil) || (u.Mutation == ClaudeToolResultObserved) != (u.Result != nil) {
		return invalidClaudeTool()
	}
	switch u.Mutation {
	case ClaudeToolStart:
		if !validClaudeToolJSON(*u.InitialInput) {
			return invalidClaudeTool()
		}
	case ClaudeToolInputAppend:
		if Text(*u.Delta, "native tool input fragment", MaxMessageText, false) != nil {
			return invalidClaudeTool()
		}
	case ClaudeToolProposalComplete:
		if !validClaudeToolJSON(u.Proposal.Proposed) || !validClaudeToolJSON(u.Proposal.Applied) {
			return invalidClaudeTool()
		}
	case ClaudeToolResultObserved:
		if u.Result.Validate() != nil {
			return invalidClaudeTool()
		}
	default:
		return invalidClaudeTool()
	}
	raw, err := json.Marshal(u)
	if err != nil || len(raw) > 768<<10 {
		return invalidClaudeTool()
	}
	return nil
}

// ApplyClaudeTool returns a private copy. Native proposal completion does not
// claim approval, execution or success, and nullable native errors stay nullable.
func ApplyClaudeTool(prior *ClaudeToolContent, state MessageState, u ClaudeToolUpdate) (*ClaudeToolContent, MessageState, error) {
	if err := u.Validate(); err != nil {
		return nil, "", err
	}
	conflict := func() (*ClaudeToolContent, MessageState, error) {
		return nil, "", Fail(Conflict, "Claude tool evidence does not follow its original lifecycle.", "Retain the original call and publication receipt without replaying the native tool.")
	}
	var next ClaudeToolContent
	if u.Mutation == ClaudeToolStart {
		if prior != nil || state != "" {
			return conflict()
		}
		next = ClaudeToolContent{Reference: u.Reference, MessageID: u.MessageID, NativeMessageID: u.NativeMessageID, Index: u.Index, Caller: u.Caller, InitialInput: *u.InitialInput}
		state = MessageStreaming
	} else {
		if prior == nil || state != MessageStreaming || prior.Result != nil || prior.Reference != u.Reference || prior.MessageID != u.MessageID || prior.NativeMessageID != u.NativeMessageID || prior.Index != u.Index || (prior.Caller == nil) != (u.Caller == nil) || prior.Caller != nil && u.Caller != nil && *prior.Caller != *u.Caller {
			return conflict()
		}
		next = *prior
		switch u.Mutation {
		case ClaudeToolInputAppend:
			if prior.Proposal != nil {
				return conflict()
			}
			delta := ""
			if prior.InputDelta != nil {
				delta = *prior.InputDelta
			}
			delta += *u.Delta
			if Text(delta, "retained native input fragment", MaxMessageText, false) != nil {
				return conflict()
			}
			next.InputDelta = &delta
		case ClaudeToolProposalComplete:
			if prior.Proposal != nil {
				return conflict()
			}
			expected := prior.InitialInput
			if prior.InputDelta != nil && *prior.InputDelta != "" {
				expected = *prior.InputDelta
			}
			if u.Proposal.Proposed != expected {
				return conflict()
			}
			next.Proposal = u.Proposal
		case ClaudeToolResultObserved:
			if prior.Proposal == nil {
				return conflict()
			}
			next.Result = u.Result
			state = MessageComplete
		default:
			return conflict()
		}
	}
	// Deep-copy pointer/slice fields and enforce the complete retained bound before
	// either the Worker or transactional server can publish any partial mutation.
	raw, err := json.Marshal(next)
	if err != nil || len(raw) > 768<<10 {
		return nil, "", Fail(ResourceExhausted, "Claude tool retention reached its bound.", "Preserve original evidence without truncation or tool replay.")
	}
	var retained ClaudeToolContent
	if Decode(raw, &retained) != nil {
		return conflict()
	}
	return &retained, state, nil
}
