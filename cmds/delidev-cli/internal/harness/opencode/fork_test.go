// SPDX-License-Identifier: Apache-2.0
package opencode

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestForkFailedDurableClaimCannotSendOrReconcile(t *testing.T) {
	f := newHistoryFixture(t)
	s := f.api.session
	s.forkAttempt = &nativeForkAttempt{source: fixtureSessionID, attempted: map[SessionMutation]bool{}, claimed: map[SessionMutation]bool{}}
	s.claim = func(context.Context, SessionClaim) error { return sessionUncertain() }
	ref := CheckpointReference{SessionID: fixtureSessionID, InputID: fixtureMessageID, PartID: fixturePartID, InputRequestID: domain.NewID()}
	for _, kind := range []SessionMutation{ForkSessionMutation, MoveForkMutation, MarkForkMutation, DeleteForkSourceMutation} {
		if _, err := s.forkMutation(context.Background(), kind, domain.NewID(), ref, http.MethodPost, "/session/"+fixtureSessionID+"/fork", []byte("{}"), http.StatusOK); err == nil || s.forkAttempt.claimed[kind] {
			t.Fatal("failed journal acquired mutation authority")
		}
		if _, err := s.forkMutation(context.Background(), kind, domain.NewID(), ref, http.MethodPost, "/session/"+fixtureSessionID+"/fork", []byte("{}"), http.StatusOK); err == nil {
			t.Fatal("reopened uncertain original claim")
		}
	}
	if len(f.reads) != 0 {
		t.Fatal("failed durable claim sent native work")
	}
}

func TestForkHistoryPreservesEveryNonIdentityByte(t *testing.T) {
	f := newHistoryFixture(t)
	const childID = "ses_01960dcbe1fcABCDEFGHIJKLMN"
	source := []json.RawMessage{}
	child := []json.RawMessage{}
	mapping := map[string]string{}
	for index, id := range f.o.messageOrder {
		original := f.o.messages[id]
		var info map[string]any
		if json.Unmarshal(original.raw, &info) != nil {
			t.Fatal("invalid fixture message")
		}
		var parts []any = []any{}
		for _, id := range original.parts {
			var part map[string]any
			_ = json.Unmarshal(f.o.parts[id].raw, &part)
			parts = append(parts, part)
		}
		raw, _ := json.Marshal(map[string]any{"info": info, "parts": parts})
		source = append(source, raw)
		nextID := "msg_01960dcbe1fc1234567890ABCD"
		if index > 0 {
			nextID = "msg_01960dcbe1fd1234567890ABCD"
		}
		mapping[id] = nextID
		info["id"], info["sessionID"] = nextID, childID
		if parent, ok := info["parentID"].(string); ok {
			info["parentID"] = mapping[parent]
		}
		for i, part := range parts {
			p := part.(map[string]any)
			p["id"] = []string{"prt_01960dcbe1fc1234567890ABCD", "prt_01960dcbe1fd1234567890ABCD", "prt_01960dcbe1fe1234567890ABCD"}[index+i]
			p["messageID"], p["sessionID"] = nextID, childID
		}
		raw, _ = json.Marshal(map[string]any{"info": info, "parts": parts})
		child = append(child, raw)
	}
	if ids, _, err := compareForkHistory(source, child, fixtureSessionID, childID); err != nil || len(ids) != 2 {
		t.Fatal("complete native ID clone rejected", err)
	}
	for _, mutation := range []string{"model", "text", "usage", "parent", "path", "reused-part", "missing-message", "reordered"} {
		t.Run(mutation, func(t *testing.T) {
			changed := append([]json.RawMessage(nil), child...)
			var value map[string]any
			_ = json.Unmarshal(changed[1], &value)
			info := value["info"].(map[string]any)
			parts := value["parts"].([]any)
			switch mutation {
			case "model":
				info["modelID"] = "unobserved-model"
			case "text":
				parts[0].(map[string]any)["text"] = "foreign text"
			case "usage":
				info["cost"] = 99
			case "parent":
				info["parentID"] = fixtureMessageID
			case "path":
				info["path"] = map[string]any{"cwd": "/foreign", "root": "/foreign"}
			case "reused-part":
				parts[1].(map[string]any)["id"] = parts[0].(map[string]any)["id"]
			}
			changed[1], _ = json.Marshal(value)
			if mutation == "missing-message" {
				changed = changed[:1]
			}
			if mutation == "reordered" {
				changed[0], changed[1] = changed[1], changed[0]
			}
			if _, _, err := compareForkHistory(source, changed, fixtureSessionID, childID); err == nil {
				t.Fatal("incomplete or changed native history adopted")
			}
		})
	}
}
