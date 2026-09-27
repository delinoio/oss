package grok

import (
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const permissionRejected CancellationCategory = "PermissionRejected"

type fileRejectionContext struct {
	Tool   fileToolName `json:"tool_name"`
	Reason string       `json:"reason"`
}

type rejectedFileResult struct {
	Result  PromptResult
	Context fileRejectionContext
}

type rejectedFileTurn struct {
	Turn    TurnCompleted
	Context fileRejectionContext
}

type rejectedFileCompletion struct {
	Completion PromptCompleted
	Context    fileRejectionContext
}

// Remove only the independently validated native rejection extension from a
// private parsing copy. The remaining complete envelope still passes its exact
// existing schema; unknown/case-aliased fields are never discarded. RawMessage
// preserves original uint64 spelling without a floating-point map conversion.
func splitFileRejection(raw []byte, metadata bool) ([]byte, fileRejectionContext, error) {
	var envelope map[string]json.RawMessage
	var reason fileRejectionContext
	if decode(raw, &envelope) != nil {
		return nil, reason, incompatible()
	}
	fields := envelope
	if metadata {
		fields = nil
		if decode(envelope["_meta"], &fields) != nil {
			return nil, reason, incompatible()
		}
	}
	var category CancellationCategory
	if decode(fields["cancellationCategory"], &category) != nil || category != permissionRejected || decode(fields["cancellationContext"], &reason) != nil || reason.Tool != writeFileTool || !text(reason.Reason, 4096) {
		return nil, reason, incompatible()
	}
	delete(fields, "cancellationCategory")
	delete(fields, "cancellationContext")
	if metadata {
		envelope["_meta"], _ = json.Marshal(fields)
	}
	clean, err := json.Marshal(envelope)
	if err != nil {
		return nil, reason, incompatible()
	}
	return clean, reason, nil
}

func parseRejectedFileResult(raw []byte, session domain.ID, prompt, model string, accounting responseAccounting) (rejectedFileResult, error) {
	clean, context, err := splitFileRejection(raw, true)
	if err != nil {
		return rejectedFileResult{}, err
	}
	result, err := parseFilePromptResult(clean, session, prompt, model, accounting)
	if err != nil || result.Reason != Cancelled {
		return rejectedFileResult{}, incompatible()
	}
	return rejectedFileResult{Result: result, Context: context}, nil
}

func parseRejectedFileTurn(raw []byte, session domain.ID, prompt, model string) (rejectedFileTurn, error) {
	clean, context, err := splitFileRejection(raw, true)
	if err != nil {
		return rejectedFileTurn{}, err
	}
	turn, err := parseTurnCompleted(clean, session, prompt, model)
	if err != nil || turn.Update.Reason != Cancelled {
		return rejectedFileTurn{}, incompatible()
	}
	return rejectedFileTurn{Turn: turn, Context: context}, nil
}

func parseRejectedFileCompletion(raw []byte, session domain.ID, prompt string) (rejectedFileCompletion, error) {
	clean, context, err := splitFileRejection(raw, false)
	if err != nil {
		return rejectedFileCompletion{}, err
	}
	completed, err := parsePromptCompleted(clean, session, prompt)
	if err != nil || completed.Reason != Cancelled {
		return rejectedFileCompletion{}, incompatible()
	}
	return rejectedFileCompletion{Completion: completed, Context: context}, nil
}

func matchFileRejection(result rejectedFileResult, turn rejectedFileTurn, completed rejectedFileCompletion, model string) error {
	if result.Context != turn.Context || result.Context != completed.Context || result.Context.Tool != writeFileTool {
		return incompatible()
	}
	return matchCompletion(result.Result, turn.Turn, completed.Completion, model)
}
