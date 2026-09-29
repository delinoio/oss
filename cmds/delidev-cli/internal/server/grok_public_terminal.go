package server

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/grok"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"strconv"
)

func publishGrokPublicTerminal(tx *store.Tx, input domain.ExecutionJobInput, sr store.Record, p *domain.ExecutionProgress, event domain.ExecutionEvent) error {
	v := event.GrokPublicTerminal
	digestInput, digestErr := grok.TextInputClaimDigest(domain.ID(event.NativeThreadID), input.Input.Prompt)
	mode := p.Observed.GrokMode
	if p.GrokMode != nil {
		mode = p.GrokMode.Mode
	}
	if v == nil || v.Validate(event.NativeThreadID) != nil || p.GrokTerminal != nil || p.GrokStop != nil || p.GrokPublicTerminal != nil || input.Continuation != nil || v.InputRequestID != input.TurnRequestID || digestErr != nil || v.InputDigest != digestInput || v.Model != input.Configuration.NativeModel || v.Mode != mode || v.Outcome != event.Outcome || p.GrokContent == nil || p.GrokContent.MessageID != "" || strconv.FormatUint(uint64(p.GrokContent.Responses), 10) != v.Responses || p.UnconfirmedResponses != 0 {
		return executionEventConflict()
	}
	pending, err := tx.OpenExecutionInteractions(input.ExecutionID)
	if err != nil {
		return err
	}
	if len(pending) != 0 {
		return executionEventConflict()
	}
	complete, err := tx.ExecutionMessagesComplete(input.ExecutionID)
	if err != nil {
		return err
	}
	if !complete {
		return executionEventConflict()
	}
	digest, chunks, err := tx.GrokTextProof(input.ExecutionID)
	if err != nil {
		return err
	}
	if digest != v.OutputDigest || chunks != v.TextChunks {
		return executionEventConflict()
	}
	copy := *v
	p.GrokPublicTerminal = &copy
	return nil
}
