package grok

import (
	"context"
	"encoding/hex"
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// TextTerminal retains the three independent original successful-text facts.
// Neither a caller's copy nor a later history read can create these facts.
type TextTerminal struct {
	Result PromptResult    `json:"result"`
	Turn   TurnCompleted   `json:"turn"`
	Prompt PromptCompleted `json:"prompt"`
}

// ClosedTextObservation is comparison evidence from the original API only. It
// contains no transcript, summary, local path, credential or continuation lease.
// Publication and workspace cleanup must still be proved by the owning Worker.
type ClosedTextObservation struct {
	History      TextHistory  `json:"history"`
	Terminal     TextTerminal `json:"terminal"`
	OutputDigest string       `json:"output_digest"`
	ChunkDigests []string     `json:"chunk_digests"`
}

func retainTextTerminal(result PromptResult, turn TurnCompleted, prompt PromptCompleted) (TextTerminal, error) {
	original := TextTerminal{Result: result, Turn: turn, Prompt: prompt}
	raw, err := json.Marshal(original)
	var copy TextTerminal
	if err != nil || decode(raw, &copy) != nil {
		return copy, incompatible()
	}
	// Deep-copy RawMessage model maps and the original null result independently
	// before any caller-owned publication callback receives the result.
	return copy, nil
}

// TextChunkDigest normalizes only the already validated original envelope. Its
// metadata and exact text are included; callers cannot recreate a native event
// or send authority from the returned comparison digest.
func TextChunkDigest(chunk TextChunk) (string, error) {
	raw, err := json.Marshal(chunk)
	if err != nil {
		return "", incompatible()
	}
	if _, err := parseTextChunk(raw, chunk.Session, chunk.Meta.Prompt); err != nil {
		return "", err
	}
	digest := historyValueDigest(chunk)
	return hex.EncodeToString(digest[:]), nil
}

func (a *OwnedAPI) ObserveClosedText(ctx context.Context) (ClosedTextObservation, error) {
	if a == nil || a.connection == nil {
		return ClosedTextObservation{}, apiConfigurationError()
	}
	history, err := a.connection.verifyClosedText(ctx)
	if err != nil {
		return ClosedTextObservation{}, err
	}
	return a.connection.closedTextObservation(ctx, history)
}

func (a *apiConnection) closedTextObservation(ctx context.Context, history TextHistory) (ClosedTextObservation, error) {
	select {
	case a.gate <- struct{}{}:
		defer func() { <-a.gate }()
	case <-ctx.Done():
		return ClosedTextObservation{}, domain.SafeError(ctx.Err())
	}
	completed := a.completedText
	if completed == nil || completed.history == nil || *completed.history != history || completed.terminalFacts == nil || history.InputID != completed.request || history.ClosureID != completed.closed || history.NativeSessionID != a.session || history.NativePromptID != completed.prompt || history.TextChunks != uint64(len(completed.chunks)) {
		return ClosedTextObservation{}, historyUncertain()
	}
	terminal, err := retainTextTerminal(completed.terminalFacts.Result, completed.terminalFacts.Turn, completed.terminalFacts.Prompt)
	if err != nil {
		return ClosedTextObservation{}, err
	}
	resultRaw, _ := json.Marshal(terminal.Result)
	turnRaw, _ := json.Marshal(terminal.Turn)
	promptRaw, _ := json.Marshal(terminal.Prompt)
	result, e1 := parsePromptResult(resultRaw, a.session, completed.prompt, a.profile.model)
	turn, e2 := parseTurnCompleted(turnRaw, a.session, completed.prompt, a.profile.model)
	prompt, e3 := parsePromptCompleted(promptRaw, a.session, completed.prompt)
	usage, usageErr := validateUsage(result.Meta.Usage, a.profile.model)
	if e1 != nil || e2 != nil || e3 != nil || usageErr != nil || matchCompletion(result, turn, prompt, a.profile.model) != nil || result.Reason != EndTurn || result.Meta.Usage.Calls != 1 || result.Meta.Usage.Turns != 1 || usage != completed.usage || historyValueDigest(turn) != completed.terminal {
		return ClosedTextObservation{}, historyUncertain()
	}
	chunks := make([]string, len(completed.chunks))
	for i, digest := range completed.chunks {
		chunks[i] = hex.EncodeToString(digest[:])
	}
	return ClosedTextObservation{History: history, Terminal: terminal, OutputDigest: hex.EncodeToString(completed.output[:]), ChunkDigests: chunks}, nil
}

// CompletedTextScope lets a publication owner compare the live original API
// before asking it to close. It never adopts another process or sends input.
type CompletedTextScope struct {
	OwnerID           domain.ID
	ProductSessionID  domain.ID
	CreationRequestID domain.ID
	InputID           domain.ID
	NativeSessionID   domain.ID
	NativePromptID    string
}

func (a *OwnedAPI) CompletedTextScope(ctx context.Context) (CompletedTextScope, error) {
	if a == nil || a.connection == nil {
		return CompletedTextScope{}, apiConfigurationError()
	}
	c := a.connection
	select {
	case c.gate <- struct{}{}:
		defer func() { <-c.gate }()
	case <-ctx.Done():
		return CompletedTextScope{}, domain.SafeError(ctx.Err())
	}
	original := c.completedText
	if original == nil || original.terminalFacts == nil || c.closureStarted || c.wire == nil {
		return CompletedTextScope{}, sessionUncertain()
	}
	select {
	case <-c.wire.Done():
		return CompletedTextScope{}, sessionUncertain()
	default:
	}
	if err := c.profile.checkInitialized(); err != nil {
		return CompletedTextScope{}, err
	}
	return CompletedTextScope{OwnerID: c.inspection.OwnerID, ProductSessionID: c.product, CreationRequestID: c.creationRequest, InputID: original.request, NativeSessionID: c.session, NativePromptID: original.prompt}, nil
}

// Validate checks the copied observation's closed shape and correlations only;
// it cannot establish original process, filesystem or publication ownership.
func (v ClosedTextObservation) Validate(model string) error {
	h := v.History
	if h.User.Validate(string(h.NativeSessionID)) != nil || h.User.Model != model || h.InputID.Validate() != nil || h.ClosureID.Validate() != nil || h.NativeSessionID.Validate() != nil || !nativeUUID(h.NativePromptID, 4) || h.TextChunks == 0 || h.TextChunks > 100000 || h.TextChunks != uint64(len(v.ChunkDigests)) {
		return incompatible()
	}
	digests := append([]string{h.FilesDigest, v.OutputDigest}, v.ChunkDigests...)
	for _, value := range digests {
		digest, err := hex.DecodeString(value)
		if err != nil || len(digest) != 32 || hex.EncodeToString(digest) != value {
			return incompatible()
		}
	}
	resultRaw, _ := json.Marshal(v.Terminal.Result)
	turnRaw, _ := json.Marshal(v.Terminal.Turn)
	promptRaw, _ := json.Marshal(v.Terminal.Prompt)
	r, e1 := parsePromptResult(resultRaw, h.NativeSessionID, h.NativePromptID, model)
	t, e2 := parseTurnCompleted(turnRaw, h.NativeSessionID, h.NativePromptID, model)
	p, e3 := parsePromptCompleted(promptRaw, h.NativeSessionID, h.NativePromptID)
	if e1 != nil || e2 != nil || e3 != nil || matchCompletion(r, t, p, model) != nil || r.Reason != EndTurn || r.Meta.Usage.Calls != 1 || r.Meta.Usage.Turns != 1 {
		return incompatible()
	}
	return nil
}
