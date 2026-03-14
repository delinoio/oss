package normalize

import (
	"log/slog"

	dexdexv1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
)

// RawAgentOutput represents unprocessed output from an agent session.
type RawAgentOutput struct {
	SessionID string
	Text      string
	Kind      string // "text", "tool_call", "tool_result", "plan", "progress", "warning", "error"
}

// OutputNormalizer converts raw agent output into proto SessionOutputEvent messages.
type OutputNormalizer struct {
	logger *slog.Logger
}

// NewOutputNormalizer creates a new OutputNormalizer with the given logger.
func NewOutputNormalizer(logger *slog.Logger) *OutputNormalizer {
	return &OutputNormalizer{logger: logger}
}

// kindMapping maps raw kind strings to proto SessionOutputKind enum values.
var kindMapping = map[string]dexdexv1.SessionOutputKind{
	"text":        dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_TEXT,
	"tool_call":   dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_TOOL_CALL,
	"tool_result": dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_TOOL_RESULT,
	"plan":        dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_PLAN_UPDATE,
	"progress":    dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_PROGRESS,
	"warning":     dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_WARNING,
	"error":       dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_ERROR,
}

// Normalize converts a RawAgentOutput into a proto SessionOutputEvent.
// Unknown kind values are mapped to SESSION_OUTPUT_KIND_UNSPECIFIED.
func (n *OutputNormalizer) Normalize(raw RawAgentOutput) *dexdexv1.SessionOutputEvent {
	kind, ok := kindMapping[raw.Kind]
	if !ok {
		n.logger.Warn("unknown output kind, mapping to unspecified",
			"session_id", raw.SessionID,
			"kind", raw.Kind,
		)
		kind = dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_UNSPECIFIED
	}

	return &dexdexv1.SessionOutputEvent{
		SessionId: raw.SessionID,
		Kind:      kind,
		Body:      raw.Text,
	}
}
