package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const nativeStopUpstreamKey = "Z-native-stop-fixture-key"

type nativeStopAuthority struct{ nativeAPIAuthority }

func (a nativeStopAuthority) Acquire(ctx context.Context, token string) (*apiproxy.Lease, error) {
	l, err := a.nativeAPIAuthority.Acquire(ctx, token)
	if err == nil {
		// Avoid an unresolved synthetic key prefix in otherwise innocuous
		// partial metadata; the real relay still performs its full secret guard.
		l.Key = func(context.Context) ([]byte, error) { return []byte(nativeStopUpstreamKey), nil }
	}
	return l, err
}

func TestManualNativeClaudeInterruptStreaming(t *testing.T) {
	for _, permission := range []NativePermission{DefaultPermission, PlanPermission} {
		t.Run(string(permission), func(t *testing.T) { nativeInterruptStreaming(t, permission) })
	}
}

func nativeInterruptStreaming(t *testing.T, permission NativePermission) {
	binary := os.Getenv("DELIDEV_NATIVE_CLAUDE_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit native binary and private scripted provider required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cfg, logs := apiFixtureConfig(t, "native-interrupt")
	cfg.Process.Executable, cfg.Model, cfg.Permission = binary, "fixture-model", permission
	var calls atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		_, _ = io.Copy(io.Discard, r.Body)
		if calls.Add(1) != 1 || r.Method != http.MethodPost || r.URL.Path != "/provider/messages" || r.Header.Get("X-Api-Key") != nativeStopUpstreamKey {
			t.Error("unexpected native stop provider request")
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []map[string]any{
			{"type": "message_start", "message": map[string]any{"id": "msg_stop_fixture", "type": "message", "role": "assistant", "content": []any{}, "model": cfg.Model, "stop_reason": nil, "stop_sequence": nil, "usage": map[string]any{"input_tokens": 1, "output_tokens": 0}}},
			{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}},
			{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": "Private interrupted fixture."}},
		} {
			raw, _ := json.Marshal(event)
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], raw)
		}
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-ctx.Done():
		}
	}))
	defer provider.Close()
	authority := nativeAPIAuthority{ctx: ctx, scope: apiproxy.Scope{ExecutionID: domain.NewID(), SessionID: cfg.SessionID, AccountID: domain.NewID(), ConnectionID: domain.NewID(), ProviderID: domain.NewID(), ModelID: domain.NewID(), NativeModel: cfg.Model, Provider: domain.Provider{Name: "Stop fixture", Endpoint: provider.URL + "/provider", Protocol: domain.AnthropicMessages, Authentication: domain.APIKeyAuth}, Operations: []apiproxy.Operation{apiproxy.MessageCreate}}}
	relay := httptest.NewServer(apiproxy.New(nativeStopAuthority{authority}, slog.New(slog.NewJSONHandler(io.Discard, nil))))
	defer relay.Close()
	cfg.API.ServerOrigin = relay.URL
	s, err := OpenAPISession(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cancel()
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	}()
	input := domain.NewID()
	if _, err := s.SendInput(ctx, input, "Use the private slow fixture.", ContinueSuccessfulRun); err != nil {
		t.Fatal(err)
	}
	interrupted := false
	var accepted, partial, marker, results, claims int
	request := domain.NewID()
	for {
		o, err := s.Next(ctx)
		if err != nil {
			t.Log(logs.String())
			t.Fatal(err)
		}
		if o.Kind == InputAccepted {
			accepted++
		}
		if o.Kind == InputFinished || o.Kind == UncorrelatedTermination {
			t.Fatal("Stop result changed original input outcome")
		}
		for _, c := range o.Content {
			if c.Kind == ContentChanged && !interrupted {
				response, err := s.Interrupt(ctx, request, func(_ context.Context, claim InterruptClaim) error {
					claims++
					if claim != (InterruptClaim{1, cfg.Process.OwnerID, cfg.SessionID, input, o.TurnID, request}) {
						t.Fatal("Stop claim changed original run")
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				if !response.Claimed || !response.Attempted || !response.Acknowledged || response.NativeResult != nil || response.Idle || response.CleanupJoined {
					t.Fatal("acknowledgment invented completion")
				}
				interrupted = true
			}
			if c.Kind == ContentInterrupted {
				if c.Block == nil || c.Block.Kind != TextBlock || c.Block.Text == nil || *c.Block.Text != "Private interrupted fixture." {
					t.Fatal("original partial content changed")
				}
				partial++
			}
			if c.Kind == ContentCompleted || c.Kind == ProviderMessageFinished {
				t.Fatal("interrupted stream became completed content")
			}
			if c.Kind == NativeInterruptContext {
				marker++
			}
		}
		if o.Kind == InterruptResultObserved {
			if o.InputID != "" || o.Accepted || o.Result == nil || o.Result.Reason != AbortedStreaming {
				t.Fatal("Stop result invented native input correlation")
			}
			results++
			t.Logf("original Stop result kind=%s reason=%s is_error=%t", o.Result.Kind, o.Result.Reason, o.Result.Error)
		}
		if o.Run != nil && o.Run.State == RunIdle {
			break
		}
	}
	if !interrupted || calls.Load() != 1 || accepted != 1 || partial != 1 || marker != 1 || results != 1 || claims != 1 {
		t.Fatal("native interrupt did not retain original observations")
	}
	if _, err := s.Interrupt(ctx, domain.NewID(), func(context.Context, InterruptClaim) error { t.Fatal("duplicate Stop claimed"); return nil }); err == nil {
		t.Fatal("Stop replayed")
	}
	if _, err := s.SendInput(ctx, domain.NewID(), "Never replay this stopped input.", ResumeTerminalRun); err == nil {
		t.Fatal("Stop gained live Resume")
	}
	if _, err := s.CloseForContinuation(ctx); err == nil {
		t.Fatal("Stop manufactured checkpoint authority")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	observed, err := s.InspectInterrupt()
	if err != nil || !observed.Acknowledged || !observed.Idle || !observed.CleanupJoined || observed.NativeResult == nil || observed.ResultCorrelated || observed.NativeResult.Reason != AbortedStreaming {
		t.Fatal("native Stop cleanup lost its separate evidence", err)
	}
	for _, private := range []string{nativeAPIFixtureToken, nativeStopUpstreamKey, cfg.Workspace, "Private interrupted fixture."} {
		if strings.Contains(logs.String(), private) {
			t.Fatal("Stop diagnostics exposed private data")
		}
	}
}
