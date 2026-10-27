// SPDX-License-Identifier: Apache-2.0
package cursor

import (
	"bytes"
	"context"
	"encoding/hex"
	"testing"
	"time"
)

const nativeHistory = `{"messages":[{"role":"user","content":"check weather"},{"role":"assistant","content":"checking","reasoning_content":"need observations","tool_calls":[{"id":"call-1","type":"function","function":{"name":"weather","arguments":"{\"city\":\"Seoul\"}"}}]},{"role":"tool","tool_call_id":"call-1","content":"sunny"}],"tools":[{"type":"function","function":{"name":"weather","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}}]}`

func TestNativeComposerReplay(t *testing.T) {
	for _, model := range []string{"composer-1", "composer-2.5", "composer-2.5-fast", "auto-cost", "gpt-5.4"} {
		t.Run(model, func(t *testing.T) {
			in := input(nativeHistory)
			in.Target.Model = model
			request, s, err := Prepare(context.Background(), in, nil)
			if err != nil {
				t.Fatal(err)
			}
			frame, _ := ReadFrame(bytes.NewReader(request.Body))
			outer, _ := parse(frame.Payload)
			run, _ := outer.sub(1)
			action, _ := run.sub(2)
			resume := !externalToolContinuation(model)
			if (action.get(2).number != 0) != resume {
				t.Fatalf("wrong continuation action %+v", action)
			}
			state, _ := run.sub(1)
			if !nativeModel(model) {
				if state.get(8).number != 0 {
					t.Fatal("external worker received Composer state")
				}
				return
			}
			fetch := func(id []byte) message {
				t.Helper()
				data, ok := s.blobs[hex.EncodeToString(id)]
				if !ok {
					t.Fatalf("unhydrated blob %x", id)
				}
				m, err := parse(data)
				if err != nil {
					t.Fatal(err)
				}
				return m
			}
			turn := fetch(state.get(8).data)
			agent, _ := turn.sub(1)
			user := fetch(agent.get(1).data)
			if user.str(1) != "check weather" || user.get(3).number == 0 || user.get(4).value != 1 {
				t.Fatalf("wrong historical user %+v", user)
			}
			var steps []message
			for _, f := range agent {
				if f.number == 2 {
					steps = append(steps, fetch(f.data))
				}
			}
			if len(steps) != 3 {
				t.Fatalf("step count %d", len(steps))
			}
			thinking, _ := steps[0].sub(3)
			assistant, _ := steps[1].sub(1)
			if thinking.str(1) != "need observations" || assistant.str(1) != "checking" {
				t.Fatal("typed text replay lost")
			}
			call, _ := steps[2].sub(2)
			mcp, _ := call.sub(15)
			args, _ := mcp.sub(1)
			if args.str(1) != "ocx_client_weather" || args.str(3) != "call-1" || args.str(4) != toolProvider {
				t.Fatalf("wrong call identity %+v", args)
			}
			arg, _ := args.sub(2)
			value, err := decodeValue(arg.get(2).data, 0)
			if err != nil || arg.str(1) != "city" || value != "Seoul" {
				t.Fatalf("argument %v %v", value, err)
			}
			result, _ := mcp.sub(2)
			success, _ := result.sub(1)
			item, _ := success.sub(1)
			text, _ := item.sub(1)
			if text.str(1) != "sunny" || success.get(2).value != 0 {
				t.Fatal("tool observation lost")
			}
		})
	}
}
func TestNativeMissingResultAndActiveTurn(t *testing.T) {
	s := newSession("composer-1", nil)
	history := []historyMessage{{Role: "user", Text: "old"}, {Role: "assistant", Calls: []historyCall{{ID: "missing", Name: "weather", Arguments: "{}"}}}, {Role: "user", Text: "new"}}
	wire, err := s.nativeReplay(history, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	state, _ := parse(wire)
	turn, _ := parse(s.blobs[hex.EncodeToString(state.get(8).data)])
	agent, _ := turn.sub(1)
	step, _ := parse(s.blobs[hex.EncodeToString(agent.get(2).data)])
	call, _ := step.sub(2)
	mcp, _ := call.sub(15)
	result, _ := mcp.sub(2)
	success, _ := result.sub(1)
	item, _ := success.sub(1)
	text, _ := item.sub(1)
	if success.get(2).value != 1 || text.str(1) != "[missing tool_result for this tool_use in history]" {
		t.Fatal("missing result became success")
	}
	for _, blob := range s.blobs {
		if bytes.Contains(blob, []byte("new")) {
			t.Fatal("active message replayed twice")
		}
	}
}

func TestNativeReplayContinuationWithoutInventedCheckpoint(t *testing.T) {
	in := input(nativeHistory)
	in.Target.Model = "composer-1"
	_, session, err := Prepare(context.Background(), in, nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := session.Snapshot(time.Now())
	if err != nil || !snapshot.ReplayOnly || len(snapshot.Checkpoint) != 0 {
		t.Fatalf("replay state %#v %v", snapshot, err)
	}
	request, _, err := Prepare(context.Background(), in, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	frame, _ := ReadFrame(bytes.NewReader(request.Body))
	outer, _ := parse(frame.Payload)
	run, _ := outer.sub(1)
	state, _ := run.sub(1)
	if state.get(8).number == 0 {
		t.Fatal("restored native state lost typed turns")
	}
	bad := *snapshot
	bad.Owner.Model = "gpt-5.4"
	in.Target.Model = "gpt-5.4"
	_, _, err = Prepare(context.Background(), in, &bad)
	requireCode(t, err, "continuation_unavailable")
}
