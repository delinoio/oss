// SPDX-License-Identifier: Apache-2.0
package apiproxy

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestProxyResponsesStreamErrorEvents(t *testing.T) {
	for _, tc := range []struct {
		name, nativeCode, sequence string
		wantCode                   domain.Code
		wantNativeCode             string
		wantSequence               uint64
		chunked, dataOnly          bool
	}{
		{"context", "context_length_exceeded", "1", domain.InvalidArgument, "context_length_exceeded", 1, false, false},
		{"rate-limit-chunked", "rate_limit_exceeded", "2", domain.ResourceExhausted, "rate_limit_exceeded", 2, true, false},
		{"unknown-data-only", "private_unknown_code", "3", domain.Unavailable, "unavailable", 3, false, true},
		{"null-code", "", "4", domain.Unavailable, "unavailable", 4, false, false},
		{"largest-safe-sequence", "server_error", "9007199254740991", domain.Unavailable, "server_error", 9007199254740991, false, false},
		{"oversized-sequence", "server_error", "9007199254740992", domain.Unavailable, "server_error", 0, false, false},
		{"negative-sequence", "server_error", "-1", domain.Unavailable, "server_error", 0, false, false},
		{"fractional-sequence", "server_error", "1.5", domain.Unavailable, "server_error", 0, false, false},
		{"string-sequence", "server_error", `"1"`, domain.Unavailable, "server_error", 0, false, false},
		{"null-sequence", "server_error", "null", domain.Unavailable, "server_error", 0, false, false},
		{"missing-sequence", "server_error", "", domain.Unavailable, "server_error", 0, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fields := map[string]any{
				"type": "error", "code": tc.nativeCode,
				"message": "private provider detail " + fixtureKey + fixtureToken + base64.StdEncoding.EncodeToString([]byte(fixtureKey)),
				"param":   fixtureToken,
			}
			if tc.nativeCode == "private_unknown_code" {
				// A nested extension cannot override the Responses top-level code.
				fields["error"] = map[string]any{"code": "invalid_api_key", "message": fixtureKey}
			}
			if tc.nativeCode == "" {
				fields["code"] = nil
			}
			if tc.sequence != "" {
				fields["sequence_number"] = json.RawMessage(tc.sequence)
			}
			input, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			prefix, newline := "event: error\ndata: ", "\n"
			if tc.dataOnly {
				prefix = "data: "
			}
			if tc.chunked {
				newline = "\r\n"
				prefix = strings.ReplaceAll(prefix, "\n", newline)
			}
			frame := prefix + string(input) + newline + newline
			f := newProxyFixture(t, domain.OpenAIResponses, []Operation{ResponseCreate}, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				if tc.chunked {
					for _, b := range []byte(frame) {
						_, _ = w.Write([]byte{b})
						w.(http.Flusher).Flush()
					}
				} else {
					_, _ = fmt.Fprint(w, frame)
				}
				_, _ = fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"must_not_deliver\",\"error\":null}}\n\n")
			})
			var settlements atomic.Int32
			f.authority.publishDiagnostic = func(_ context.Context, diagnostic domain.RequestDiagnostic) error {
				if diagnostic.FinishedAt != nil {
					settlements.Add(1)
					if diagnostic.State != domain.RequestDiagnosticFailed || diagnostic.ErrorCode != tc.wantCode {
						t.Errorf("incorrect terminal diagnostic: state=%v code=%s", diagnostic.State, diagnostic.ErrorCode)
					}
				}
				return nil
			}
			response, raw, err := f.request(t, "/responses", `{"model":"fixed-model","stream":true}`, nil)
			if err != nil || response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream" {
				t.Fatalf("Responses stream error: %v %v", response, err)
			}
			event, data, err := frameData(raw)
			if err != nil || event != "error" {
				t.Fatalf("incorrect SSE error event: event=%q error=%v", event, err)
			}
			var output map[string]json.RawMessage
			if err := json.Unmarshal(data, &output); err != nil {
				t.Fatal(err)
			}
			var kind, code, message string
			var sequence uint64
			if json.Unmarshal(output["type"], &kind) != nil || kind != "error" ||
				json.Unmarshal(output["code"], &code) != nil || code != tc.wantNativeCode ||
				json.Unmarshal(output["message"], &message) != nil || message == "" ||
				json.Unmarshal(output["sequence_number"], &sequence) != nil || sequence != tc.wantSequence ||
				!bytes.Equal(output["param"], []byte("null")) || len(output) != 5 {
				t.Fatalf("incorrect Responses error shape: %s", data)
			}
			waitProxyLeaseRelease(t, f)
			assertNoProxySecrets(t, f, raw)
			if bytes.Contains(raw, []byte("private provider detail")) || strings.Contains(f.logs.String(), "private provider detail") {
				t.Fatal("native diagnostic message escaped")
			}
			if settlements.Load() != 1 || f.authority.keys.Load() != 1 || f.calls.Load() != 1 ||
				strings.Count(f.logs.String(), "api_proxy_request_finished") != 1 ||
				!strings.Contains(f.logs.String(), `"error_code":"`+string(tc.wantCode)+`"`) {
				t.Fatalf("incorrect failure settlement: settlements=%d keys=%d calls=%d logs=%s", settlements.Load(), f.authority.keys.Load(), f.calls.Load(), f.logs.String())
			}
		})
	}
}

func TestProxyResponsesStreamErrorCannotReflectAllowlistedCode(t *testing.T) {
	f := newProxyFixture(t, domain.OpenAIResponses, []Operation{ResponseCreate}, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: error\ndata: {\"type\":\"error\",\"code\":\"rate_limit_exceeded\",\"message\":\"private detail\",\"param\":null,\"sequence_number\":1}\n\n")
	})
	f.authority.key = "rate_limit_exceeded"
	response, raw, err := f.request(t, "/responses", `{"model":"fixed-model","stream":true}`, nil)
	if err != nil || response.StatusCode != http.StatusOK || bytes.Contains(raw, []byte(f.authority.key)) ||
		!bytes.Contains(raw, []byte(`"code":"resource_exhausted"`)) || !bytes.Contains(raw, []byte(`"type":"error"`)) {
		t.Fatalf("unsafe Responses machine code: %v %v %s", response, err, raw)
	}
	waitProxyLeaseRelease(t, f)
	if strings.Contains(f.logs.String(), f.authority.key) {
		t.Fatal("protected native code escaped into logs")
	}
}

func TestProxyResponsesStreamErrorCannotCompleteProtectedSequence(t *testing.T) {
	f := newProxyFixture(t, domain.OpenAIResponses, []Operation{ResponseCreate}, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: response.created\ndata: {\"type\":\"response.created\",\"sequence_number\":123,\"response\":{\"id\":\"resp_test\",\"error\":null}}\n\n")
		w.(http.Flusher).Flush()
		_, _ = fmt.Fprint(w, "event: error\ndata: {\"type\":\"error\",\"code\":\"server_error\",\"message\":\"private detail\",\"param\":null,\"sequence_number\":456789}\n\n")
	})
	f.authority.key = "123456789"
	response, raw, err := f.request(t, "/responses", `{"model":"fixed-model","stream":true}`, nil)
	if err != nil || response.StatusCode != http.StatusBadGateway || bytes.Contains(raw, []byte("123")) || bytes.Contains(raw, []byte("456789")) {
		t.Fatalf("fragmented protected sequence escaped: %v %v %s", response, err, raw)
	}
	waitProxyLeaseRelease(t, f)
	if strings.Contains(f.logs.String(), f.authority.key) {
		t.Fatal("protected sequence escaped into logs")
	}
}

func TestProxyResponsesDataOnlyErrorCannotFlushProtectedMetadataPrefix(t *testing.T) {
	f := newProxyFixture(t, domain.OpenAIResponses, []Operation{ResponseCreate}, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: prefix\n\n")
		w.(http.Flusher).Flush()
		_, _ = fmt.Fprint(w, "data: {\"type\":\"error\",\"code\":\"server_error\",\"message\":\"private detail\"}\n\n")
	})
	f.authority.key = "prefixerror"
	response, raw, err := f.request(t, "/responses", `{"model":"fixed-model","stream":true}`, nil)
	if err != nil || response.StatusCode != http.StatusBadGateway || bytes.Contains(raw, []byte("prefix")) || bytes.Contains(raw, []byte("event: error")) {
		t.Fatalf("protected metadata prefix escaped or was delivered: %v %v %s", response, err, raw)
	}
	waitProxyLeaseRelease(t, f)
	if strings.Contains(f.logs.String(), f.authority.key) {
		t.Fatal("protected metadata prefix escaped into logs")
	}
}

func TestProxyChatStreamErrorRetainsNestedEnvelope(t *testing.T) {
	f := newProxyFixture(t, domain.OpenAIChat, []Operation{ChatCompletion}, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: {\"error\":{\"code\":\"context_length_exceeded\",\"message\":%q}}\n\n", fixtureKey)
	})
	response, raw, err := f.request(t, "/chat/completions", `{"model":"fixed-model","stream":true}`, nil)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("Chat stream error: %v %v", response, err)
	}
	event, data, err := frameData(raw)
	var output map[string]json.RawMessage
	if err != nil || event != "" || json.Unmarshal(data, &output) != nil || !nonNull(output["error"]) || len(output) != 1 ||
		!bytes.Contains(data, []byte(`"code":"context_length_exceeded"`)) {
		t.Fatalf("Chat error envelope changed: %s", raw)
	}
	assertNoProxySecrets(t, f, raw)
	waitProxyLeaseRelease(t, f)
}

func waitProxyLeaseRelease(t *testing.T, f *proxyFixture) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for f.authority.releases.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("proxy lease was not released")
		}
		time.Sleep(time.Millisecond)
	}
	if f.authority.releases.Load() != 1 {
		t.Fatalf("proxy lease released %d times", f.authority.releases.Load())
	}
}
