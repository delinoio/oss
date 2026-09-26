package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type replyFixture struct {
	f                 *observerFixture
	api               *sessionAPI
	mu                sync.Mutex
	claims            []SessionClaim
	posts             int
	lost              bool
	claimError        bool
	beforeClaimReturn func()
	onReply           func()
	id                string
	response          InteractionResponse
}

func newReplyFixture(t *testing.T, kind InteractionKind) *replyFixture {
	t.Helper()
	f := newObserverFixture(t)
	f.start()
	f.part(f.assistantPart(StepStartPartKind, 90, nil))
	name, event := "read", PermissionAskedEvent
	proposal := fixturePermission()
	decision := PermissionOnce
	response := InteractionResponse{Decision: &decision}
	if kind == QuestionInteraction {
		name, event, proposal, response = "question", QuestionAskedEvent, fixtureQuestion(), InteractionResponse{Answers: [][]string{{"private-sentinel"}}}
	}
	part := f.assistantPart(ToolPartKind, 100, map[string]any{"callID": "call_private", "tool": name, "state": map[string]any{"status": ToolPending, "input": map[string]any{}, "raw": ""}})
	f.part(part)
	proposal["tool"].(map[string]any)["messageID"] = f.a["id"]
	observation := f.observe(event, proposal)
	r := &replyFixture{f: f, id: observation.Interaction.ID, response: response}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		username, password, valid := request.BasicAuth()
		if !valid || username != "delidev" || password != "private-credential" || request.URL.RawQuery != "" || request.Header.Get("x-opencode-directory") != f.o.cwd {
			t.Error("reply escaped original HTTP authority")
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodGet && request.URL.Path == "/session/"+fixtureSessionID {
			_ = json.NewEncoder(w).Encode(fixtureSession(f.o.cwd, f.o.creation.request, f.o.creation.settings))
			return
		}
		body, _ := io.ReadAll(request.Body)
		r.mu.Lock()
		r.posts++
		claims := append([]SessionClaim(nil), r.claims...)
		r.mu.Unlock()
		if request.Method != http.MethodPost || request.URL.Path != "/"+string(kind)+"/"+r.id+"/reply" || len(claims) != 1 || claims[0].BodyDigest != mutationDigest(body) || claims[0].InteractionID != r.id || claims[0].ArrivalID == "" || claims[0].InputRequestID != f.o.input.receipt.RequestID || claims[0].MessageID != f.a["id"] || claims[0].CallID != "call_private" || claims[0].PartID != part["id"] {
			t.Error("native reply preceded or changed its original durable claim")
			w.WriteHeader(400)
			return
		}
		if r.onReply != nil {
			r.onReply()
		}
		if r.lost {
			connection, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = connection.Close()
			}
			return
		}
		_, _ = io.WriteString(w, "true")
	}))
	t.Cleanup(server.Close)
	client, transport := probeHTTPClient(strings.TrimPrefix(server.URL, "http://"))
	t.Cleanup(transport.CloseIdleConnections)
	streamContext, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	r.api = &sessionAPI{client: client, origin: server.URL, password: "private-credential", cwd: f.o.cwd, creation: &f.o.creation, input: &f.o.input, observer: f.o, events: &eventStream{ctx: streamContext}, gate: make(chan struct{}, 1), alive: func() error { return nil }}
	r.api.claim = func(_ context.Context, claim SessionClaim) error {
		r.mu.Lock()
		r.claims = append(r.claims, claim)
		r.mu.Unlock()
		if r.beforeClaimReturn != nil {
			r.beforeClaimReturn()
		}
		if r.claimError {
			return errors.New("private claim diagnostic")
		}
		return nil
	}
	return r
}

func (r *replyFixture) closure() NativeEvent {
	kind := PermissionRepliedEvent
	fields := map[string]any{"sessionID": fixtureSessionID, "requestID": r.id, "reply": PermissionOnce}
	if r.response.Decision == nil {
		kind = QuestionRepliedEvent
		delete(fields, "reply")
		fields["answers"] = r.response.Answers
	}
	raw, _ := json.Marshal(fields)
	return NativeEvent{ID: "evt_01960dcbe1feABCDEFGHIJKLMN", Kind: kind, Properties: raw}
}

func TestInteractionReplyKeepsClaimDeliveryAndNativeAcceptanceSeparate(t *testing.T) {
	for _, kind := range []InteractionKind{PermissionInteraction, QuestionInteraction} {
		for _, lost := range []bool{false, true} {
			t.Run(string(kind)+"/lost="+fmtBool(lost), func(t *testing.T) {
				r := newReplyFixture(t, kind)
				r.lost = lost
				if lost {
					r.onReply = func() {
						if _, err := r.f.o.observe(context.Background(), r.closure()); err != nil {
							t.Error(err)
						}
					}
				}
				request := domain.NewID()
				receipt, err := r.api.replyInteraction(context.Background(), r.f.o, request, r.id, r.response)
				if (err != nil) != lost || receipt.HTTPAccepted == lost || receipt.NativeAccepted != lost || receipt.RequestID != request {
					t.Fatalf("independent delivery/acceptance: %+v %v", receipt, err)
				}
				if !lost {
					if _, err := r.f.o.observe(context.Background(), r.closure()); err != nil {
						t.Fatal(err)
					}
				}
				receipt, _ = r.f.o.interactionReceipt(r.id)
				if !receipt.NativeAccepted || r.f.o.snapshot().TerminalObserved {
					t.Fatal("reply acceptance fabricated root completion")
				}
				for _, next := range []domain.ID{request, domain.NewID()} {
					if _, err := r.api.replyInteraction(context.Background(), r.f.o, next, r.id, r.response); err == nil {
						t.Fatal("closed reply scope allowed resend")
					}
				}
				r.mu.Lock()
				posts, claims := r.posts, len(r.claims)
				r.mu.Unlock()
				if posts != 1 || claims != 1 {
					t.Fatal("uncertain reply automatically retried")
				}
				if r.api.replyAttempt != nil {
					t.Fatal("finished HTTP reply retained route authority")
				}
			})
		}
	}
}

func fmtBool(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func TestInteractionReplyClaimLossAndCancellationConsumeScope(t *testing.T) {
	for _, mode := range []string{"claim-error", "canceled", "observer-loss"} {
		t.Run(mode, func(t *testing.T) {
			r := newReplyFixture(t, PermissionInteraction)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "claim-error":
				r.claimError = true
			case "canceled":
				r.beforeClaimReturn = cancel
			case "observer-loss":
				r.beforeClaimReturn = func() { _ = r.f.o.interruption(context.Background()) }
			}
			receipt, err := r.api.replyInteraction(ctx, r.f.o, domain.NewID(), r.id, r.response)
			if err == nil || receipt.RequestID == "" || receipt.HTTPAccepted || receipt.NativeAccepted {
				t.Fatal("uncertain durable claim became a successful reply")
			}
			if _, err := r.api.replyInteraction(context.Background(), r.f.o, domain.NewID(), r.id, r.response); err == nil {
				t.Fatal("failed claim regained response authority")
			}
			r.mu.Lock()
			defer r.mu.Unlock()
			if r.posts != 0 || len(r.claims) != 1 {
				t.Fatal("failed claim sent or reclaimed native reply")
			}
		})
	}
}

func TestInteractionReplyRejectsUnclaimedAlteredAndDuplicateNativeReplies(t *testing.T) {
	for _, mode := range []string{"unclaimed", "altered", "duplicate", "foreign-request"} {
		t.Run(mode, func(t *testing.T) {
			r := newReplyFixture(t, QuestionInteraction)
			if mode != "unclaimed" {
				if _, err := r.api.replyInteraction(context.Background(), r.f.o, domain.NewID(), r.id, r.response); err != nil {
					t.Fatal(err)
				}
			}
			event := r.closure()
			if mode == "altered" || mode == "foreign-request" {
				fields, _ := object(event.Properties)
				if mode == "altered" {
					fields["answers"] = json.RawMessage(`[["foreign"]]`)
				} else {
					fields["requestID"] = json.RawMessage(`"que_01960dcbe1ffABCDEFGHIJKLMN"`)
				}
				event.Properties, _ = json.Marshal(fields)
			}
			if mode == "duplicate" {
				if _, err := r.f.o.observe(context.Background(), event); err != nil {
					t.Fatal(err)
				}
				event.ID = "evt_01960dcbe1ffABCDEFGHIJKLMN"
			}
			if _, err := r.f.o.observe(context.Background(), event); err == nil || !r.f.o.snapshot().NeedsRecovery {
				t.Fatal("unclaimed or changed reply erased recovery")
			}
			if mode == "duplicate" {
				receipt, _ := r.f.o.interactionReceipt(r.id)
				if !receipt.NativeAccepted {
					t.Fatal("later conflict erased original acceptance")
				}
			}
		})
	}
}

func TestQuestionAnswerValidationDoesNotRewriteNativeOptions(t *testing.T) {
	no, yes := false, true
	q := NativeQuestion{Custom: &no, Options: []QuestionOption{{Label: "First"}, {Label: "Second"}}}
	for _, answers := range [][][]string{nil, {nil}, {{"Foreign"}}, {{"First", "Second"}}, {{"First", "First"}}, {{"First"}, {"Second"}}} {
		if validQuestionAnswers([]NativeQuestion{q}, answers) {
			t.Fatal("invalid answers passed original question contract")
		}
	}
	if !validQuestionAnswers([]NativeQuestion{q}, [][]string{{}}) || !validQuestionAnswers([]NativeQuestion{q}, [][]string{{"First"}}) {
		t.Fatal("explicit unanswered/selected answer was rewritten")
	}
	q.Multiple = &yes
	if !validQuestionAnswers([]NativeQuestion{q}, [][]string{{"First", "Second"}}) {
		t.Fatal("native multiple selection was lost")
	}
	q.Custom = nil
	if !validQuestionAnswers([]NativeQuestion{q}, [][]string{{"Custom"}}) {
		t.Fatal("native omitted custom default was forced false")
	}
	q.Options = append(q.Options, QuestionOption{Label: "First"})
	if validQuestionAnswers([]NativeQuestion{q}, [][]string{{"First"}}) {
		t.Fatal("ambiguous option was silently selected")
	}
}

func TestInteractionReplyInvalidSelectionDoesNotConsumeOrSend(t *testing.T) {
	for _, decision := range []PermissionDecision{PermissionAlways, PermissionReject, "unknown"} {
		r := newReplyFixture(t, PermissionInteraction)
		if _, err := r.api.replyInteraction(context.Background(), r.f.o, domain.NewID(), r.id, InteractionResponse{Decision: &decision}); err == nil {
			t.Fatal("unimplemented decision gained single-use authority")
		}
		r.mu.Lock()
		claims, posts := len(r.claims), r.posts
		r.mu.Unlock()
		if claims != 0 || posts != 0 {
			t.Fatal("invalid selection consumed a mutation or sent a reply")
		}
		if _, err := r.api.replyInteraction(context.Background(), r.f.o, domain.NewID(), r.id, r.response); err != nil {
			t.Fatal(err)
		}
	}
	r := newReplyFixture(t, QuestionInteraction)
	foreign := newObserverFixture(t)
	if _, err := r.api.replyInteraction(context.Background(), foreign.o, domain.NewID(), r.id, r.response); err == nil {
		t.Fatal("foreign observer supplied native reply authority")
	}
	for _, request := range []domain.ID{r.f.o.creation.request, r.f.o.input.receipt.RequestID} {
		if _, err := r.api.replyInteraction(context.Background(), r.f.o, request, r.id, r.response); err == nil {
			t.Fatal("reply reused original creation/input request identity")
		}
	}
	if _, _, err := r.api.request(context.Background(), http.MethodPost, "/question/"+r.id+"/reply", []byte(`{"answers":[["private-sentinel"]]}`), http.StatusOK); err == nil {
		t.Fatal("unclaimed private HTTP route gained reply authority")
	}
}

func TestInteractionProposalOwnershipAndUnansweredToolClosure(t *testing.T) {
	for _, mode := range []string{"duplicate", "foreign-call", "missing-owner", "early-tool-result"} {
		t.Run(mode, func(t *testing.T) {
			r := newReplyFixture(t, PermissionInteraction)
			proposal := fixturePermission()
			proposal["tool"].(map[string]any)["messageID"] = r.f.a["id"]
			switch mode {
			case "foreign-call":
				proposal["id"] = "per_01960dcbe1ffABCDEFGHIJKLMN"
				proposal["tool"].(map[string]any)["callID"] = "foreign"
			case "missing-owner":
				proposal["id"] = "per_01960dcbe1ffABCDEFGHIJKLMN"
				delete(proposal, "tool")
			case "early-tool-result":
				part := r.f.assistantPart(ToolPartKind, 100, map[string]any{"callID": "call_private", "tool": "read", "state": map[string]any{"status": ToolError, "input": map[string]any{}, "error": "private", "time": map[string]any{"start": 1236, "end": 1240}}})
				r.f.reject(MessagePartUpdatedEvent, map[string]any{"sessionID": fixtureSessionID, "part": part, "time": 1240})
				return
			}
			r.f.reject(PermissionAskedEvent, proposal)
		})
	}
}
