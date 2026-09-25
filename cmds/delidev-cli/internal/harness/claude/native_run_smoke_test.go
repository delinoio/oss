package claude

import (
	"context"
	"encoding/json"
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

func TestManualNativeBackgroundRunBoundary(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_CLAUDE_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit native binary and private scripted provider required")
	}
	cfg, logs := apiFixtureConfig(t, "native-run-boundary")
	cfg.Process.Executable = binary
	cfg.Model = "fixture-model"
	cfg.Permission = DefaultPermission
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var calls atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/provider/messages" || r.Header.Get("X-Api-Key") != nativeAPIUpstreamKey || r.Header.Get("Authorization") != "" {
			t.Error("native run authority changed")
			w.WriteHeader(400)
			return
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, maxStreamFrame+1))
		var body struct {
			Model  string `json:"model"`
			Stream bool   `json:"stream"`
		}
		if err != nil || len(raw) > maxStreamFrame || json.Unmarshal(raw, &body) != nil || body.Model != cfg.Model || !body.Stream {
			t.Error("native run request changed")
			w.WriteHeader(400)
			return
		}
		n := calls.Add(1)
		if n == 1 {
			nativeFixtureToolResponse(w, n, "Agent", "toolu_background_child", map[string]any{"description": "Owned async child", "prompt": "Return the private async fixture response.", "subagent_type": "general-purpose", "run_in_background": true})
		} else if n <= 5 {
			nativeFixtureTextResponse(w, n)
		} else {
			t.Error("native run repeated unexpected provider operations")
			w.WriteHeader(400)
		}
	}))
	defer provider.Close()
	authority := nativeAPIAuthority{ctx: ctx, scope: apiproxy.Scope{ExecutionID: domain.NewID(), SessionID: cfg.SessionID, AccountID: domain.NewID(), ConnectionID: domain.NewID(), ProviderID: domain.NewID(), ModelID: domain.NewID(), NativeModel: cfg.Model, Provider: domain.Provider{Name: "Run boundary fixture", Endpoint: provider.URL + "/provider", Protocol: domain.AnthropicMessages, Authentication: domain.APIKeyAuth}, Operations: []apiproxy.Operation{apiproxy.MessageCreate}}}
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
	if _, err := s.SendInput(ctx, input, "Run the asynchronous child fixture.", ContinueSuccessfulRun); err != nil {
		t.Fatal(err)
	}
	var original, synthetic, idle bool
	var originalTurn, continuationTurn string
	for !idle {
		observation, err := s.Next(ctx)
		if err != nil {
			t.Log(logs.String())
			t.Fatal(err)
		}
		switch observation.Kind {
		case SessionInitialized:
			originalTurn = observation.TurnID
		case ContinuationInitialized:
			if !original || observation.InputID != "" || observation.Accepted || observation.TurnID == originalTurn || observation.TurnID == "" {
				t.Fatal("automatic continuation borrowed original input acceptance or turn identity")
			}
			continuationTurn = observation.TurnID
		case InputFinished:
			if observation.InputID != input || !observation.Accepted || observation.Result == nil || !observation.Result.Successful() || observation.TurnID != originalTurn {
				t.Fatal("original native result was not retained")
			}
			original = true
		case ContinuationFinished:
			if observation.InputID != "" || observation.Accepted || observation.Result == nil || !observation.Result.Successful() || observation.Result.Origin == nil || *observation.Result.Origin != TaskNotificationOrigin || observation.TurnID != continuationTurn {
				t.Fatal("automatic result lost original native origin or turn identity")
			}
			synthetic = true
		case RunStateObserved:
			if observation.Run.State == RunIdle {
				if !original || !synthetic {
					t.Fatal("native idle preceded automatic continuation completion")
				}
				idle = true
			}
		}
	}
	if calls.Load() != 4 {
		t.Fatal("native run changed its expected provider operations")
	}
	second := domain.NewID()
	if _, err := s.SendInput(ctx, second, "Continue only after the original native run is idle.", ContinueSuccessfulRun); err != nil {
		t.Fatal(err)
	}
	var secondFinished bool
	for {
		observation, err := s.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if observation.Kind == ContinuationInitialized || observation.Kind == ContinuationFinished {
			t.Fatal("historical task notification created another automatic turn")
		}
		if observation.Kind == InputFinished {
			if observation.InputID != second || !observation.Result.Successful() {
				t.Fatal("next input borrowed earlier native result")
			}
			secondFinished = true
		}
		if observation.Kind == RunStateObserved && observation.Run.State == RunIdle {
			break
		}
	}
	if !secondFinished || calls.Load() != 5 {
		t.Fatal("second input did not complete exactly once")
	}

	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	nativeFixtureChildHistory(t, cfg, s.current.tasks, nil)
}
