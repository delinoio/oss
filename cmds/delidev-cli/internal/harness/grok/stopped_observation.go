package grok

import (
	"context"
	"encoding/hex"
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// InterruptedTextTerminal keeps the three independently received cancellation
// facts. Context is reported by the native RPC; missing usage stays absent.
type InterruptedTextTerminal struct {
	Result InterruptedPromptResult    `json:"result"`
	Turn   InterruptedTurnCompleted   `json:"turn"`
	Prompt InterruptedPromptCompleted `json:"prompt"`
}

// StoppedTextObservation is a copy of the original controller's joined Stop
// evidence. Exactly one terminal variant is present, including a real successful
// result racing Stop. This grants neither history nor continuation authority.
type StoppedTextObservation struct {
	CreationRequestID domain.ID                `json:"creation_request_id"`
	Stop              StopObservation          `json:"stop"`
	Interrupted       *InterruptedTextTerminal `json:"interrupted,omitempty"`
	Completed         *TextTerminal            `json:"completed,omitempty"`
	InputDigest       string                   `json:"input_digest"`
	OutputDigest      string                   `json:"output_digest"`
	ChunkDigests      []string                 `json:"chunk_digests"`
}

func copyStoppedText(v StoppedTextObservation) (StoppedTextObservation, error) {
	raw, err := json.Marshal(v)
	var copy StoppedTextObservation
	// This is our own typed envelope, not an incoming native protocol frame.
	// StopObservation retains its existing Go-field JSON names.
	if err != nil || domain.Decode(raw, &copy) != nil {
		return copy, incompatible()
	}
	return copy, nil
}

// Called only by the original input reader after native terminal/idle and owned
// cleanup, before caller callbacks receive pointers to any terminal facts.
func (a *apiConnection) retainStoppedText(input completedText, interrupted *InterruptedTextTerminal) error {
	if a.stoppedText != nil || a.completedText != nil || a.closureStarted {
		return sessionUncertain()
	}
	chunks := make([]string, len(input.chunks))
	for i, digest := range input.chunks {
		chunks[i] = hex.EncodeToString(digest[:])
	}
	value := StoppedTextObservation{CreationRequestID: a.creationRequest, Stop: a.textControl().observation(), Interrupted: interrupted, Completed: input.terminalFacts, InputDigest: input.bodyDigest, OutputDigest: hex.EncodeToString(input.output[:]), ChunkDigests: chunks}
	copy, err := copyStoppedText(value)
	if err != nil || copy.Validate(a.profile.model) != nil || copy.Stop.Claim.InputRequestID != input.request {
		return sessionUncertain()
	}
	a.stoppedText = &copy
	return nil
}

// ObserveStoppedText cannot be called from the original input callback: it waits
// for that reader and all reply writers to finish. Publication errors and failed
// claims remain uncertainty even if a callback saw some native terminal fields.
func (a *OwnedAPI) ObserveStoppedText(ctx context.Context) (StoppedTextObservation, error) {
	if a == nil || a.connection == nil {
		return StoppedTextObservation{}, apiConfigurationError()
	}
	c := a.connection
	if ctx.Err() != nil {
		return StoppedTextObservation{}, domain.SafeError(ctx.Err())
	}
	select {
	case c.gate <- struct{}{}:
		defer func() { <-c.gate }()
	case <-ctx.Done():
		return StoppedTextObservation{}, domain.SafeError(ctx.Err())
	}
	v := c.stoppedText
	if v == nil || c.completedText != nil || c.closureStarted || c.textControl() == nil || c.wire == nil || v.Stop != c.textControl().observation() || v.Stop.Claim.OwnerID != c.inspection.OwnerID || v.Stop.Claim.ProductSessionID != c.product || v.Stop.Claim.NativeSessionID != c.session || v.CreationRequestID != c.creationRequest || v.Validate(c.profile.model) != nil {
		return StoppedTextObservation{}, sessionUncertain()
	}
	select {
	case <-c.wire.Done():
	default:
		return StoppedTextObservation{}, sessionUncertain()
	}
	if err := c.profile.checkInitialized(); err != nil {
		return StoppedTextObservation{}, err
	}
	return copyStoppedText(*v)
}

// Validate checks copied shape and correlations, not original API ownership or
// publication/workspace cleanup. No zero-filled successful usage is accepted for
// the interrupted variant, and successful races have no closed-history proof.
func (v StoppedTextObservation) Validate(model string) error {
	s := v.Stop
	c := s.Claim
	if c.Validate() != nil || v.CreationRequestID.Validate() != nil || v.CreationRequestID == c.RequestID || v.CreationRequestID == c.InputRequestID || !s.Claimed || !s.Attempted || !s.Delivered || !s.Idle || !s.CleanupJoined || s.ProblemCode != "" || len(v.ChunkDigests) == 0 || len(v.ChunkDigests) > 100000 {
		return incompatible()
	}
	for _, value := range append([]string{v.InputDigest, v.OutputDigest}, v.ChunkDigests...) {
		digest, err := hex.DecodeString(value)
		if err != nil || len(digest) != 32 || hex.EncodeToString(digest) != value {
			return incompatible()
		}
	}
	if s.NativeReason == Cancelled && s.Category == MidTurnAbort && v.Interrupted != nil && v.Completed == nil {
		value := v.Interrupted
		rr, _ := json.Marshal(value.Result)
		tr, _ := json.Marshal(value.Turn)
		pr, _ := json.Marshal(value.Prompt)
		r, e1 := parseInterruptedPromptResult(rr, c.NativeSessionID, c.NativePromptID, model)
		t, e2 := parseInterruptedTurn(tr, c.NativeSessionID, c.NativePromptID)
		p, e3 := parseInterruptedPromptCompleted(pr, c.NativeSessionID, c.NativePromptID)
		if e1 == nil && e2 == nil && e3 == nil && matchInterruption(r, t, p) == nil {
			return nil
		}
	} else if s.NativeReason == EndTurn && s.Category == "" && v.Completed != nil && v.Interrupted == nil {
		value := v.Completed
		rr, _ := json.Marshal(value.Result)
		tr, _ := json.Marshal(value.Turn)
		pr, _ := json.Marshal(value.Prompt)
		r, e1 := parsePromptResult(rr, c.NativeSessionID, c.NativePromptID, model)
		t, e2 := parseTurnCompleted(tr, c.NativeSessionID, c.NativePromptID, model)
		p, e3 := parsePromptCompleted(pr, c.NativeSessionID, c.NativePromptID)
		if e1 == nil && e2 == nil && e3 == nil && matchCompletion(r, t, p, model) == nil && r.Reason == EndTurn && r.Meta.Usage.Calls == 1 && r.Meta.Usage.Turns == 1 {
			return nil
		}
	}
	return incompatible()
}
