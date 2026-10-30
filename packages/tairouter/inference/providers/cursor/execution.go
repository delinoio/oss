// SPDX-License-Identifier: Apache-2.0
// HTTP/1 duplex behavior follows the pinned opencodex transport; see LICENSE.upstream.
package cursor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/delinoio/oss/packages/tairouter/inference"
)

var _ inference.ExecutingAdapter = (*Adapter)(nil)
var _ inference.StatefulAdapter = (*Adapter)(nil)
var _ inference.ContextPreparer = (*Adapter)(nil)

const appendTimeout = 30 * time.Second
const firstFrameTimeout = 30 * time.Second
const checkpointGrace = 1500 * time.Millisecond

type executionStateKey struct{}
type executionState struct {
	mu         sync.Mutex
	restored   *savedExecution
	captured   []byte
	savedID    string
	captureErr error
}
type savedExecution struct {
	Version      int
	Suspended    bool
	Continuation *Continuation
}

func (*Adapter) PrepareContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, executionStateKey{}, &executionState{})
}

func (*Adapter) RestoreState(ctx context.Context, in inference.AdapterInput, store inference.AdapterStateStore) (context.Context, error) {
	state := &executionState{}
	previous := in.PreviousResponseID
	if previous == "" {
		var b map[string]any
		if json.Unmarshal(in.Body, &b) == nil {
			previous = str(b["previous_response_id"])
		}
	}
	if previous != "" {
		if store == nil || len(previous) > 256 {
			return nil, inference.ErrContinuation
		}
		data, err := store.Load(ctx, previous)
		if err != nil {
			return nil, err
		}
		if len(data) > MaxStateBytes || json.Unmarshal(data, &state.restored) != nil || state.restored == nil || state.restored.Version != 1 || state.restored.Continuation == nil {
			return nil, inference.ErrContinuation
		}
	}
	return context.WithValue(ctx, executionStateKey{}, state), nil
}

// CaptureState aliases the already completed snapshot to the public Responses ID.
// The parent calls this only after successful delivery and an authorization check.
func (*Adapter) CaptureState(ctx context.Context, _ inference.AdapterInput, id string, store inference.AdapterStateStore) error {
	state, _ := ctx.Value(executionStateKey{}).(*executionState)
	if state == nil {
		return inference.ErrContinuation
	}
	state.mu.Lock()
	data := append([]byte(nil), state.captured...)
	err := state.captureErr
	savedID := state.savedID
	state.mu.Unlock()
	if err != nil {
		return err
	}
	// Chat streaming has no parent ResponseID; Execute saved its emitted ID.
	if id == "" || id == savedID {
		return nil
	}
	if len(data) == 0 || len(id) > 256 || store == nil {
		return inference.ErrContinuation
	}
	return store.Save(ctx, id, data, 15*time.Minute)
}

// Execute uses RunSSE for receiving and ordered BidiAppend for sending. All
// network operations, including protocol replies, go through the shared executor.
// There is no transport retry here: a missing acknowledgement is ambiguous.
func (a *Adapter) Execute(ctx context.Context, in inference.AdapterInput, x inference.AdapterExecutor, store inference.AdapterStateStore) (*http.Response, inference.Wire, error) {
	if x == nil {
		return nil, "", protocolError("cursor_executor_required")
	}
	state, _ := ctx.Value(executionStateKey{}).(*executionState)
	var requestBody map[string]any
	_ = json.Unmarshal(in.Body, &requestBody)
	if state == nil || state.restored == nil && (in.PreviousResponseID != "" || str(requestBody["previous_response_id"]) != "") {
		var err error
		ctx, err = a.RestoreState(ctx, in, store)
		if err != nil {
			return nil, "", err
		}
		state = ctx.Value(executionStateKey{}).(*executionState)
	}
	var saved *Continuation
	if state.restored != nil {
		saved = state.restored.Continuation
		if state.restored.Suspended {
			var body map[string]any
			if json.Unmarshal(in.Body, &body) != nil {
				return nil, "", inference.ErrContinuation
			}
			history, err := readHistory(body, in.Wire)
			if err != nil || len(history) == 0 || history[len(history)-1].Role != "tool" {
				return nil, "", inference.ErrContinuation
			}
		}
	}
	var continuityErr error
	if ctx.Value(recoveryConversationKey{}) == nil {
		previous := ""
		if saved != nil {
			previous = saved.ConversationID
		}
		ctx, continuityErr = retainedConversation(ctx, in, store, previous)
	}
	if continuityErr != nil {
		return nil, "", continuityErr
	}
	built, session, err := Prepare(ctx, in, saved)
	if err != nil {
		return nil, "", err
	}
	if retained, _ := ctx.Value(recoveryConversationKey{}).(string); saved != nil && retained != "" && retained != saved.ConversationID {
		// Validate the authenticated history prefix above before abandoning a
		// checkpoint whose conversation was rotated by an earlier guarded failure.
		fresh := in
		var body map[string]any
		_ = json.Unmarshal(in.Body, &body)
		delete(body, "previous_response_id")
		fresh.Body, _ = json.Marshal(body)
		built, session, err = Prepare(ctx, fresh, nil)
		if err != nil {
			return nil, "", err
		}
	}
	initial, err := ReadFrame(bytes.NewReader(built.Body))
	if err != nil {
		return nil, "", err
	}
	requestID := built.Headers.Get("X-Request-Id")
	runBody, err := BidiRunBody(requestID)
	if err != nil {
		return nil, "", err
	}
	life, cancelLife := context.WithCancel(ctx)
	if in.Target.AttemptTimeout > 0 {
		cancelLife()
		life, cancelLife = context.WithTimeout(ctx, in.Target.AttemptTimeout)
	}
	network, cancelNetwork := context.WithCancelCause(life)
	first := time.AfterFunc(firstFrameTimeout, func() { cancelNetwork(&inference.Error{Status: 504, Code: "cursor_first_frame_timeout"}) })
	runRequest := built
	runRequest.URL = cursorEndpoint(built.URL, RunSSEPath)
	runRequest.Body = runBody
	runRequest.Method = http.MethodPost
	upstream, err := x.Do(network, runRequest)
	if err != nil {
		first.Stop()
		cause := context.Cause(network)
		cancelNetwork(err)
		cancelLife()
		if upstream != nil && upstream.Body != nil {
			upstream.Body.Close()
		}
		if cause != nil {
			err = cause
		}
		return nil, "", err
	}
	if err = a.checkRunResponse(network, upstream); err != nil {
		first.Stop()
		cancelNetwork(err)
		cancelLife()
		return nil, "", err
	}
	reader, writer := io.Pipe()
	finished := make(chan struct{})
	body := &executionBody{PipeReader: reader, cancel: cancelLife, finished: finished}
	responseID := "chatcmpl-" + requestID
	go func() {
		defer close(finished)
		defer cancelLife()
		defer first.Stop()
		emit := chatEmitter(writer, session, responseID)
		// Cancellation must unblock writes even when the downstream stopped reading.
		callbackDone := make(chan struct{})
		stop := context.AfterFunc(life, func() { writer.CloseWithError(life.Err()); close(callbackDone) })
		defer func() {
			if !stop() {
				<-callbackDone
			}
		}()
		terminal, suspended, runErr := runDuplex(network, cancelNetwork, first, upstream, x, built, initial.Payload, session, emit, in)
		if runErr != nil {
			recoveryCtx, recoveryIn, recover, recoveryErr := a.recoveryInput(life, in, x, store, session, runErr)
			if recoveryErr != nil {
				runErr = recoveryErr
			}
			if recover {
				response, _, err := a.Execute(recoveryCtx, recoveryIn, x, store)
				if err == nil {
					_, err = io.Copy(writer, response.Body)
					closeErr := response.Body.Close()
					if err == nil {
						err = closeErr
					}
					child := recoveryCtx.Value(executionStateKey{}).(*executionState)
					child.mu.Lock()
					state.mu.Lock()
					state.captured = append([]byte(nil), child.captured...)
					state.captureErr = child.captureErr
					state.savedID = child.savedID
					state.mu.Unlock()
					child.mu.Unlock()
					writer.CloseWithError(err)
					return
				}
				runErr = err
			}
		}
		if rotateErr := rotateNextConversation(life, in, store, session, runErr); rotateErr != nil && runErr == nil {
			runErr = rotateErr
		}
		if runErr == nil {
			runErr = life.Err()
		}
		if runErr == nil {
			var options map[string]any
			_ = json.Unmarshal(in.Body, &options)
			if options["store"] != false {
				snapshot, snapshotErr := session.Snapshot(time.Now())
				var data []byte
				if snapshotErr == nil {
					data, snapshotErr = json.Marshal(savedExecution{Version: 1, Suspended: suspended, Continuation: snapshot})
					if len(data) > MaxStateBytes {
						snapshotErr = protocolError("cursor_checkpoint_limit")
					}
				}
				state.mu.Lock()
				state.captureErr = snapshotErr
				state.captured = data
				state.mu.Unlock()
				// A missing checkpoint does not make observed inference unsuccessful;
				// CaptureState reports it and later continuation must fail explicitly.
				if snapshotErr == nil && store != nil {
					runErr = store.Save(life, responseID, data, 15*time.Minute)
					if runErr == nil {
						state.mu.Lock()
						state.savedID = responseID
						state.mu.Unlock()
					}
				}
			}
		}
		if runErr == nil {
			runErr = emit(terminal)
		}
		if runErr != nil {
			state.mu.Lock()
			state.captureErr = runErr
			state.captured = nil
			state.mu.Unlock()
			var typed *inference.Error
			code := "cursor_upstream_error"
			if errors.As(runErr, &typed) {
				code = typed.Code
			}
			_ = writeSSE(writer, map[string]any{"error": map[string]any{"type": "upstream_error", "code": code}})
		}
		writer.CloseWithError(runErr)
	}()
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}, "Cache-Control": {"no-store"}, "X-Should-Retry": {"false"}}, Body: body, ContentLength: -1}, inference.Chat, nil
}

type executionBody struct {
	*io.PipeReader
	cancel   context.CancelFunc
	finished <-chan struct{}
	once     sync.Once
}

func (b *executionBody) Close() error {
	b.once.Do(func() { b.cancel(); b.PipeReader.CloseWithError(io.ErrClosedPipe) })
	<-b.finished
	return nil
}

func cursorEndpoint(runURL, path string) string {
	u, _ := url.Parse(runURL)
	u.Path = strings.TrimSuffix(u.Path, RunPath) + path
	u.RawPath = ""
	return u.String()
}
func nativeModel(model string) bool {
	m := strings.TrimPrefix(strings.ToLower(model), "cursor/")
	return strings.HasPrefix(m, "composer-") || m == "auto" || m == "default" || m == "auto-cost" || m == "auto-balance" || m == "auto-intelligence"
}

func (a *Adapter) checkRunResponse(ctx context.Context, r *http.Response) error {
	if r == nil || r.Body == nil {
		return protocolError("cursor_missing_response")
	}
	if r.StatusCode != http.StatusOK {
		if r.StatusCode >= 200 && r.StatusCode < 300 {
			r.Body.Close()
			return protocolError("cursor_invalid_run_status")
		}
		_, err := a.Decode(ctx, r)
		return err
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	encoding := strings.ToLower(r.Header.Get("Connect-Content-Encoding"))
	if err != nil || media != "application/connect+proto" || (encoding != "" && encoding != "identity" && encoding != "gzip") {
		r.Body.Close()
		return protocolError("cursor_invalid_content_type")
	}
	return nil
}

type incomingFrame struct {
	frame Frame
	err   error
}

// runDuplex owns and joins both workers before returning. BidiAppend waits never
// prevent the frame reader from receiving the context/KV messages it must answer.
func runDuplex(ctx context.Context, cancel context.CancelCauseFunc, first *time.Timer, r *http.Response, x inference.AdapterExecutor, built inference.AdapterRequest, initial []byte, s *Session, emit func([]Event) error, in inference.AdapterInput) (_ []Event, _ bool, resultErr error) {
	frames := make(chan incomingFrame, 1)
	sends := make(chan []byte)
	acks := make(chan error, 1)
	var workers sync.WaitGroup
	callbackDone := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { r.Body.Close(); close(callbackDone) })
	defer func() {
		var closeErr error
		if resultErr == nil {
			// A terminal frame can finish before HTTP EOF. Finalize the body
			// before our cleanup cancellation can classify that success as failure.
			closeErr = r.Body.Close()
			cancel(context.Canceled)
		} else {
			cancel(context.Canceled)
			closeErr = r.Body.Close()
		}
		workers.Wait()
		if !stop() {
			<-callbackDone
		}
		resultErr = errors.Join(resultErr, closeErr)
	}()
	workers.Add(2)
	go func() {
		defer workers.Done()
		for {
			f, err := ReadFrame(r.Body)
			select {
			case frames <- incomingFrame{f, err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	go func() {
		defer workers.Done()
		var seq uint64
		for {
			select {
			case <-ctx.Done():
				return
			case payload := <-sends:
				if ctx.Err() != nil {
					return
				}
				err := sendAppend(ctx, x, built, payload, seq)
				seq++
				select {
				case acks <- err:
				case <-ctx.Done():
					return
				}
				if err != nil {
					return
				}
			}
		}
	}()
	queue := [][]byte{initial}
	queuedBytes := len(initial)
	active := false
	var terminal []Event
	var semanticErr error
	var suspended bool
	var grace *time.Timer
	var graceC <-chan time.Time
	defer func() {
		if grace != nil {
			grace.Stop()
		}
	}()
	arm := func(d time.Duration) {
		if grace != nil {
			grace.Stop()
		}
		grace = time.NewTimer(d)
		graceC = grace.C
	}
	lastToolFrame, checkpointFrame := 0, 0
	extended := false
	toolGrace := 50 * time.Millisecond
	var options map[string]any
	_ = json.Unmarshal(in.Body, &options)
	if options["parallel_tool_calls"] == true && len(s.tools) > 1 {
		toolGrace = time.Duration(len(s.tools)) * 125 * time.Millisecond
		if toolGrace < 750*time.Millisecond {
			toolGrace = 750 * time.Millisecond
		}
		if toolGrace > 1800*time.Millisecond {
			toolGrace = 1800 * time.Millisecond
		}
	}
	for {
		if semanticErr != nil && !active && len(queue) == 0 {
			s.recoverySafe = true
			return nil, false, semanticErr
		}
		if terminal != nil && !active && len(queue) == 0 {
			return terminal, suspended, nil
		}
		var send chan []byte
		var next []byte
		if !active && len(queue) > 0 {
			send = sends
			next = queue[0]
		}
		read := frames
		if terminal != nil || semanticErr != nil {
			read = nil
		}
		select {
		case <-ctx.Done():
			return nil, false, context.Cause(ctx)
		case send <- next:
			queue = queue[1:]
			queuedBytes -= len(next)
			active = true
		case err := <-acks:
			active = false
			if err != nil {
				return nil, false, err
			}
		case <-graceC:
			graceC = nil
			if s.ended {
				var err error
				terminal, err = s.EOF()
				if err != nil {
					return nil, false, err
				}
				continue
			}
			if len(s.open) > 0 || len(s.completed) == 0 {
				continue
			}
			if checkpointFrame < lastToolFrame && !extended {
				extended = true
				arm(checkpointGrace)
				continue
			}
			// Expected tool suspension is the only locally synthesized terminal.
			// Never send a fabricated MCP success to release the remote worker.
			var err error
			terminal, err = s.finish(true)
			if err != nil {
				return nil, false, err
			}
			suspended = true
			if checkpointFrame < lastToolFrame {
				s.checkpoint = nil
			}
		case received := <-read:
			first.Stop()
			if received.err != nil {
				if received.err != io.EOF {
					var transportErr *inference.Error
					if errors.Is(received.err, inference.ErrUnavailable) || errors.As(received.err, &transportErr) && transportErr.Code == "hosted_response_too_large" {
						return nil, false, received.err
					}
					return nil, false, protocolError("cursor_stream_truncated")
				}
				var err error
				terminal, err = s.EOF()
				if err != nil {
					return nil, false, err
				}
				continue
			}
			f := received.frame
			toolFrame := false
			if f.Flags&2 == 0 {
				m, err := parse(f.Payload)
				if err != nil {
					return nil, false, err
				}
				if m.get(3).number != 0 {
					checkpointFrame = s.frames + 1
				}
				if e, _ := m.sub(2); e.get(11).number != 0 {
					toolFrame = true
				}
				if u, _ := m.sub(1); u.get(2).number != 0 || u.get(3).number != 0 || u.get(7).number != 0 {
					toolFrame = true
				}
			}
			events, replies, err := s.Handle(ctx, f)
			if err != nil {
				if semanticReason(s, err) != 0 {
					semanticErr = err
					continue
				}
				return nil, false, err
			}
			if toolFrame {
				lastToolFrame = s.frames
				if grace != nil {
					grace.Stop()
				}
				graceC = nil
				extended = false
			}
			for _, reply := range replies {
				queuedBytes += len(reply)
				if queuedBytes > MaxStateBytes || len(queue) >= 256 {
					return nil, false, protocolError("cursor_reply_queue_limit")
				}
				queue = append(queue, reply)
			}
			if s.terminal {
				terminal = events
				continue
			}
			if err = emit(events); err != nil {
				return nil, false, err
			}
			if s.ended {
				arm(500 * time.Millisecond)
			} else if toolFrame && len(s.completed) > 0 && len(s.open) == 0 {
				arm(toolGrace)
			}
		}
	}
}

func sendAppend(ctx context.Context, x inference.AdapterExecutor, built inference.AdapterRequest, payload []byte, seq uint64) error {
	body, err := EncodeBidiAppend(payload, built.Headers.Get("X-Request-Id"), seq)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, appendTimeout)
	defer cancel()
	req := built
	req.URL = cursorEndpoint(built.URL, BidiAppendPath)
	req.Body = body
	req.Method = http.MethodPost
	req.Headers = built.Headers.Clone()
	req.Headers.Set("Content-Type", "application/proto")
	r, err := x.Do(ctx, req)
	if err != nil {
		if r != nil && r.Body != nil {
			r.Body.Close()
		}
		return err
	}
	if r == nil || r.Body == nil {
		return protocolError("cursor_missing_append_response")
	}
	if r.StatusCode != http.StatusOK {
		if r.StatusCode >= 200 && r.StatusCode < 300 {
			r.Body.Close()
			return protocolError("cursor_invalid_append_status")
		}
		_, err = New().Decode(ctx, r)
		return err
	}
	callbackDone := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { r.Body.Close(); close(callbackDone) })
	defer func() {
		r.Body.Close()
		if !stop() {
			<-callbackDone
		}
	}()
	b, err := io.ReadAll(io.LimitReader(r.Body, 65537))
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return protocolError("cursor_append_read_failed")
	}
	if len(b) > 65536 {
		return protocolError("cursor_append_response_limit")
	}
	return nil
}
