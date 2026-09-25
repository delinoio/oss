package codex

import (
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type turnWire struct {
	ID          domain.ID         `json:"id"`
	Items       []json.RawMessage `json:"items"`
	ItemsView   string            `json:"itemsView"`
	Status      TurnStatus        `json:"status"`
	StartedAt   *int64            `json:"startedAt"`
	CompletedAt *int64            `json:"completedAt"`
	DurationMS  *int64            `json:"durationMs"`
	Error       json.RawMessage   `json:"error"`
}

func decodeTurn(raw json.RawMessage) (Turn, error) {
	var wire turnWire
	if domain.Decode(raw, &wire) != nil || wire.ID.Validate() != nil || wire.Items == nil {
		return Turn{}, incompatible()
	}
	if wire.Status != TurnRunning && !wire.Status.terminal() {
		return Turn{}, incompatible()
	}
	switch wire.ItemsView {
	case "", "full", "summary", "notLoaded":
	default:
		return Turn{}, incompatible()
	}
	for _, n := range []*int64{wire.StartedAt, wire.CompletedAt, wire.DurationMS} {
		if n != nil && *n < 0 {
			return Turn{}, incompatible()
		}
	}
	if wire.Status == TurnRunning && (wire.CompletedAt != nil || wire.DurationMS != nil) {
		return Turn{}, incompatible()
	}
	result := Turn{ID: wire.ID, Status: wire.Status, StartedAt: wire.StartedAt, CompletedAt: wire.CompletedAt, DurationMS: wire.DurationMS}
	hasError := len(wire.Error) > 0 && string(wire.Error) != "null"
	if hasError {
		var failure struct {
			Message           *string         `json:"message"`
			AdditionalDetails *string         `json:"additionalDetails"`
			Code              json.RawMessage `json:"codexErrorInfo"`
			Misalignment      json.RawMessage `json:"misalignment"`
		}
		if domain.Decode(wire.Error, &failure) != nil || failure.Message == nil || wire.Status != TurnFailed {
			return Turn{}, incompatible()
		}
	}
	if wire.Status == TurnFailed {
		result.Problem = domain.Fail(domain.Unavailable, "The native Codex turn failed.", "Reconcile native state and resume explicitly; native diagnostic content is not exposed.")
	}
	return result, nil
}
func decodeTurnResponse(raw json.RawMessage) (Turn, error) {
	var result struct {
		Turn json.RawMessage `json:"turn"`
	}
	if domain.Decode(raw, &result) != nil {
		return Turn{}, incompatible()
	}
	return decodeTurn(result.Turn)
}
