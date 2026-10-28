// SPDX-License-Identifier: Apache-2.0
// Kiro event behavior adapted from opencodex; see LICENSE.upstream.
package kiro

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/delinoio/oss/packages/tairouter/inference"
)

type decodedBody struct {
	*io.PipeReader
	closeSource func()
	stop        func() bool
	done        <-chan struct{}
}

func (b *decodedBody) Close() error {
	b.stop()
	b.closeSource()
	err := b.PipeReader.Close()
	<-b.done
	return err
}

func (*Adapter) Decode(ctx context.Context, response *http.Response) (*http.Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if response == nil || response.Body == nil {
		return nil, failure(502, "kiro_missing_body")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		stop := context.AfterFunc(ctx, func() { response.Body.Close() })
		defer stop()
		b, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		response.Body.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil {
			return nil, err
		}
		f := ClassifyHTTPError(response.StatusCode, response.Header, b)
		return nil, failure(f.Status, f.Code)
	}
	state, _ := ctx.Value(stateKey{}).(*requestState)
	if state == nil {
		return nil, failure(500, "kiro_request_context_required")
	}
	state.mu.Lock()
	model, aliases, completion, built := state.model, state.aliases, state.completion, state.built
	definitions := state.definitions
	state.mu.Unlock()
	if !built {
		return nil, failure(500, "kiro_request_not_built")
	}
	id, err := identifier()
	if err != nil {
		return nil, err
	}
	state.mu.Lock()
	if state.streamID == "" {
		state.streamID = id
	} else {
		id = state.streamID
	}
	state.mu.Unlock()
	reader, writer := io.Pipe()
	var once sync.Once
	closeSource := func() { once.Do(func() { response.Body.Close() }) }
	stop := context.AfterFunc(ctx, func() { closeSource(); writer.CloseWithError(ctx.Err()) })
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer stop()
		defer closeSource()
		s := streamState{ctx: ctx, out: writer, id: "chatcmpl-" + id, created: time.Now().Unix(), model: model, aliases: aliases, definitions: definitions, request: state, completionRequired: completion, textFallback: ctx.Value(fallbackKey{}) == true, tools: map[string]*streamTool{}, usage: object{}}
		err := s.run(response.Body)
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		state.mu.Lock()
		state.metadata.Terminal = err == nil
		if err != nil {
			state.metadata.FailureCode = "kiro_stream_failed"
			var typed *inference.Error
			if errors.As(err, &typed) {
				state.metadata.FailureCode = typed.Code
			}
		}
		state.mu.Unlock()
		if err != nil && ctx.Err() == nil {
			var e *inference.Error
			if !errors.As(err, &e) {
				e = &inference.Error{Status: 502, Code: "kiro_stream_truncated"}
			}
			// No DONE after an error. The reader error also protects consumers that
			// ignore SSE error events. Never reflect upstream content into this frame.
			_ = s.emit(object{"error": object{"code": e.Code, "type": "upstream_error", "message": e.Code, "status": e.Status}, "usage": s.usage})
		}
		writer.CloseWithError(err)
	}()
	copy := *response
	copy.Header = response.Header.Clone()
	copy.Header.Set("Content-Type", "text/event-stream")
	copy.Header.Del("Content-Length")
	copy.Header.Del("Content-Encoding")
	copy.ContentLength = -1
	copy.Body = &decodedBody{reader, closeSource, stop, done}
	return &copy, nil
}

type streamTool struct {
	id, name string
	input    strings.Builder
	stopped  bool
}
type streamState struct {
	ctx                   context.Context
	out                   io.Writer
	model                 string
	id                    string
	created               int64
	aliases               map[string]string
	definitions           map[string]ToolDefinition
	request               *requestState
	completionRequired    bool
	textFallback          bool
	tools                 map[string]*streamTool
	order                 []string
	active                string
	usage                 object
	stopReason            string
	contextMeasured       bool
	sawText, sawReasoning bool
	retained              int
	answer                string
	answerSeen            bool
	thinking              bool
	textPending           string
	deferredText          strings.Builder
}

func (s *streamState) emit(v object) error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(b) > 2<<20 {
		return failure(502, "kiro_output_event_limit")
	}
	_, err = fmt.Fprintf(s.out, "data: %s\n\n", b)
	return err
}
func (s *streamState) chunk(delta object, finish any) error {
	return s.emit(object{"id": s.id, "created": s.created, "object": "chat.completion.chunk", "model": s.model, "choices": []any{object{"index": 0, "delta": delta, "finish_reason": finish}}})
}

func (s *streamState) run(source io.Reader) error {
	for {
		if err := s.ctx.Err(); err != nil {
			return err
		}
		f, err := readFrame(source)
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if s.retained > maxRetainedBytes-len(f.payload) {
			return failure(502, "kiro_stream_byte_limit")
		}
		s.retained += len(f.payload)
		kind := f.headers[":message-type"]
		if kind == "exception" || kind == "error" {
			h := http.Header{}
			for k, v := range f.headers {
				h.Set(k, v)
			}
			c := ClassifyHTTPError(0, h, f.payload)
			return failure(c.Status, c.Code)
		}
		if kind != "event" {
			return failure(502, "kiro_invalid_message_type")
		}
		typeName := f.headers[":event-type"]
		switch typeName {
		case "assistantResponseEvent", "reasoningContentEvent", "toolUseEvent", "metadataEvent", "messageMetadataEvent", "initial-response", "contextUsageEvent", "meteringEvent", "invalidStateEvent", "error":
		default:
			continue // Future event payloads are opaque, including invalid JSON.
		}
		var m object
		if json.Unmarshal(f.payload, &m) != nil || m == nil {
			return failure(502, "kiro_invalid_event_json")
		}
		if err := s.event(typeName, m); err != nil {
			return err
		}
	}
	if err := s.text("", true); err != nil {
		return err
	}
	if len(s.order) == 0 && !s.answerSeen {
		if err := s.recoverTextTools(); err != nil {
			return err
		}
	}
	for _, id := range s.order {
		if !s.tools[id].stopped {
			return failure(502, "kiro_incomplete_tool")
		}
	}
	stop := strings.ToUpper(strings.TrimSpace(s.stopReason))
	finish := "stop"
	switch stop {
	case "MODEL_CONTEXT_WINDOW_EXCEEDED":
		return failure(400, "context_length_exceeded")
	case "MAX_TOKENS":
		finish = "length"
	case "CONTENT_FILTERED", "GUARDRAIL_INTERVENED":
		finish = "content_filter"
	case "MALFORMED_TOOL_USE":
		return failure(502, "kiro_malformed_tool_use")
	case "MALFORMED_MODEL_OUTPUT":
		return failure(502, "kiro_malformed_model_output")
	case "TOOL_USE":
		if len(s.order) == 0 {
			return failure(502, "kiro_tool_use_without_call")
		}
	case "END_TURN", "STOP_SEQUENCE":
	case "":
		if !s.answerSeen && len(s.order) == 0 {
			return failure(502, "kiro_missing_terminal_metadata")
		}
	default:
		return failure(502, "kiro_unknown_stop_reason")
	}
	if s.answerSeen {
		if len(s.order) != 1 {
			return failure(502, "kiro_completion_with_other_tools")
		}
		if err := s.chunk(object{"content": s.answer}, nil); err != nil {
			return err
		}
	} else if len(s.order) > 0 {
		finish = "tool_calls"
	} else if s.completionRequired && !s.textFallback && finish == "stop" {
		// This requires one centrally admitted fallback send. Returning success
		// would turn progress commentary into a final answer.
		if err := s.flushCommentary(); err != nil {
			return err
		}
		return failure(502, "kiro_completion_fallback_required")
	} else if !s.sawText && !s.sawReasoning {
		return failure(502, "kiro_empty_stream")
	}
	if !s.answerSeen {
		if err := s.flushCommentary(); err != nil {
			return err
		}
	}
	if len(s.usage) > 0 {
		if err := s.emit(object{"id": s.id, "created": s.created, "model": s.model, "object": "chat.completion.chunk", "choices": []any{}, "usage": s.usage}); err != nil {
			return err
		}
	}
	if err := s.chunk(object{}, finish); err != nil {
		return err
	}
	_, err := io.WriteString(s.out, "data: [DONE]\n\n")
	return err
}

func optionalString(m object, key string) (string, error) {
	v := m[key]
	if v == nil {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", failure(502, "kiro_invalid_event_field")
	}
	return s, nil
}

func (s *streamState) event(kind string, m object) error {
	// Native metadata stop reasons must retain their own terminal mapping.
	if kind != "metadataEvent" || m["stopReason"] == nil {
		if m["truncated"] == true {
			return failure(502, "kiro_upstream_truncated")
		}
		for _, key := range []string{"finish_reason", "finishReason", "stop_reason", "stopReason", "completionReason", "reason"} {
			if truncationPattern.MatchString(str(m[key])) {
				return failure(502, "kiro_upstream_truncated")
			}
		}
	}
	switch kind {
	case "assistantResponseEvent":
		text, err := optionalString(m, "content")
		if err != nil {
			return err
		}
		model, err := optionalString(m, "modelId")
		if err != nil {
			return err
		}
		if model != "" {
			s.model = model
		}
		if s.answerSeen && text != "" {
			return failure(502, "kiro_output_after_completion")
		}
		return s.text(text, false)
	case "reasoningContentEvent":
		text, err := optionalString(m, "text")
		if err != nil {
			return err
		}
		signature, err := optionalString(m, "signature")
		if err != nil {
			return err
		}
		redacted, err := optionalString(m, "redactedContent")
		if err != nil {
			return err
		}
		if s.answerSeen && (text != "" || signature != "" || redacted != "") {
			return failure(502, "kiro_output_after_completion")
		}
		delta := object{}
		if text != "" {
			delta["reasoning_content"] = text
		}
		if signature != "" {
			delta["kiro_reasoning"] = "signature:" + signature
		} else if redacted != "" {
			delta["kiro_reasoning"] = redacted
		}
		if len(delta) > 0 {
			s.sawReasoning = true
			return s.chunk(delta, nil)
		}
	case "toolUseEvent":
		return s.tool(m)
	case "metadataEvent":
		reason, err := optionalString(m, "stopReason")
		if err != nil {
			return err
		}
		if reason != "" {
			if s.stopReason != "" && s.stopReason != reason {
				return failure(502, "kiro_conflicting_stop_reason")
			}
			s.stopReason = reason
		}
		if value, ok := m["contextUsagePercentage"]; ok {
			if !finiteNumber(value) {
				return failure(502, "kiro_invalid_context_usage")
			}
			n := value.(float64)
			if !s.contextMeasured {
				s.record(func(metadata *Metadata) { metadata.ContextUsagePercentage = &n })
			}
		}
		if usage, ok := m["tokenUsage"]; ok && usage != nil {
			return s.tokenUsage(usage)
		}
	case "contextUsageEvent":
		if !finiteNumber(m["contextUsagePercentage"]) {
			return failure(502, "kiro_invalid_context_usage")
		}
		n := m["contextUsagePercentage"].(float64)
		s.contextMeasured = true
		s.record(func(metadata *Metadata) { metadata.ContextUsagePercentage = &n })
	case "meteringEvent":
		if _, ok := m["unit"].(string); !ok {
			return failure(502, "kiro_invalid_metering")
		}
		v := m["usage"]
		if v == nil {
			v = m["amount"]
		}
		n, ok := v.(float64)
		if !ok || !finiteNumber(v) || n < 0 {
			return failure(502, "kiro_invalid_metering")
		}
		if _, err := optionalString(m, "unitPlural"); err != nil {
			return err
		}
		if len(str(m["unit"])) > 128 || len(str(m["unitPlural"])) > 128 {
			return failure(502, "kiro_metering_unit_limit")
		}
		if s.request != nil {
			s.request.mu.Lock()
			meta := &s.request.metadata
			found := false
			for i := range meta.Metering {
				if meta.Metering[i].Unit == str(m["unit"]) {
					meta.Metering[i].Usage += n
					if math.IsInf(meta.Metering[i].Usage, 0) {
						s.request.mu.Unlock()
						return failure(502, "kiro_metering_overflow")
					}
					found = true
					break
				}
			}
			if !found {
				if len(meta.Metering) >= 64 {
					s.request.mu.Unlock()
					return failure(502, "kiro_metering_limit")
				}
				meta.Metering = append(meta.Metering, Metering{str(m["unit"]), str(m["unitPlural"]), n})
			}
			s.request.mu.Unlock()
		}
		// Credits and context pressure are not token counts. Do not invent usage.
	case "messageMetadataEvent", "initial-response":
		for _, key := range []string{"conversationId", "utteranceId"} {
			if _, err := optionalString(m, key); err != nil {
				return err
			}
		}
		id := str(m["conversationId"])
		if id == "" {
			id = str(m["utteranceId"])
		}
		if conversationPattern.MatchString(id) {
			s.record(func(metadata *Metadata) { metadata.ConversationID = id })
		}
	case "invalidStateEvent":
		return failure(502, "kiro_invalid_state")
	case "error":
		b, _ := json.Marshal(m)
		c := ClassifyHTTPError(0, nil, b)
		return failure(c.Status, c.Code)
	}
	return nil
}

func finiteNumber(v any) bool { n, ok := v.(float64); return ok && !math.IsInf(n, 0) && !math.IsNaN(n) }
func count(m object, key string, required bool) (int64, bool, error) {
	v, exists := m[key]
	if !exists && !required {
		return 0, false, nil
	}
	n, ok := v.(float64)
	if !ok || !finiteNumber(v) || n < 0 || n > 9007199254740991 || math.Trunc(n) != n {
		return 0, false, failure(502, "kiro_invalid_token_usage")
	}
	return int64(n), true, nil
}
func (s *streamState) tokenUsage(value any) error {
	m := obj(value)
	if m == nil {
		return failure(502, "kiro_invalid_token_usage")
	}
	input, _, err := count(m, "uncachedInputTokens", true)
	if err != nil {
		return err
	}
	output, _, err := count(m, "outputTokens", true)
	if err != nil {
		return err
	}
	total, _, err := count(m, "totalTokens", true)
	if err != nil {
		return err
	}
	read, hasRead, err := count(m, "cacheReadInputTokens", false)
	if err != nil {
		return err
	}
	write, hasWrite, err := count(m, "cacheWriteInputTokens", false)
	if err != nil {
		return err
	}
	if input+read+write > 9007199254740991 {
		return failure(502, "kiro_invalid_token_usage")
	}
	s.usage = object{"prompt_tokens": input + read + write, "completion_tokens": output, "total_tokens": total}
	if hasRead {
		s.usage["prompt_tokens_details"] = object{"cached_tokens": read}
	}
	if hasWrite {
		s.usage["cache_creation_input_tokens"] = write
	}
	s.record(func(metadata *Metadata) {
		sum := input + read + write
		metadata.Usage = inference.Usage{InputTokens: &sum, OutputTokens: &output}
		if hasRead {
			metadata.Usage.CacheReadTokens = &read
		}
		if hasWrite {
			metadata.Usage.CacheWriteTokens = &write
		}
	})
	return nil
}

func (s *streamState) record(update func(*Metadata)) {
	if s.request == nil {
		return
	}
	s.request.mu.Lock()
	defer s.request.mu.Unlock()
	update(&s.request.metadata)
}

func (s *streamState) tool(m object) error {
	id, err := optionalString(m, "toolUseId")
	if err != nil {
		return err
	}
	name, err := optionalString(m, "name")
	if err != nil {
		return err
	}
	input, err := optionalString(m, "input")
	if err != nil {
		return err
	}
	stop := false
	if v := m["stop"]; v != nil {
		var ok bool
		stop, ok = v.(bool)
		if !ok {
			return failure(502, "kiro_invalid_tool_stop")
		}
	}
	if id == "" {
		id = s.active
	}
	if id == "" || len(id) > 64 || invalidName.MatchString(id) {
		return failure(502, "kiro_invalid_tool_id")
	}
	t := s.tools[id]
	if t == nil {
		if s.answerSeen || len(s.order) >= 128 || name == "" {
			return failure(502, "kiro_invalid_tool_start")
		}
		if name != completionTool {
			if _, ok := s.aliases[name]; !ok {
				return failure(502, "kiro_unadvertised_tool")
			}
		}
		if name == completionTool && !s.completionRequired {
			return failure(502, "kiro_unadvertised_completion")
		}
		t = &streamTool{id: id, name: name}
		s.tools[id] = t
		s.order = append(s.order, id)
	}
	if t.stopped || (name != "" && name != t.name) {
		return failure(502, "kiro_invalid_tool_fragment")
	}
	s.active = id
	if t.input.Len()+len(input) > 1<<20 {
		return failure(502, "kiro_tool_argument_limit")
	}
	t.input.WriteString(input)
	if !stop {
		return nil
	}
	var args object
	arguments := t.input.String()
	if arguments == "" {
		arguments = "{}"
	}
	if json.Unmarshal([]byte(arguments), &args) != nil || args == nil {
		return failure(502, "kiro_invalid_tool_arguments")
	}
	if definition := s.definitions[t.name]; definition.Kind == CustomTool {
		if _, ok := args["input"].(string); !ok {
			return failure(502, "kiro_invalid_custom_arguments")
		}
	}
	t.stopped = true
	s.active = ""
	if t.name == completionTool {
		answer, ok := args["answer"].(string)
		if !ok || strings.TrimSpace(answer) == "" || s.answerSeen {
			return failure(502, "kiro_invalid_completion")
		}
		s.answer, s.answerSeen = answer, true
		return nil
	}
	if err := s.flushCommentary(); err != nil {
		return err
	}
	index := 0
	for i, item := range s.order {
		if item == id {
			index = i
			break
		}
	}
	call := object{"index": index, "id": id, "type": "function", "function": object{"name": s.aliases[t.name], "arguments": arguments}}
	if definition, ok := s.definitions[t.name]; ok {
		call["provider_metadata"] = object{"tool_kind": string(definition.Kind), "tool_name": definition.Name, "tool_namespace": definition.Namespace}
	}
	return s.chunk(object{"tool_calls": []any{call}}, nil)
}

// Keep only a possible tag prefix across frames, so split thinking tags do not
// leak into visible text. An unclosed thinking block cannot become a success.
func (s *streamState) text(fragment string, final bool) error {
	s.textPending += fragment
	for s.textPending != "" {
		tag := "<thinking>"
		if s.thinking {
			tag = "</thinking>"
		}
		if i := strings.Index(s.textPending, tag); i >= 0 {
			if err := s.textChunk(s.textPending[:i]); err != nil {
				return err
			}
			s.textPending = s.textPending[i+len(tag):]
			s.thinking = !s.thinking
			continue
		}
		keep := 0
		if !final {
			for n := 1; n < len(tag) && n <= len(s.textPending); n++ {
				if strings.HasSuffix(s.textPending, tag[:n]) {
					keep = n
				}
			}
		}
		cut := len(s.textPending) - keep
		if err := s.textChunk(s.textPending[:cut]); err != nil {
			return err
		}
		s.textPending = s.textPending[cut:]
		break
	}
	if final && s.thinking {
		return failure(502, "kiro_unclosed_thinking")
	}
	return nil
}
func (s *streamState) textChunk(text string) error {
	if text == "" {
		return nil
	}
	if s.thinking {
		s.sawReasoning = true
		return s.chunk(object{"reasoning_content": text}, nil)
	}
	s.sawText = true
	if s.completionRequired {
		s.deferredText.WriteString(text)
		return nil
	}
	return s.chunk(object{"content": text}, nil)
}

var truncationPattern = regexp.MustCompile(`(?i)length|max[_-]?tokens?|truncat|incomplete|context_length`)

func (s *streamState) flushCommentary() error {
	if s.deferredText.Len() == 0 {
		return nil
	}
	text := s.deferredText.String()
	s.deferredText.Reset()
	if len(s.order) > 0 {
		text = s.reconcileNativeText(text)
	}
	if text == "" {
		return nil
	}
	return s.chunk(object{"content": text}, nil)
}
