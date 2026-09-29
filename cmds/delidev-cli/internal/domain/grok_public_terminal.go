package domain

// Rich terminal counters describe the original root turn. Response counters
// remain separate observations; these overlapping totals are never summed.
type GrokPublicTerminal struct {
	InputRequestID     ID                 `json:"input_request_id"`
	InputDigest        string             `json:"input_digest"`
	OutputDigest       string             `json:"output_digest"`
	NativeFactsDigest  string             `json:"native_facts_digest"`
	TextChunks         uint32             `json:"text_chunks"`
	NativeEventID      string             `json:"native_event_id"`
	TimestampMS        string             `json:"timestamp_ms"`
	Model              string             `json:"model"`
	Mode               GrokMode           `json:"mode"`
	Outcome            ExecutionOutcome   `json:"outcome"`
	PermissionRejected bool               `json:"permission_rejected"`
	Responses          string             `json:"responses"`
	Counts             GrokResponseCounts `json:"counts"`
	TotalTokens        string             `json:"total_tokens"`
	APIDurationMS      string             `json:"api_duration_ms"`
	Turns              string             `json:"turns"`
	Idle               bool               `json:"idle"`
	CleanupJoined      bool               `json:"cleanup_joined"`
}

func (v GrokPublicTerminal) Validate(thread string) error {
	if v.InputRequestID.Validate() != nil || !validGrokDigest(v.InputDigest) || !validGrokDigest(v.OutputDigest) || !validGrokDigest(v.NativeFactsDigest) || Text(v.Model, "original model", 256, true) != nil || !v.Mode.Valid() || !v.Idle || !v.CleanupJoined || v.TextChunks > 100000 || v.PermissionRejected != (v.Outcome == ExecutionStopped) || v.Outcome != ExecutionSucceeded && v.Outcome != ExecutionStopped {
		return invalidGrokContent()
	}
	if _, err := GrokEventIndex(v.NativeEventID, thread); err != nil {
		return err
	}
	if n, ok := grokCount(v.TimestampMS); !ok || n > 253402300799999 {
		return invalidGrokContent()
	}
	for _, value := range []string{v.Responses, v.TotalTokens, v.APIDurationMS, v.Turns} {
		if _, ok := grokCount(value); !ok {
			return invalidGrokContent()
		}
	}
	if n, _ := grokCount(v.Responses); n == 0 || n > 100000 {
		return invalidGrokContent()
	}
	return (GrokResponseUsage{Ordinal: 1, Counts: v.Counts}).Validate()
}
