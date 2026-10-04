// SPDX-License-Identifier: Apache-2.0
package opencode

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"io"
	"net/http"
	"strings"
	"testing"
)

type childHistoryTransport func(*http.Request) (*http.Response, error)

func (f childHistoryTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestForegroundChildUsesPinnedChronologicalPage(t *testing.T) {
	for _, variant := range []string{"pinned-chronological", "contradictory-newest-first", "completed-question"} {
		t.Run(variant, func(t *testing.T) {
			reversed := variant == "contradictory-newest-first"
			f := newHistoryFixture(t)
			s := f.api.session
			childID := "ses_01960dcbe1fcABCDEFGHIJKLMN"
			userID := "msg_01960dcbe1fcABCDEFGHIJKLMN"
			receipt := InputReceipt{RequestID: domain.NewID(), SessionID: childID, MessageID: userID, PartID: "prt_01960dcbe1fcABCDEFGHIJKLMN"}
			user := fixtureInput(receipt, fixtureSettings(), "original child input")
			assistant := fixtureAssistant()
			assistant["sessionID"], assistant["parentID"] = childID, userID
			assistant["finish"] = "stop"
			assistant["time"].(map[string]any)["completed"] = 1250
			answer := map[string]any{"id": "prt_01960dcbe1fdABCDEFGHIJKLMN", "sessionID": childID, "messageID": assistant["id"], "type": "text", "text": "original child answer", "time": map[string]any{"start": 1235, "end": 1250}}
			parts := []any{answer}
			if variant == "completed-question" {
				question := fixtureCompletedTool()
				question["id"], question["sessionID"], question["messageID"], question["tool"] = "prt_01960dcbe1feABCDEFGHIJKLMN", childID, assistant["id"], string(domain.OpenCodeQuestionTool)
				parts = append(parts, question)
			}
			rows := []any{user, map[string]any{"info": assistant, "parts": parts}}
			if reversed {
				rows[0], rows[1] = rows[1], rows[0]
			}
			s.client = &http.Client{Transport: childHistoryTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodGet {
					t.Fatal("child history gained mutation authority")
				}
				var value any
				switch r.URL.RequestURI() {
				case "/session/" + childID:
					session := fixtureSession(s.cwd, domain.NewID(), fixtureSettings())
					session["id"], session["parentID"] = childID, fixtureSessionID
					session["time"] = map[string]any{"created": 1235, "updated": 1250}
					value = session
				case "/session/" + childID + "/message?limit=1000":
					value = rows
				case "/session/status":
					value = map[string]any{}
				default:
					t.Fatal("child history escaped closed GET routes", r.URL.RequestURI())
				}
				raw, _ := json.Marshal(value)
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
			})}
			child := &foregroundChild{published: true, task: domain.OpenCodeTaskInput{Prompt: "original child input", AgentType: "build"}, value: domain.SubagentObservation{ID: domain.NewID(), NativeID: childID, ParentID: fixtureSessionID, Status: domain.SubagentPending}}
			values, err := s.observeForegroundChild(context.Background(), child, "evt_01960dcbe1ffABCDEFGHIJKLMN", domain.OpenCodeChildHistorySource)
			if reversed || variant == "completed-question" {
				if err == nil {
					t.Fatal("contradictory order adopted")
				}
				return
			}
			if err != nil || len(values) != 1 || values[0].Status != domain.SubagentCompleted || values[0].Output == nil || values[0].Output.Text != "original child answer" || values[0].Usage == nil || values[0].ObservedModel == nil {
				t.Fatal("pinned child page rejected", values, err)
			}
		})
	}
}

func TestForegroundChildFinalInventoryCannotBorrowFailedSettlement(t *testing.T) {
	for _, prior := range []domain.SubagentStatus{domain.SubagentFailed, domain.SubagentInterrupted, domain.SubagentCompleted} {
		for _, empty := range []bool{false, true} {
			t.Run(string(prior)+"/"+map[bool]string{false: "user-only", true: "empty"}[empty], func(t *testing.T) {
				f := newHistoryFixture(t)
				s := f.api.session
				childID := "ses_01960dcbe1fcABCDEFGHIJKLMN"
				receipt := InputReceipt{RequestID: domain.NewID(), SessionID: childID, MessageID: "msg_01960dcbe1fcABCDEFGHIJKLMN", PartID: "prt_01960dcbe1fcABCDEFGHIJKLMN"}
				rows := []any{fixtureInput(receipt, fixtureSettings(), "original child input")}
				if empty {
					rows = []any{}
				}
				session := fixtureSession(s.cwd, domain.NewID(), fixtureSettings())
				session["id"], session["parentID"] = childID, fixtureSessionID
				session["time"] = map[string]any{"created": 1235, "updated": 1250}
				s.client = &http.Client{Transport: childHistoryTransport(func(r *http.Request) (*http.Response, error) {
					if r.Method != http.MethodGet {
						t.Fatal("inventory gained mutation authority")
					}
					var value any
					switch r.URL.RequestURI() {
					case "/session/" + fixtureSessionID + "/children":
						value = []any{session}
					case "/session/" + childID:
						value = session
					case "/session/" + childID + "/message?limit=1000":
						value = rows
					case "/session/status":
						value = map[string]any{}
					default:
						t.Fatal("inventory escaped owned GET routes")
					}
					raw, _ := json.Marshal(value)
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
				})}
				child := &foregroundChild{published: true, task: domain.OpenCodeTaskInput{Prompt: "original child input", AgentType: "build"}, value: domain.SubagentObservation{ID: domain.NewID(), NativeID: childID, ParentID: fixtureSessionID, Status: prior}}
				s.children = map[string]*foregroundChild{childID: child}
				if err := s.readForegroundChildInventory(context.Background(), false); err == nil || s.childInventoryVerified || len(s.childInventoryFacts) != 0 {
					t.Fatal("missing fresh settlement granted root completion")
				}
				if !empty {
					if child.value.Status.Terminal() {
						t.Fatal("earlier terminal state survived current user-only page")
					}
					// Original Stop may retain this unfinished history; only the separate
					// joined scope-cleanup proof can subsequently project interruption.
					if err := s.readForegroundChildInventory(context.Background(), true); err != nil || !s.childInventoryVerified || child.value.Status.Terminal() {
						t.Fatal("unfinished Stop history gained native settlement", err)
					}
				}
			})
		}
	}
}
