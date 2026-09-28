package claude

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
)

func TestManualNativeQuestionRetainedHistory(t *testing.T) {
	for _, permission := range []NativePermission{DefaultPermission, PlanPermission} {
		t.Run(string(permission), func(t *testing.T) { nativeQuestionRetainedHistory(t, permission) })
	}
}

func nativeQuestionRetainedHistory(t *testing.T, permission NativePermission) {
	binary := os.Getenv("DELIDEV_NATIVE_CLAUDE_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit native binary and private scripted provider required")
	}
	cfg, logs := apiFixtureConfig(t, "native-question-history")
	cfg.Process.Executable, cfg.Model, cfg.Permission = binary, "fixture-model", permission
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	var calls atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/provider/messages" || r.Header.Get("X-Api-Key") != nativeAPIUpstreamKey || r.Header.Get("Authorization") != "" {
			t.Error("native question fixture authority changed")
			w.WriteHeader(400)
			return
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, maxStreamFrame+1))
		if err != nil || len(raw) > maxStreamFrame {
			t.Error("unbounded native question request")
			w.WriteHeader(400)
			return
		}
		n := calls.Add(1)
		if n > 6 {
			t.Error("unexpected repeated inference")
			w.WriteHeader(400)
			return
		}
		results := nativeFixtureToolResults(t, raw)
		for previous := int64(1); previous <= n/2; previous++ {
			result, exists := results[fmt.Sprintf("toolu_question_%d", previous)]
			if !exists || result.Error || !bytes.Contains(result.Content, []byte(retainedQuestion)) || !bytes.Contains(result.Content, []byte(retainedAnswer)) {
				t.Error("replacement lost an original question answer")
				w.WriteHeader(400)
				return
			}
		}
		if len(results) != int(n/2) {
			t.Error("question result duplicated or invented")
			w.WriteHeader(400)
			return
		}
		if n%2 == 1 {
			input, _ := inlineQuestionFixtureValues()
			nativeFixtureToolResponse(w, n, "AskUserQuestion", fmt.Sprintf("toolu_question_%d", (n+1)/2), input)
		} else {
			nativeFixtureTextResponse(w, n)
		}
	}))
	defer provider.Close()
	authority := &rotatingNativeAPIAuthority{token: nativeAPIFixtureToken, authority: nativeAPIAuthority{ctx: ctx, scope: apiproxy.Scope{ExecutionID: domain.NewID(), SessionID: cfg.SessionID, AccountID: domain.NewID(), ConnectionID: domain.NewID(), ProviderID: domain.NewID(), ModelID: domain.NewID(), NativeModel: cfg.Model, Provider: domain.Provider{Name: "Question history fixture", Endpoint: provider.URL + "/provider", Protocol: domain.AnthropicMessages, Authentication: domain.APIKeyAuth}, Operations: []apiproxy.Operation{apiproxy.MessageCreate}}}}
	relay := httptest.NewServer(apiproxy.New(authority, slog.New(slog.NewJSONHandler(io.Discard, nil))))
	defer relay.Close()
	cfg.API.ServerOrigin = relay.URL
	session, err := OpenAPISession(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	sessions := []*APISession{session}
	defer func() {
		for _, current := range sessions {
			if err := current.Close(); err != nil {
				t.Error(err)
			}
			if err := process.ReconcileOwner(cfg.Process.Directory, current.config.Process.OwnerID); err != nil {
				t.Error(err)
			}
		}
	}()
	callbacks := 0
	for turn := 1; turn <= 3; turn++ {
		input := domain.NewID()
		if _, err := session.SendInput(ctx, input, fmt.Sprintf("Continue private question fixture %d.", turn), ContinueSuccessfulRun); err != nil {
			t.Fatal(err)
		}
		finished := false
		var nativeTurn string
		for {
			observed, err := session.Next(ctx)
			if err != nil {
				t.Fatal("native question lifecycle failed", err, logs.String())
			}
			if observed.Kind == InputFinished {
				finished = observed.InputID == input && observed.Result.Successful()
				nativeTurn = observed.TurnID
			}
			if observed.Kind == InteractionObserved && observed.Interaction.Kind == InteractionRequested {
				request := observed.Interaction.Request
				if request.Kind != UserQuestion || request.ToolID != fmt.Sprintf("toolu_question_%d", turn) || callbacks != turn-1 {
					t.Fatal("historical question was replayed")
				}
				callbacks++
				if err := session.Reply(ctx, observed.Interaction.ArrivalID, PermissionReply{Behavior: PermissionAllow, Answers: map[string]string{retainedQuestion: retainedAnswer}}); err != nil {
					t.Fatal(err)
				}
			}
			if observed.Kind == RunStateObserved && observed.Run.State == RunIdle {
				break
			}
		}
		if !finished || callbacks != turn || calls.Load() != int64(2*turn) {
			t.Fatal("original question input did not settle exactly once")
		}
		if _, err := session.FinishOriginalInput(ctx, session.config.Process.OwnerID, cfg.SessionID, input, nativeTurn); err != nil {
			t.Fatal("original question EOF failed", err, logs.String())
		}
		closed, err := session.RetainOriginalCompletion(ctx, session.config.Process.OwnerID, cfg.SessionID, input, nativeTurn)
		if err != nil {
			t.Fatal("original question history could not be retained", err, logs.String())
		}
		raw, reference, err := closed.RetainCheckpoint(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, private := range []string{retainedQuestion, retainedAnswer, cfg.Home, cfg.Workspace, nativeAPIFixtureToken, nativeAPIUpstreamKey} {
			if bytes.Contains(raw, []byte(private)) {
				t.Fatal("checkpoint contains private question content")
			}
		}
		previous := session.config
		previous.API = APIConfig{ServerOrigin: relay.URL}
		inspection := previous
		instructions := checkpointDigest([]byte(inspection.Instructions))
		inspection.Instructions = ""
		if err := InspectCheckpoint(ctx, inspection, instructions, raw, reference); err != nil {
			t.Fatal("original question history inspection failed", err, logs.String())
		}
		closed, err = RestoreCheckpoint(ctx, previous, raw, reference)
		if err != nil {
			t.Fatal("original question checkpoint failed to restore", err, logs.String())
		}
		clear(raw)
		proofs, err := closed.previous.current.closedQuestionAnswers()
		if err != nil || len(proofs) != turn {
			t.Fatal("original question proof lineage changed", err)
		}
		if turn == 3 {
			break
		}
		token := nativeContinuationToken(byte(70 + turn))
		authority.rotate(token)
		session, err = ContinueAPISession(ctx, closed, domain.NewID(), APIConfig{ServerOrigin: relay.URL, Token: token}, ContinueSuccessfulRun)
		if err != nil {
			t.Fatal("question conversation could not replace process", err, logs.String())
		}
		sessions = append(sessions, session)
	}
}
