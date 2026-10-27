// SPDX-License-Identifier: Apache-2.0
// Completion fallback adapted from opencodex; see LICENSE.upstream.
package kiro

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/delinoio/oss/packages/aiprouter/inference"
)

type fallbackKey struct{}

const completionRetry = "Continue the existing task without quoting this instruction. If the task is complete, call codex_kiro_final_answer now with the complete final answer. If you cannot continue until the user supplies a decision, information, or a clarification that only they can give, call codex_kiro_final_answer now with that question as the answer. Otherwise issue the next real tool call now. Do not solicit a new task and do not emit another progress-only message."

var _ inference.ExecutingAdapter = (*Adapter)(nil)

// Execute buffers only completion-protocol turns, so a progress-only first leg
// cannot become visible before the single admitted fallback. Ordinary text turns
// stream directly. Every physical send uses the original executor and budget.
func (a *Adapter) Execute(ctx context.Context, in inference.AdapterInput, executor inference.AdapterExecutor, _ inference.AdapterStateStore) (*http.Response, inference.Wire, error) {
	request, err := a.Build(ctx, in)
	if err != nil {
		var typed *inference.Error
		if errors.As(err, &typed) && typed.Code == "kiro_final_answer_already_delivered" {
			return a.localTerminal(ctx, in)
		}
		return nil, inference.Chat, err
	}
	response, err := executor.Do(ctx, request)
	if err != nil {
		return nil, inference.Chat, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return response, inference.Chat, nil
	}
	decoded, err := a.DecodeWithInput(ctx, in, response)
	if err != nil {
		response.Body.Close()
		return nil, inference.Chat, err
	}
	state := ctx.Value(stateKey{}).(*requestState)
	state.mu.Lock()
	completion := state.completion
	state.mu.Unlock()
	if !completion {
		return decoded, inference.Chat, nil
	}
	data, readErr := io.ReadAll(io.LimitReader(decoded.Body, maxRetainedBytes+1))
	decoded.Body.Close()
	if len(data) > maxRetainedBytes || readErr != nil {
		if accounting, ok := executor.(inference.AttemptAccounting); ok {
			accounting.RecordUnmeasuredAttempt()
		}
	}
	if len(data) > maxRetainedBytes {
		return nil, inference.Chat, failure(502, "kiro_output_limit")
	}
	if ctx.Err() != nil {
		return nil, inference.Chat, ctx.Err()
	}
	var typed *inference.Error
	if !errors.As(readErr, &typed) || typed.Code != "kiro_completion_fallback_required" {
		if readErr != nil {
			return nil, inference.Chat, readErr
		}
		decoded.Body = io.NopCloser(bytes.NewReader(data))
		return decoded, inference.Chat, nil
	}
	// A discarded leg must not disappear from billing uncertainty. Requiring this
	// seam also prevents callers with an obsolete executor from silently replaying.
	_, ok := executor.(inference.AttemptAccounting)
	if !ok {
		return nil, inference.Chat, readErr
	}
	m, _ := MetadataFor(ctx, in.Principal)
	retryCtx, err := WithContinuation(a.PrepareContext(ctx), in.Principal, m.ConversationID)
	if err != nil {
		return nil, inference.Chat, err
	}
	retryCtx, err = WithImagePositions(retryCtx, in.Principal, m.ImagePositions)
	if err != nil {
		return nil, inference.Chat, err
	}
	retryCtx = context.WithValue(retryCtx, fallbackKey{}, true)
	// Rebuild with the original public body, then add only the provider-owned
	// replay fields. Reasoning/signatures from the first leg are never replayed.
	retry, err := a.Build(retryCtx, in)
	if err != nil {
		return nil, inference.Chat, err
	}
	var payload object
	if err = json.Unmarshal(retry.Body, &payload); err != nil {
		return nil, inference.Chat, err
	}
	conversation := obj(payload["conversationState"])
	current := obj(obj(conversation["currentMessage"])["userInputMessage"])
	text := stagedText(data)
	if len(retry.Body)+len(data)+(64<<10) > maxRetainedBytes {
		return nil, inference.Chat, failure(502, "kiro_fallback_limit")
	}
	if strings.TrimSpace(text) != "" {
		history := arr(conversation["history"])
		// The original current user turn becomes history before the replay assistant.
		historical := object{}
		for k, v := range current {
			if k != "userInputMessageContext" {
				historical[k] = v
			}
		}
		if c := obj(current["userInputMessageContext"]); c != nil && len(arr(c["toolResults"])) > 0 {
			historical["userInputMessageContext"] = object{"toolResults": c["toolResults"]}
		}
		history = append(history, object{"userInputMessage": historical}, object{"assistantResponseMessage": object{"content": text}})
		conversation["history"] = history
		current["content"] = completionRetry
		if c := obj(current["userInputMessageContext"]); c != nil {
			delete(c, "toolResults")
		}
	} else {
		current["content"] = joinText(str(current["content"]), completionRetry)
	}
	retry.Body, err = json.Marshal(payload)
	if err != nil {
		return nil, inference.Chat, err
	}
	if len(retry.Body) > maxRequestBytes {
		return nil, inference.Chat, failure(413, "kiro_request_too_large")
	}
	response, err = executor.Do(retryCtx, retry)
	if err != nil {
		return nil, inference.Chat, err
	}
	// Once a successful first leg has run, a refused fallback must not reenter
	// outer HTTP recovery and replay that leg or transfer credentials to a gateway.
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, err = a.Decode(retryCtx, response)
		return nil, inference.Chat, err
	}
	retryState := retryCtx.Value(stateKey{}).(*requestState)
	state.mu.Lock()
	streamID := state.streamID
	state.mu.Unlock()
	retryState.mu.Lock()
	retryState.streamID = streamID
	retryState.mu.Unlock()
	decoded, err = a.DecodeWithInput(retryCtx, in, response)
	if err != nil {
		response.Body.Close()
		return nil, inference.Chat, err
	}
	// CaptureState observes the final leg through the original prepared context.
	decoded.Body = &captureBody{ReadCloser: &prefixedBody{Reader: io.MultiReader(bytes.NewReader(stagedReasoning(data)), decoded.Body), Closer: decoded.Body}, source: retryCtx.Value(stateKey{}).(*requestState), destination: state}
	return decoded, inference.Chat, nil
}

func stagedText(data []byte) string {
	var text strings.Builder
	for _, line := range bytes.Split(data, []byte("\n")) {
		if !bytes.HasPrefix(line, []byte("data: ")) {
			continue
		}
		var event object
		if json.Unmarshal(line[6:], &event) != nil {
			continue
		}
		for _, choice := range arr(event["choices"]) {
			text.WriteString(str(obj(obj(choice)["delta"])["content"]))
		}
	}
	return text.String()
}

type captureBody struct {
	io.ReadCloser
	source, destination *requestState
	captured            bool
	priorMetering       []Metering
	priorPressure       *float64
}

func (b *captureBody) capture() {
	b.source.mu.Lock()
	defer b.source.mu.Unlock()
	b.destination.mu.Lock()
	defer b.destination.mu.Unlock()
	previous := b.destination.metadata
	b.destination.metadata = b.source.metadata
	if !b.captured {
		b.priorMetering = append([]Metering(nil), previous.Metering...)
		b.priorPressure = previous.ContextUsagePercentage
		b.captured = true
	}
	b.destination.metadata.Metering = append(append([]Metering(nil), b.priorMetering...), b.source.metadata.Metering...)
	if b.destination.metadata.ContextUsagePercentage == nil {
		b.destination.metadata.ContextUsagePercentage = b.priorPressure
	}
}
func (b *captureBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		b.capture()
	}
	return n, err
}
func (b *captureBody) Close() error { err := b.ReadCloser.Close(); b.capture(); return err }

// A replay ending in a delivered final answer is already complete. The pinned
// adapter returns locally here, without dispatch or an estimated token charge.
func (a *Adapter) localTerminal(ctx context.Context, in inference.AdapterInput) (*http.Response, inference.Wire, error) {
	id, err := identifier()
	if err != nil {
		return nil, inference.Chat, err
	}
	conversation := id
	if seed, ok := ctx.Value(continuationKey{}).(continuationSeed); ok {
		if seed.owner != in.Principal {
			return nil, inference.Chat, inference.ErrUnauthorized
		}
		conversation = seed.id
	}
	state := ctx.Value(stateKey{}).(*requestState)
	zero := int64(0)
	state.mu.Lock()
	state.built, state.owner, state.model = true, in.Principal, normalizeModel(in.Target.Model)
	state.binding = inputBinding(in)
	state.metadata = Metadata{ConversationID: conversation, Terminal: true, Usage: inference.Usage{InputTokens: &zero, OutputTokens: &zero}}
	state.mu.Unlock()
	event := object{"id": "chatcmpl-" + id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": normalizeModel(in.Target.Model), "choices": []any{object{"index": 0, "delta": object{}, "finish_reason": "stop"}}, "usage": object{"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0}}
	data, err := json.Marshal(event)
	if err != nil {
		return nil, inference.Chat, err
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(fmt.Sprintf("data: %s\n\ndata: [DONE]\n\n", data)))}, inference.Chat, nil
}

type prefixedBody struct {
	io.Reader
	io.Closer
}

func stagedReasoning(data []byte) []byte {
	var out bytes.Buffer
	for _, line := range bytes.Split(data, []byte("\n")) {
		if !bytes.HasPrefix(line, []byte("data: ")) {
			continue
		}
		var event object
		if json.Unmarshal(line[6:], &event) != nil {
			continue
		}
		choices := []any{}
		for _, item := range arr(event["choices"]) {
			choice := obj(item)
			delta := obj(choice["delta"])
			kept := object{}
			for _, key := range []string{"reasoning_content", "kiro_reasoning"} {
				if value, ok := delta[key]; ok {
					kept[key] = value
				}
			}
			if len(kept) > 0 {
				choices = append(choices, object{"index": choice["index"], "delta": kept, "finish_reason": nil})
			}
		}
		if len(choices) > 0 {
			event["choices"] = choices
			delete(event, "usage")
			b, _ := json.Marshal(event)
			fmt.Fprintf(&out, "data: %s\n\n", b)
		}
	}
	return out.Bytes()
}
