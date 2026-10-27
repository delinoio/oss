// SPDX-License-Identifier: Apache-2.0
// Event mapping adapted from opencodex; see LICENSE.upstream.
package cursor

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"github.com/delinoio/oss/packages/aiprouter/inference"
	"math"
	"strings"
	"time"
	"unicode/utf8"
)

type EventKind string

const (
	Text        EventKind = "text"
	Thinking    EventKind = "thinking"
	ToolCall    EventKind = "tool_call"
	UsageUpdate EventKind = "usage"
	Done        EventKind = "done"
)

type Event struct {
	Kind                      EventKind
	Text, ID, Name, Arguments string
	Usage                     inference.Usage
	// ContextTokens is an absolute checkpoint measurement, never input billing.
	ContextTokens *int64
}
type openCall struct {
	Name, Arguments string
	AwaitingNative  bool
}
type pendingExec struct {
	ID     uint64
	ExecID string
}

// Session is request-local and has no I/O or global caches. The central executor
// owns synchronization, cancellation, pacing and all physical sends.
type Session struct {
	envelope                      envelopeFilter
	guardEcho, observeReplay      bool
	midstreamEcho                 bool
	invalidArgument, recoverySafe bool
	model, conversationID         string
	owner                         Owner
	tools                         map[string]tool
	open                          map[string]*openCall
	completed                     map[string]bool
	pending                       map[string]pendingExec
	callIDs                       map[string]string
	history                       []historyMessage
	blobs                         map[string][]byte
	blobBytes                     int
	requestContext, checkpoint    []byte
	output, contextTokens         *int64
	ended, terminal, visible      bool
	frames                        int
	pendingText                   string
	guardPending                  string
	guardDone, guardRouting       bool
	fallback                      []Event
	sawRealTool                   bool
}

func newSession(model string, tools []tool) *Session {
	s := &Session{model: model, tools: map[string]tool{}, open: map[string]*openCall{}, completed: map[string]bool{}, pending: map[string]pendingExec{}, blobs: map[string][]byte{}, callIDs: map[string]string{}}
	for _, t := range tools {
		s.tools[t.WireName] = t
		if !strings.HasPrefix(strings.TrimPrefix(strings.ToLower(model), "cursor/"), "claude-") && (t.WireName == "exec" || t.WireName == "exec_command" || t.WireName == "shell_command") {
			s.guardRouting = true
		}
	}
	return s
}

// Handle consumes one decoded Connect frame. Replies are unframed protobuf
// messages for authorized HTTP/2 writes or EncodeBidiAppend HTTP/1 requests.
// A reply is not permission to send: the host must check current authorization.
func (s *Session) Handle(ctx context.Context, f Frame) (events []Event, replies [][]byte, err error) {
	if err = ctx.Err(); err != nil {
		return
	}
	if s.terminal {
		err = protocolError("cursor_message_after_terminal")
		return
	}
	defer func() {
		if err != nil {
			s.terminal = true
		}
	}()
	s.frames++
	if s.frames > 100000 {
		err = protocolError("cursor_frame_count_limit")
		return
	}
	if len(f.Payload) > MaxFrameBytes || f.Flags & ^byte(3) != 0 {
		err = protocolError("cursor_invalid_frame")
		return
	}
	if f.Flags&2 != 0 {
		var end struct {
			Error *struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if len(f.Payload) == 0 || f.Payload[0] != '{' || !json.Valid(f.Payload) || json.Unmarshal(f.Payload, &end) != nil {
			err = protocolError("cursor_invalid_end_stream")
			return
		}
		if end.Error != nil {
			s.invalidArgument = end.Error.Code == "invalid_argument"
			err = ClassifyError(end.Error.Code, end.Error.Message, nil)
			return
		}
		events, err = s.finish(true)
		return
	}
	m, e := parse(f.Payload)
	if e != nil {
		err = e
		return
	}
	for _, f := range m {
		if f.wire != 2 {
			err = protocolError("cursor_invalid_server_message")
			return
		}
		switch f.number {
		case 1:
			if s.ended {
				err = protocolError("cursor_update_after_turn_end")
				return
			}
			events, err = s.interaction(f.data)
			return
		case 2:
			events, replies, err = s.exec(f.data)
			return
		case 3:
			checkpoint, e := parse(f.data)
			if e != nil {
				err = e
				return
			}
			tokens, e := checkpoint.sub(5)
			if e != nil {
				err = e
				return
			}
			if v := tokens.get(1); v.number != 0 {
				if v.wire != 0 || v.value > math.MaxUint32 {
					err = protocolError("cursor_invalid_usage")
					return
				}
				n := int64(v.value)
				if s.contextTokens == nil || n > *s.contextTokens {
					s.contextTokens = &n
				}
			}
			if len(f.data) > MaxStateBytes {
				err = protocolError("cursor_checkpoint_limit")
				return
			}
			s.checkpoint = append(s.checkpoint[:0], f.data...)
			return
		case 4:
			var reply []byte
			reply, err = s.kv(f.data)
			if err == nil {
				replies = [][]byte{reply}
			}
			return
		case 5, 7:
			err = &inference.Error{Status: 501, Code: "cursor_unsupported_interaction"}
			return
		}
	}
	return
}

func (s *Session) interaction(b []byte) ([]Event, error) {
	m, err := parse(b)
	if err != nil {
		return nil, err
	}
	for _, f := range m {
		u, err := parse(f.data)
		if err != nil || f.wire != 2 {
			return nil, protocolError("cursor_invalid_interaction")
		}
		switch f.number {
		case 1, 4:
			if !utf8.Valid(u.get(1).data) {
				return nil, protocolError("cursor_invalid_text")
			}
			text := u.str(1)
			if text == "" {
				return nil, nil
			}
			if f.number == 1 {
				return s.textEvents(text)
			}
			s.visible = true
			kind := Text
			if f.number == 4 {
				kind = Thinking
			}
			return []Event{{Kind: kind, Text: text}}, nil
		case 2, 3, 7:
			id := u.str(1)
			if id == "" || len(id) > 4096 {
				return nil, protocolError("cursor_invalid_call_id")
			}
			if s.completed[id] {
				return nil, nil
			}
			tc, e := u.sub(2)
			if e != nil {
				return nil, e
			}
			mc, e := tc.sub(15)
			if e != nil {
				return nil, e
			}
			args, e := mc.sub(1)
			if e != nil {
				return nil, e
			}
			name := args.str(1)
			if name == "" {
				name = args.str(5)
			}
			if len(tc) > 0 && len(mc) == 0 {
				return nil, &inference.Error{Status: 501, Code: "cursor_native_execution_denied"}
			}
			wasOpen := s.open[id] != nil
			if name != "" {
				s.sawRealTool = true
				s.fallback = nil
				if e := s.start(id, name); e != nil {
					return nil, e
				}
			}
			call := s.open[id]
			if call == nil {
				return nil, protocolError("cursor_unknown_tool_call")
			}
			if f.number == 7 {
				text := u.str(3)
				if len(text) > MaxArgumentBytes {
					return nil, protocolError("cursor_arguments_too_large")
				}
				total := len(text)
				for key, other := range s.open {
					if key != id {
						total += len(other.Arguments)
					}
				}
				if total > MaxStateBytes {
					return nil, protocolError("cursor_argument_budget")
				}
				// Cursor partial args are cumulative, not append-only deltas.
				if len(text) >= len(call.Arguments) {
					call.Arguments = text
				}
				return nil, nil
			}
			if f.number == 3 {
				encoded, has, e := s.arguments(args, call.Name)
				if e != nil {
					return nil, e
				}
				if !has {
					encoded = call.Arguments
				}
				if encoded == "" && (wasOpen && name != "" || s.tools[call.Name].Freeform) {
					call.AwaitingNative = true
					return nil, nil
				}
				if encoded == "" {
					encoded = "{}"
				}
				return s.commit(id, encoded)
			}
			return nil, nil
		case 8:
			v := u.get(1)
			if v.number != 0 && (v.wire != 0 || v.value > math.MaxInt32) {
				return nil, protocolError("cursor_invalid_usage")
			}
			if s.output == nil {
				n := int64(0)
				s.output = &n
			}
			if *s.output > math.MaxInt64-int64(v.value) {
				return nil, protocolError("cursor_usage_overflow")
			}
			*s.output += int64(v.value)
			return []Event{{Kind: UsageUpdate, Usage: inference.Usage{OutputTokens: cloneInt(s.output)}}}, nil
		case 14:
			if len(s.open) > 0 {
				return nil, protocolError("cursor_incomplete_tool_call")
			}
			s.ended = true
			return nil, nil
		}
	}
	return nil, nil
}

func (s *Session) start(id, name string) error {
	name = strings.TrimPrefix(name, "mcp_"+toolProvider+"_")
	if _, ok := s.tools[name]; !ok {
		return protocolError("cursor_unknown_tool")
	}
	if len(s.completed)+len(s.open) >= MaxToolCalls && s.open[id] == nil {
		return protocolError("cursor_tool_call_limit")
	}
	if c := s.open[id]; c != nil {
		if c.Name != name {
			return protocolError("cursor_tool_name_changed")
		}
		return nil
	}
	s.open[id] = &openCall{Name: name}
	return nil
}
func (s *Session) arguments(args message, name string) (string, bool, error) {
	out := map[string]any{}
	total := 0
	for _, f := range args {
		if f.number != 2 {
			continue
		}
		total += len(f.data)
		if total > MaxArgumentBytes {
			return "", false, protocolError("cursor_arguments_too_large")
		}
		entry, err := parse(f.data)
		if err != nil {
			return "", false, err
		}
		raw := entry.get(2).data
		v, err := decodeValue(raw, 0)
		if err != nil {
			if !utf8.Valid(raw) {
				return "", false, protocolError("cursor_invalid_argument_value")
			}
			var decoded any
			if json.Unmarshal(raw, &decoded) == nil {
				v = decoded
			} else {
				v = string(raw)
			}
		}
		if text, ok := v.(string); ok && !s.tools[name].Freeform {
			var decoded any
			if json.Unmarshal([]byte(text), &decoded) == nil {
				v = decoded
			}
		}
		if s.tools[name].Freeform {
			if _, ok := v.(string); !ok {
				v = string(raw)
			}
		}
		out[entry.str(1)] = v
	}
	if len(out) == 0 {
		return "", false, nil
	}
	b, err := json.Marshal(out)
	return string(b), true, err
}
func (s *Session) commit(id, args string) ([]Event, error) {
	c := s.open[id]
	if c == nil {
		return nil, protocolError("cursor_unknown_tool_call")
	}
	if len(args) > MaxArgumentBytes {
		return nil, protocolError("cursor_arguments_too_large")
	}
	var obj map[string]any
	if json.Unmarshal([]byte(args), &obj) != nil || obj == nil {
		return nil, protocolError("cursor_invalid_tool_arguments")
	}
	t := s.tools[c.Name]
	args, obj = normalizeArguments(args, obj, t)
	if t.Synthetic {
		patch, err := structuredPatch(t.WireName, obj)
		if err != nil {
			return nil, err
		}
		encoded, _ := json.Marshal(map[string]any{"input": patch})
		args = string(encoded)
		if len(args) > MaxArgumentBytes {
			return nil, protocolError("cursor_arguments_too_large")
		}
	}
	if t.Freeform {
		if _, ok := obj["input"].(string); !ok {
			return nil, protocolError("cursor_invalid_freeform_arguments")
		}
	}
	delete(s.open, id)
	s.completed[id] = true
	s.visible = true
	visibleID := id
	if strings.ContainsAny(id, "\r\n") || strings.HasPrefix(id, "aipcursor_") {
		visibleID = "aipcursor_" + base64.RawURLEncoding.EncodeToString([]byte(id))
	}
	s.callIDs[visibleID] = id
	return []Event{{Kind: ToolCall, ID: visibleID, Name: t.Name, Arguments: args}}, nil
}

func (s *Session) exec(b []byte) ([]Event, [][]byte, error) {
	m, err := parse(b)
	if err != nil {
		return nil, nil, err
	}
	if v := m.get(1); v.wire != 0 || v.value > math.MaxUint32 {
		return nil, nil, protocolError("cursor_invalid_exec_id")
	}
	id := m.get(1).value
	execID := m.str(15)
	if f := m.get(10); f.number != 0 {
		// This is protocol context only. Never report the server's filesystem,
		// shell, environment, workspace or other local execution capabilities.
		result := pb(1, pb(1, s.requestContext))
		return nil, [][]byte{pb(2, join(pv(1, id), ps(15, execID), pb(10, result)))}, nil
	}
	if f := m.get(11); f.number != 0 {
		args, e := parse(f.data)
		if e != nil {
			return nil, nil, e
		}
		name := args.str(1)
		if name == "" {
			name = args.str(5)
		}
		name = strings.TrimPrefix(name, "mcp_"+toolProvider+"_")
		callID := args.str(3)
		if callID == "" || len(callID) > 4096 {
			return nil, nil, protocolError("cursor_invalid_call_id")
		}
		if _, ok := s.tools[name]; !ok {
			return nil, nil, protocolError("cursor_unknown_tool")
		}
		s.sawRealTool = true
		s.fallback = nil
		if old, ok := s.pending[callID]; ok && (old.ID != id || old.ExecID != execID) {
			return nil, nil, protocolError("cursor_conflicting_tool_execution")
		}
		s.pending[callID] = pendingExec{ID: id, ExecID: execID}
		if s.completed[callID] {
			return nil, nil, nil
		}
		if e := s.start(callID, name); e != nil {
			return nil, nil, e
		}
		encoded, has, e := s.arguments(args, name)
		if e != nil {
			return nil, nil, e
		}
		if !has {
			encoded = s.open[callID].Arguments
		}
		if encoded == "" {
			encoded = "{}"
		}
		events, e := s.commit(callID, encoded)
		return events, nil, e
	}
	return nil, nil, &inference.Error{Status: 501, Code: "cursor_native_execution_denied"}
}

// ToolResult serializes a real client result. It never fabricates a successful
// acknowledgement, executes the tool, or sends the result itself.
func (s *Session) ToolResult(ctx context.Context, callID, text string, isError bool) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if original, ok := s.callIDs[callID]; ok {
		callID = original
	}
	p, ok := s.pending[callID]
	if !ok {
		return nil, protocolError("cursor_unpaired_tool_result")
	}
	if len(text) > MaxArgumentBytes {
		return nil, protocolError("cursor_tool_result_too_large")
	}
	success := pb(1, pb(1, ps(1, text)))
	if isError {
		success = append(success, pv(2, 1)...)
	}
	result := pb(1, success)
	delete(s.pending, callID)
	return pb(2, join(pv(1, p.ID), ps(15, p.ExecID), pb(11, result))), nil
}

func (s *Session) kv(b []byte) ([]byte, error) {
	m, err := parse(b)
	if err != nil {
		return nil, err
	}
	id := m.get(1)
	if id.wire != 0 || id.value > math.MaxUint32 {
		return nil, protocolError("cursor_invalid_kv_id")
	}
	if f := m.get(2); f.number != 0 {
		args, e := parse(f.data)
		if e != nil {
			return nil, e
		}
		key := hex.EncodeToString(args.get(1).data)
		data, ok := s.blobs[key]
		if !ok {
			return nil, inference.ErrContinuation
		}
		return pb(3, join(pv(1, id.value), pb(2, pb(1, data)))), nil
	}
	if f := m.get(3); f.number != 0 {
		args, e := parse(f.data)
		if e != nil {
			return nil, e
		}
		data := args.get(2).data
		key := args.get(1).data
		hash := sha256.Sum256(data)
		if len(key) != 32 || hex.EncodeToString(key) != hex.EncodeToString(hash[:]) {
			return nil, protocolError("cursor_blob_digest_mismatch")
		}
		k := hex.EncodeToString(key)
		old := s.blobs[k]
		if len(data) > MaxBlobBytes || s.blobBytes-len(old)+len(data) > MaxStateBytes || len(s.blobs) >= 4096 && old == nil {
			return nil, protocolError("cursor_blob_limit")
		}
		s.blobs[k] = append([]byte(nil), data...)
		s.blobBytes += len(data) - len(old)
		return pb(3, join(pv(1, id.value), pb(3, nil))), nil
	}
	return nil, protocolError("cursor_unknown_kv_operation")
}

func (s *Session) finish(cleanEnd bool) ([]Event, error) {
	s.terminal = true
	var events []Event
	if s.guardPending != "" {
		text, err := s.guardText("", true)
		if err != nil {
			return nil, err
		}
		if text != "" {
			released, err := s.textEvents(text)
			if err != nil {
				return nil, err
			}
			events = append(events, released...)
		}
	}
	if s.observeReplay {
		tail := s.envelope.finish()
		s.midstreamEcho = s.envelope.matched || s.envelope.unverified
		if tail != "" {
			released, err := s.parseTextEvents(tail)
			if err != nil {
				return nil, err
			}
			events = append(events, released...)
		}
	}
	if len(s.open) > 0 {
		return nil, protocolError("cursor_incomplete_tool_call")
	}
	if !s.ended && (!cleanEnd || !s.visible && len(s.fallback) == 0) {
		return nil, protocolError("cursor_stream_truncated")
	}
	s.pendingText = ""
	if !s.sawRealTool {
		for _, call := range s.fallback {
			if err := s.start(call.ID, call.Name); err != nil {
				return nil, err
			}
			committed, err := s.commit(call.ID, call.Arguments)
			if err != nil {
				return nil, err
			}
			events = append(events, committed...)
		}
	}
	s.fallback = nil
	return append(events, Event{Kind: Done, Usage: inference.Usage{OutputTokens: cloneInt(s.output)}, ContextTokens: cloneInt(s.contextTokens)}), nil
}
func cloneInt(p *int64) *int64 {
	if p == nil {
		return nil
	}
	n := *p
	return &n
}

// EOF distinguishes a completed turn from a transport-level close. A clean
// socket alone cannot turn partial content into a successful completion.
func (s *Session) EOF() ([]Event, error) {
	if s.terminal {
		return nil, nil
	}
	return s.finish(false)
}

func (s *Session) Snapshot(now time.Time) (*Continuation, error) {
	if len(s.checkpoint) == 0 && !nativeModel(s.model) || len(s.open) > 0 {
		return nil, inference.ErrContinuation
	}
	c := &Continuation{ReplayOnly: nativeModel(s.model), Owner: s.owner, ConversationID: s.conversationID, Checkpoint: append([]byte(nil), s.checkpoint...), Blobs: map[string][]byte{}, CallIDs: map[string]string{}, ExpiresAt: now.Add(15 * time.Minute)}
	for k, v := range s.blobs {
		c.Blobs[k] = append([]byte(nil), v...)
	}
	for k, v := range s.callIDs {
		c.CallIDs[k] = v
	}
	encoded, _ := json.Marshal(s.history)
	_ = json.Unmarshal(encoded, &c.History)
	if len(encoded)+len(c.Checkpoint)+s.blobBytes > MaxStateBytes {
		return nil, protocolError("cursor_checkpoint_limit")
	}
	return c, nil
}
func (s *Session) restore(c *Continuation) error {
	if c.Owner != s.owner || !time.Now().Before(c.ExpiresAt) || len(c.Checkpoint) == 0 && !c.ReplayOnly || c.ReplayOnly && !nativeModel(s.model) || len(c.Checkpoint) > MaxStateBytes || len(c.Blobs) > 4096 || len(c.CallIDs) > MaxToolCalls || c.ConversationID == "" || len(c.ConversationID) > 256 {
		return inference.ErrContinuation
	}
	if _, e := parse(c.Checkpoint); e != nil {
		return inference.ErrContinuation
	}
	historyBytes, _ := json.Marshal(c.History)
	total := len(c.Checkpoint) + len(historyBytes)
	for k, v := range c.Blobs {
		total += len(v)
		hash := sha256.Sum256(v)
		if len(v) > MaxBlobBytes || total > MaxStateBytes || hex.EncodeToString(hash[:]) != k {
			return inference.ErrContinuation
		}
	}
	for k, v := range c.Blobs {
		s.blobs[k] = append([]byte(nil), v...)
	}
	for k, v := range c.CallIDs {
		if len(k) > 8192 || len(v) > 4096 {
			return inference.ErrContinuation
		}
		s.callIDs[k] = v
	}
	s.blobBytes = total - len(c.Checkpoint) - len(historyBytes)
	s.conversationID = c.ConversationID
	s.checkpoint = append([]byte(nil), c.Checkpoint...)
	return nil
}
