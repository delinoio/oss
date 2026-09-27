package claude

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
)

// Every run owns a fresh private runtime and wholly generated provider content.
func TestManualNativeInterruptedDenialPreservesContextWithoutInputOutcome(t *testing.T) {
	for _, test := range []struct {
		name, tool string
		permission NativePermission
	}{
		{"tool", "Bash", DefaultPermission}, {"question", "AskUserQuestion", DefaultPermission}, {"plan-question", "AskUserQuestion", PlanPermission},
	} {
		t.Run(test.name, func(t *testing.T) { nativeInterruptedDenial(t, test.tool, test.permission) })
	}
}
func nativeInterruptedDenial(t *testing.T, tool string, permission NativePermission) {
	binary := os.Getenv("DELIDEV_NATIVE_CLAUDE_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit pinned binary and private provider required")
	}
	cfg, logs := apiFixtureConfig(t, "interrupted-denial")
	cfg.Process.Executable, cfg.Model, cfg.Permission = binary, "fixture-model", permission
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var requests atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if r.Method != http.MethodPost || r.URL.Path != "/provider/messages" || r.Header.Get("X-Api-Key") != nativeAPIUpstreamKey || requests.Add(1) != 1 {
			t.Error("denial fixture repeated inference or changed authority")
			w.WriteHeader(400)
			return
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(r.Body, maxStreamFrame+1))
		params := map[string]any{"command": "printf fixture > interrupted-denial-marker.txt"}
		if tool == "AskUserQuestion" {
			params = questionInput()
		}
		nativeFixtureToolResponse(w, 1, tool, "toolu_denial_original", params)
	}))
	defer provider.Close()
	authority := nativeAPIAuthority{ctx: ctx, scope: apiproxy.Scope{ExecutionID: domain.NewID(), SessionID: cfg.SessionID, AccountID: domain.NewID(), ConnectionID: domain.NewID(), ProviderID: domain.NewID(), ModelID: domain.NewID(), NativeModel: cfg.Model, Provider: domain.Provider{Name: "Denial fixture", Endpoint: provider.URL + "/provider", Protocol: domain.AnthropicMessages, Authentication: domain.APIKeyAuth}, Operations: []apiproxy.Operation{apiproxy.MessageCreate}}}
	relay := httptest.NewServer(apiproxy.New(authority, slog.New(slog.NewJSONHandler(io.Discard, nil))))
	defer relay.Close()
	cfg.API.ServerOrigin = relay.URL
	s, err := OpenAPISession(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
		if err := process.ReconcileOwner(cfg.Process.Directory, cfg.Process.OwnerID); err != nil {
			t.Error(err)
		}
	}()
	input := domain.NewID()
	if _, err := s.SendInput(ctx, input, "Inspect the private denial fixture.", ContinueSuccessfulRun); err != nil {
		t.Fatal(err)
	}
	var arrival domain.ID
	replies, echoes, rejected, contexts, results := 0, 0, 0, 0, 0
	for {
		o, err := s.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		switch o.Kind {
		case InteractionObserved:
			if o.Interaction.Kind == InteractionRequested {
				if arrival != "" || o.Interaction.Request.ToolName != tool {
					t.Fatal("interrupted callback changed original ownership")
				}
				arrival = o.Interaction.ArrivalID
				err := s.ReplyClaimed(ctx, arrival, PermissionReply{Behavior: PermissionDeny, Message: "Original fixture denial", Interrupt: true}, func(_ context.Context, c PermissionReplyClaim) error {
					if c.OwnerID != cfg.Process.OwnerID || c.SessionID != cfg.SessionID || c.InputID != input || c.ArrivalID != arrival || c.ToolID != "toolu_denial_original" {
						t.Fatal("interruption reply claim changed original ownership")
					}
					replies++
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
			} else if o.Interaction.Kind == InteractionReplyEchoed {
				echoes++
			} else {
				t.Fatal("denial fabricated callback cancellation")
			}
		case ContentObserved:
			for _, event := range o.Content {
				if event.Kind == ToolResultObserved {
					result := event.ToolResult
					if result == nil || result.ID != "toolu_denial_original" || result.NonExecution == nil || result.NonExecution.Kind != domain.ClaudeUserRejectedNonExecution || result.Error == nil || !*result.Error {
						t.Fatal("native interrupted denial lost non-execution evidence")
					}
					rejected++
				}
				if event.Kind == NativeCallbackInterruptContext {
					if event.CallbackArrivalID != arrival || len(event.Blocks) != 1 || event.Blocks[0].Text == nil || *event.Blocks[0].Text != "[Request interrupted by user for tool use]" {
						t.Fatal("original interrupted tool context changed")
					}
					contexts++
				}
			}
		case CallbackInterruptResultObserved:
			var fields map[string]json.RawMessage
			if o.Native == nil || json.Unmarshal(o.Native.Body, &fields) != nil || fields["user_message_uuid"] != nil || o.InputID != "" || o.Accepted || o.CallbackArrivalID != arrival || o.Result == nil || o.Result.Kind != ResultExecutionError || o.Result.Reason != AbortedTools || !o.Result.Error {
				t.Fatal("missing native input identity was invented")
			}
			results++
		case InputFinished, UncorrelatedTermination, InterruptResultObserved:
			t.Fatal("callback interruption acquired unrelated input/Stop authority")
		case RunStateObserved:
			if o.Run.State == RunIdle && results != 0 {
				if replies != 1 || echoes != 1 || rejected != 1 || contexts != 1 || results != 1 || requests.Load() != 1 {
					t.Fatal("interrupted original reply repeated or lost evidence")
				}
				if s.current.finished || s.current.terminal != nil || s.current.interrupt != nil || s.current.interruptResult == nil {
					t.Fatal("callback interruption became input completion or explicit Stop")
				}
				if _, err := s.SendInput(ctx, domain.NewID(), "Do not adopt missing input outcome.", ResumeTerminalRun); err == nil {
					t.Fatal("uncorrelated result granted another input")
				}
				if _, err := s.Interrupt(ctx, domain.NewID(), func(context.Context, InterruptClaim) error {
					t.Fatal("callback interruption sent separate Stop")
					return nil
				}); err == nil {
					t.Fatal("callback interruption allowed separate Stop")
				}
				if _, err := os.Stat(filepath.Join(cfg.Workspace, "interrupted-denial-marker.txt")); !os.IsNotExist(err) {
					t.Fatal("interrupted denied command executed", err)
				}
				result, err := s.FinishOriginalDenial(ctx, cfg.Process.OwnerID, cfg.SessionID, input, s.current.turnID, arrival)
				if err != nil || result.Kind != ResultExecutionError || result.Reason != AbortedTools || !result.Error || result.Usage != nil || !s.cleanupJoined.Load() || s.current.finished || s.current.terminal != nil {
					t.Fatal("original denial clean EOF lost separate uncorrelated result", err)
				}
				for _, text := range []string{"Original fixture denial", "Inspect the private denial fixture.", cfg.Workspace, nativeAPIFixtureToken, nativeAPIUpstreamKey} {
					if strings.Contains(logs.String(), text) {
						t.Fatal("private interruption data entered logs")
					}
				}
				return
			}
		}
	}
}
