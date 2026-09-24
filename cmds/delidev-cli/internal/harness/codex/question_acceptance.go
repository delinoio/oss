package codex

import (
	"crypto/sha256"
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

// Raw notifications supplement the canonical item/usage stream. They include
// private instructions and diagnostic metadata, so only exact owned question
// or permission outputs may create product observations. Discard all other bounded supplement
// content; its canonical event still requires the appropriate typed adapter.
func (c *Client) observeRawInteractionEvidenceLocked(native nativewire.Event) (Event, error) {
	var envelope struct {
		ThreadID      domain.ID       `json:"threadId"`
		TurnID        domain.ID       `json:"turnId"`
		Item          json.RawMessage `json:"item,omitempty"`
		ResponseID    *string         `json:"responseId,omitempty"`
		Usage         json.RawMessage `json:"usage,omitempty"`
		UsageMetadata json.RawMessage `json:"usageMetadata,omitempty"`
	}
	if domain.Decode(native.Params, &envelope) != nil || envelope.ThreadID.Validate() != nil || envelope.TurnID.Validate() != nil {
		return Event{}, incompatible()
	}
	if envelope.ThreadID != c.thread {
		return privateNative(native), nil
	}
	turn, known := c.execution.turns[envelope.TurnID]
	if !known {
		return Event{}, incompatible()
	}
	discarded := c.metadata(RawSupplementDiscarded)
	discarded.TurnID, discarded.Late = envelope.TurnID, turn.Turn.Status.terminal()
	if native.Method == "rawResponse/completed" {
		if len(envelope.Item) != 0 || envelope.ResponseID == nil || domain.Text(*envelope.ResponseID, "native response identity", 1024, true) != nil {
			return Event{}, incompatible()
		}
		return discarded, nil
	}
	if envelope.ResponseID != nil || len(envelope.Usage) != 0 || len(envelope.UsageMetadata) != 0 {
		return Event{}, incompatible()
	}
	var header struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(envelope.Item, &header) != nil {
		return Event{}, incompatible()
	}
	switch header.Type {
	case "function_call_output":
	case "message", "agent_message", "reasoning", "local_shell_call", "function_call", "tool_search_call", "custom_tool_call", "custom_tool_call_output", "tool_search_output", "web_search_call", "image_generation_call", "compaction", "compaction_trigger", "context_compaction", "other":
		return discarded, nil
	default:
		return Event{}, incompatible()
	}
	var output nativeFunctionOutput
	if domain.Decode(envelope.Item, &output) != nil || len(output.Output) == 0 {
		return Event{}, incompatible()
	}
	if output.CallID == nil {
		return discarded, nil
	}
	if domain.Text(*output.CallID, "native call identity", 1024, true) != nil {
		return Event{}, incompatible()
	}
	var owned *trackedInteraction
	matches := 0
	for _, candidate := range c.execution.interactions.arrivals {
		if candidate.status.TurnID != envelope.TurnID || candidate.status.ItemID != *output.CallID {
			continue
		}
		matches++
		if candidate.kind == UserInputInteraction || (candidate.kind == ApprovalInteraction && candidate.approvalKind == PermissionsApproval) {
			owned = candidate
		}
	}
	if owned == nil {
		return discarded, nil
	}
	if matches != 1 {
		return Event{}, incompatible()
	}

	// Stop/cancellation also returns a native tool error or empty answer. With
	// no delivered owner response this is only supplemental native output.
	if owned.status.ResponseID == "" || owned.status.Delivery == QuestionNotSent {
		return discarded, nil
	}
	if owned.kind == ApprovalInteraction {
		return c.observePermissionAcceptanceLocked(owned, output, discarded)
	}
	// A duplicate exact observation can confirm an existing fact, but cannot
	// create another publication or consume response accounting twice.
	if output.Namespace != nil || (output.Name != nil && *output.Name != "request_user_input") {
		return Event{}, incompatible()
	}
	var text *string
	var answers nativeQuestionResponse
	if json.Unmarshal(output.Output, &text) != nil || text == nil || len(*text) > maxAnswerBytes || domain.Decode([]byte(*text), &answers) != nil {
		return Event{}, incompatible()
	}
	raw, err := json.Marshal(answers)
	if err != nil || sha256.Sum256(raw) != owned.answerDigest {
		return Event{}, incompatible()
	}
	if owned.status.Accepted {
		return discarded, nil
	}
	owned.status.Accepted = true
	status := owned.status
	return Event{Kind: QuestionAcceptedEvent, ThreadID: c.thread, TurnID: envelope.TurnID, ItemID: status.ItemID, InteractionState: &status, Correlated: true, Late: turn.Turn.Status.terminal()}, nil
}

type nativeFunctionOutput struct {
	Type      string          `json:"type"`
	ID        *string         `json:"id,omitempty"`
	CallID    *string         `json:"call_id,omitempty"`
	Name      *string         `json:"name,omitempty"`
	Namespace *string         `json:"namespace,omitempty"`
	Output    json.RawMessage `json:"output"`
	Metadata  json.RawMessage `json:"internal_chat_message_metadata_passthrough,omitempty"`
}
