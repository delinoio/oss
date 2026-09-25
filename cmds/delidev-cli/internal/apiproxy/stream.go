package apiproxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Frames are bounded before delivery so split transport writes cannot leak a
// reflected key, or turn a truncated native frame into an accepted completion.
func readFrame(reader *bufio.Reader) ([]byte, error) {
	var frame []byte
	lineStart := 0
	for {
		part, err := reader.ReadSlice('\n')
		if len(frame)+len(part) > maxBody {
			return nil, errInvalidDocument
		}
		frame = append(frame, part...)
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			if err == io.EOF && len(frame) > 0 {
				return nil, io.ErrUnexpectedEOF
			}
			return nil, err
		}
		line := bytes.TrimSuffix(bytes.TrimSuffix(frame[lineStart:], []byte("\n")), []byte("\r"))
		if len(line) == 0 {
			return frame, nil
		}
		lineStart = len(frame)
	}
}

func frameData(frame []byte) (string, []byte, error) {
	if !utf8.Valid(frame) {
		return "", nil, errInvalidDocument
	}
	var event string
	var data []byte
	for _, raw := range bytes.Split(frame, []byte("\n")) {
		line := bytes.TrimSuffix(raw, []byte("\r"))
		if len(line) == 0 || line[0] == ':' {
			continue
		}
		field, value, found := bytes.Cut(line, []byte(":"))
		if !found {
			value = nil
		}
		value = bytes.TrimPrefix(value, []byte(" "))
		switch string(field) {
		case "event":
			if event != "" || len(value) > 128 {
				return "", nil, errInvalidDocument
			}
			event = string(value)
		case "data":
			if data != nil {
				data = append(data, '\n')
			}
			data = append(data, value...)
		}
	}
	return event, data, nil
}

func relayStream(ctx context.Context, w http.ResponseWriter, body io.Reader, operation Operation, lease *Lease, guard secretGuard, correlation string) (started bool, result error) {
	reader := bufio.NewReaderSize(io.LimitReader(body, maxStream+1), 32<<10)
	controller := http.NewResponseController(w)
	total := 0
	fragments := newStreamGuard(guard)
	defer fragments.clear()
	write := func(frame []byte) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := controller.SetWriteDeadline(time.Now().Add(clientWriteTimeout)); err != nil {
			return err
		}
		w.Header().Set("Content-Type", "text/event-stream")
		started = true
		if _, err := w.Write(frame); err != nil {
			return err
		}
		return controller.Flush()
	}
	for {
		frame, err := readFrame(reader)
		if err != nil {
			return started, err
		}
		total += len(frame)
		if total > maxStream {
			return started, errInvalidDocument
		}
		event, data, err := frameData(frame)
		if err != nil {
			return started, err
		}
		if err := fragments.inspectMetadata(frame); err != nil {
			return started, err
		}
		if len(data) == 0 {
			if guard.contains(string(frame)) {
				return started, errSecret
			}
			if err := fragments.deliver(frame, false, write); err != nil {
				return started, err
			}
			continue
		}
		if bytes.Equal(bytes.TrimSpace(data), []byte("[DONE]")) {
			if operation != ChatCompletion || guard.contains(string(frame)) {
				return started, errInvalidDocument
			}
			err := fragments.deliver(frame, true, write)
			return started, err
		}
		object, err := document(data, secretGuard{})
		if err != nil {
			return started, err
		}
		var kind string
		if raw, ok := object["type"]; ok && json.Unmarshal(raw, &kind) != nil {
			return started, errInvalidDocument
		}
		if len(kind) > 128 || strings.IndexFunc(kind, func(r rune) bool {
			return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '_' && r != '.' && r != '-'
		}) >= 0 {
			return started, errInvalidDocument
		}
		if event != "" && kind != "" && event != kind {
			return started, errInvalidDocument
		}
		if kind == "error" || nonNull(object["error"]) {
			code, nativeCode := nativeErrorCode(object["error"], domain.Unavailable)
			raw := errorBodyWithNativeCode(operation.protocol(), code, correlation, nativeCode)
			if guard.contains(string(raw)) {
				return started, errSecret
			}
			prefix := "data: "
			if operation.protocol() == domain.AnthropicMessages {
				prefix = "event: error\ndata: "
			}
			safe := append([]byte(prefix), raw...)
			safe = append(safe, '\n', '\n')
			if err := fragments.deliver(safe, true, write); err != nil {
				return started, err
			}
			return started, &nativeFailure{code: code}
		}
		terminal := false
		var terminalFailure *nativeFailure
		var observedResponse map[string]json.RawMessage
		switch operation {
		case ResponseCreate:
			if raw, ok := object["response"]; ok {
				response, err := document(raw, secretGuard{})
				if err != nil {
					return started, err
				}
				if nonNull(response["error"]) {
					code, nativeCode := nativeErrorCode(response["error"], domain.Unavailable)
					terminalFailure = &nativeFailure{code: code}
					response["error"] = nativeErrorValue(operation.protocol(), code, correlation, nativeCode)
					object["response"], _ = json.Marshal(response)
					data, _ = json.Marshal(object)
					frame = append([]byte("event: "+kind+"\ndata: "), data...)
					frame = append(frame, '\n', '\n')
				}
				observedResponse = response
			}
			terminal = kind == "response.completed" || kind == "response.failed" || kind == "response.incomplete"
			if kind == "response.failed" && terminalFailure == nil {
				terminalFailure = &nativeFailure{code: domain.Unavailable}
			}
		case MessageCreate:
			terminal = kind == "message_stop"
		}
		if guard.contains(string(frame)) {
			return started, errSecret
		}
		if _, err := document(data, guard); err != nil {
			return started, err
		}
		if err := fragments.inspect(data); err != nil {
			return started, err
		}
		// Reference storage is a persistence boundary too. Do not hand it an
		// upstream response identity before the complete secret checks succeed.
		if observedResponse != nil {
			if err := observeReference(ctx, lease, operation, observedResponse); err != nil {
				return started, err
			}
		}
		if err := fragments.deliver(frame, terminal, write); err != nil {
			return started, err
		}
		if terminal {
			if terminalFailure != nil {
				return started, terminalFailure
			}
			return started, nil
		}
	}
}

func nonNull(raw json.RawMessage) bool {
	return len(raw) > 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

// Keep only closed machine-readable native error codes. Native compaction and
// rate-limit handling must still distinguish a context limit from unavailable
// transport, without forwarding provider diagnostic text, IDs or request data.
func nativeErrorCode(raw json.RawMessage, fallback domain.Code) (domain.Code, string) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return fallback, ""
	}
	for _, field := range []string{"code", "type"} {
		var value string
		if json.Unmarshal(fields[field], &value) != nil {
			continue
		}
		switch value {
		case "context_length_exceeded", "max_tokens", "invalid_request_error":
			return domain.InvalidArgument, value
		case "invalid_api_key", "authentication_error":
			return domain.Unauthenticated, value
		case "permission_denied", "permission_error":
			return domain.PermissionDenied, value
		case "rate_limit_exceeded", "rate_limit_error", "insufficient_quota":
			return domain.ResourceExhausted, value
		case "model_not_found", "not_found_error":
			return domain.Unsupported, value
		case "server_error", "api_error", "overloaded_error":
			return domain.Unavailable, value
		}
	}
	return fallback, ""
}
func nativeErrorValue(protocol domain.APIProtocol, code domain.Code, correlation, nativeCode string) json.RawMessage {
	var object map[string]json.RawMessage
	_ = json.Unmarshal(errorBodyWithNativeCode(protocol, code, correlation, nativeCode), &object)
	return object["error"]
}

func errorBodyWithNativeCode(protocol domain.APIProtocol, code domain.Code, correlation, nativeCode string) []byte {
	raw := errorBody(protocol, code, correlation)
	if nativeCode == "" {
		return raw
	}
	var object map[string]any
	_ = json.Unmarshal(raw, &object)
	fields := object["error"].(map[string]any)
	if protocol == domain.AnthropicMessages {
		if strings.HasSuffix(nativeCode, "_error") {
			fields["type"] = nativeCode
		}
	} else {
		fields["code"] = nativeCode
	}
	raw, _ = json.Marshal(object)
	return raw
}

type nativeFailure struct{ code domain.Code }

func (f *nativeFailure) Error() string { return "native API stream failure" }

func readProviderError(response *http.Response, fallback domain.Code) (domain.Code, string) {
	// Error parsing is optional, small and independently timed. Only closed
	// native machine codes survive; messages and all other fields are discarded.
	if response.ContentLength > 64<<10 {
		return fallback, ""
	}
	timer := time.AfterFunc(2*time.Second, func() { _ = response.Body.Close() })
	defer timer.Stop()
	raw, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	defer clear(raw)
	if err != nil || len(raw) > 64<<10 {
		return fallback, ""
	}
	object, err := document(raw, secretGuard{})
	if err != nil {
		return fallback, ""
	}
	return nativeErrorCode(object["error"], fallback)
}
