// SPDX-License-Identifier: Apache-2.0
package apiproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func foregroundFrame(t *testing.T, name, arguments string, final bool) ([]byte, map[string]json.RawMessage) {
	t.Helper()
	function := map[string]any{}
	if name != "" {
		function["name"] = name
	}
	if arguments != "" {
		function["arguments"] = arguments
	}
	choice := map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "type": "function", "function": function}}}, "finish_reason": nil}
	if final {
		choice["delta"] = map[string]any{}
		choice["finish_reason"] = "tool_calls"
	}
	raw, _ := json.Marshal(map[string]any{"choices": []any{choice}})
	object, err := document(raw, secretGuard{})
	if err != nil {
		t.Fatal(err)
	}
	return append(append([]byte("data: "), raw...), '\n', '\n'), object
}

func TestOpenCodeTaskAndSecretGuardsRetainIndependentFrameOwnership(t *testing.T) {
	var stream bytes.Buffer
	for _, input := range []struct {
		name, arguments string
		final           bool
	}{
		{"task", `{"description":"Original task",`, false},
		{"", `"prompt":"Original prompt","subagent_type":"general"}`, false},
		{"", "", true},
	} {
		_, object := foregroundFrame(t, input.name, input.arguments, input.final)
		// This legitimate model suffix remains a possible execution-secret
		// prefix until DONE, forcing independent delayed frame ownership.
		object["model"] = json.RawMessage(`"fixture-model"`)
		raw, _ := json.Marshal(object)
		stream.WriteString("data: ")
		stream.Write(raw)
		stream.WriteString("\n\n")
	}
	stream.WriteString("data: [DONE]\n\n")
	writer := &delayedCancellationWriter{ResponseRecorder: httptest.NewRecorder()}
	lease := &Lease{Scope: Scope{Harness: domain.OpenCode}}
	started, err := relayStream(context.Background(), writer, bytes.NewReader(stream.Bytes()), ChatCompletion, lease, newSecretGuard([]byte("ddv_exec_fixture_private_secret")), "original", &diagnosticObservations{value: &domain.RequestDiagnostic{}})
	if err != nil || !started || !bytes.Equal(writer.Body.Bytes(), stream.Bytes()) {
		t.Fatal("one guard erased or reordered another guard's retained native frames", err)
	}
}

func TestOpenCodeTaskArgumentsAreCheckedBeforeExecutableFrames(t *testing.T) {
	for _, extra := range []string{"", `,"task_id":"original-child"`, `,"background":true`, `,"unknown":true`, `,"prompt":"duplicate"`} {
		t.Run(extra, func(t *testing.T) {
			g := &foregroundToolGuard{}
			defer g.clear()
			var written bytes.Buffer
			write := func(frame []byte) error { _, err := written.Write(frame); return err }
			a, ao := foregroundFrame(t, "ta", `{"description":"Original task",`, false)
			b, bo := foregroundFrame(t, "sk", `"prompt":"Original prompt","subagent_type":"general"`+extra+`}`, false)
			if g.deliver(a, ao, write) != nil || g.deliver(b, bo, write) != nil || written.Len() != 0 || g.settled() {
				t.Fatal("executable task arguments escaped before validation")
			}
			last, object := foregroundFrame(t, "", "", true)
			err := g.deliver(last, object, write)
			if extra != "" {
				if err == nil || written.Len() != 0 {
					t.Fatal("unsupported native task became executable")
				}
			} else if err != nil || g.settled() || written.Len() != 0 || g.finish(write) != nil || !g.settled() || !bytes.Equal(written.Bytes(), bytes.Join([][]byte{a, b, last}, nil)) {
				t.Fatal("original foreground task frames changed or remained withheld", err)
			}
		})
	}
}

func TestOpenCodeTaskGuardRetainsOrderingAndRefusesIncompleteOrRepeatedCalls(t *testing.T) {
	g := &foregroundToolGuard{}
	defer g.clear()
	var written bytes.Buffer
	write := func(frame []byte) error { _, err := written.Write(frame); return err }
	frame, object := foregroundFrame(t, "task", `{"description":"Original task",`, false)
	if g.deliver(frame, object, write) != nil || g.deliver([]byte(": keep-alive\n\n"), nil, write) != nil || written.Len() != 0 || g.settled() {
		t.Fatal("unfinished task or heartbeat overtook retained frames")
	}
	last, object := foregroundFrame(t, "", "", true)
	if g.deliver(last, object, write) == nil || written.Len() != 0 {
		t.Fatal("truncated arguments acquired a native call")
	}
	for _, raw := range []string{
		`{"choices":[{"message":{"tool_calls":[{"function":{"name":"task","arguments":"{\"description\":\"Original\",\"prompt\":\"Original\",\"subagent_type\":\"general\",\"task_id\":\"old\"}"}}]}}]}`,
		`{"choices":[{"message":{"tool_calls":[{"function":{"name":"task","arguments":"{}"}}]}}]}`,
	} {
		object, err := document([]byte(raw), secretGuard{})
		if err != nil || validateForegroundToolResponse(object) == nil {
			t.Fatal("nonstream task response acquired reuse or missing-input authority")
		}
	}
}

func TestOpenCodeTaskFramesRemainPrivateUntilValidatedDone(t *testing.T) {
	for _, ending := range []string{"done", "truncated", "malformed", "repeated-call"} {
		t.Run(ending, func(t *testing.T) {
			call, _ := foregroundFrame(t, "task", `{"description":"Original","prompt":"Original","subagent_type":"general"}`, false)
			final, _ := foregroundFrame(t, "", "", true)
			stream := append(append([]byte{}, call...), final...)
			switch ending {
			case "done":
				stream = append(stream, []byte("data: [DONE]\n\n")...)
			case "malformed":
				stream = append(stream, []byte("data: {broken}\n\n")...)
			case "repeated-call":
				stream = append(stream, call...)
				stream = append(stream, []byte("data: [DONE]\n\n")...)
			}
			writer := &delayedCancellationWriter{ResponseRecorder: httptest.NewRecorder()}
			started, err := relayStream(context.Background(), writer, bytes.NewReader(stream), ChatCompletion, &Lease{Scope: Scope{Harness: domain.OpenCode}}, secretGuard{}, "original", &diagnosticObservations{value: &domain.RequestDiagnostic{}})
			if ending == "done" {
				if err != nil || !started || !bytes.Equal(writer.Body.Bytes(), stream) {
					t.Fatal("validated task frames changed order", err)
				}
			} else if err == nil || started || writer.Body.Len() != 0 {
				t.Fatal("unfinished or invalid stream exposed executable task", ending, err)
			}
		})
	}
}
