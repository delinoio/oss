package grok

import (
	"context"
	"encoding/hex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"strconv"
)

type completedTools struct {
	request    domain.ID
	prompt     string
	terminal   TextTerminal
	rejection  *fileRejectionContext
	accounting responseAccounting
	idle       bool
	output     string
	chunks     [][32]byte
}
type ToolsTerminalObservation struct {
	Owner, Product, Creation, Input, Session domain.ID
	Prompt                                   string
	Terminal                                 domain.GrokToolsTerminal
	OutputDigest                             string
	ChunkDigests                             []string
}

// CloseTools joins only the original owned process after independently matched
// RPC/turn/prompt and idle observations. It never reads text-only history or
// obtains continuation authority. A publication retry cannot call it again.
func (a *OwnedAPI) CloseTools(ctx context.Context) (ToolsTerminalObservation, error) {
	if a == nil || a.connection == nil {
		return ToolsTerminalObservation{}, apiConfigurationError()
	}
	c := a.connection
	if ctx.Err() != nil {
		return ToolsTerminalObservation{}, domain.SafeError(ctx.Err())
	}
	v := c.completedTools
	if v == nil || !v.idle || c.completedText != nil || c.stoppedText != nil || v.request.Validate() != nil || v.accounting.count == 0 {
		return ToolsTerminalObservation{}, sessionUncertain()
	}
	if err := c.Close(); err != nil {
		return ToolsTerminalObservation{}, err
	}
	u := v.terminal.Result.Meta.Usage
	t := v.terminal.Turn
	n := func(v uint64) string { return strconv.FormatUint(v, 10) }
	result := domain.GrokToolsTerminal{Kind: domain.GrokClosedFirstTools, NativeEventID: t.Meta.Event, TimestampMS: n(t.Meta.TimestampMS), ElapsedMS: n(t.Update.ElapsedMS), Model: v.terminal.Result.Meta.Model, Reason: domain.GrokToolsEndTurn, Counts: domain.GrokResponseCounts{Input: n(u.Input), Output: n(u.Output), CachedRead: n(u.CachedRead), CacheCreation: n(u.CacheCreation), Reasoning: n(u.Reasoning)}, TotalTokens: n(u.Total), ModelCalls: n(u.Calls), APIDurationMS: n(u.DurationMS), Turns: n(u.Turns)}
	if v.rejection != nil {
		result.Reason = domain.GrokToolsPermissionRejected
		result.Rejection = &domain.GrokFileRejection{Tool: string(v.rejection.Tool), Reason: v.rejection.Reason}
	}
	if result.Validate(string(c.session)) != nil || uint64(v.accounting.count) != u.Calls || u.Turns != u.Calls {
		return ToolsTerminalObservation{}, sessionUncertain()
	}
	chunks := make([]string, len(v.chunks))
	for i, d := range v.chunks {
		chunks[i] = hex.EncodeToString(d[:])
	}
	return ToolsTerminalObservation{Owner: c.inspection.OwnerID, Product: c.product, Creation: c.creationRequest, Input: v.request, Session: c.session, Prompt: v.prompt, Terminal: result, OutputDigest: v.output, ChunkDigests: chunks}, nil
}
