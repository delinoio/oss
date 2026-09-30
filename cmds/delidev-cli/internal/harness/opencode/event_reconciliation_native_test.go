package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// Opt-in installed-native evidence. Only the original native event connection
// is severed; its original process, input and scripted provider request survive.
func TestManualNativeOpenCodeEventReconciliation(t *testing.T) {
	executable := os.Getenv("DELIDEV_NATIVE_OPENCODE_EXECUTABLE")
	if executable == "" {
		t.Skip("explicit pinned native OpenCode executable required")
	}
	requireNoManagedOpenCodeConfig(t)
	config := fixtureOwnedAPIConfig(t)
	var logs bytes.Buffer
	config.Probe.Process.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
	defer func() {
		if t.Failed() {
			t.Logf("redacted native phase diagnostics: %s", logs.String())
		}
	}()
	config.Probe.Process.Executable = executable
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/api-proxy/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer "+config.Token {
			t.Error("native recovery replaced original account authority")
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"id":"chatcmpl-private","object":"chat.completion.chunk","created":1,"model":"private-model","choices":[{"index":0,"delta":{"role":"assistant","content":"prefix"},"finish_reason":null}]}`+"\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, `data: {"id":"chatcmpl-private","object":"chat.completion.chunk","created":1,"model":"private-model","choices":[{"index":0,"delta":{"content":" and final suffix"},"finish_reason":null}]}`+"\n\n")
		_, _ = io.WriteString(w, `data: {"id":"chatcmpl-private","object":"chat.completion.chunk","created":1,"model":"private-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":4,"total_tokens":24}}`+"\n\ndata: [DONE]\n\n")
	}))
	defer provider.Close()
	config.ServerOrigin = provider.URL
	var claims []SessionClaim
	config.Claim = func(_ context.Context, claim SessionClaim) error {
		claims = append(claims, claim)
		raw, _ := json.Marshal(claims)
		return security.WriteAtomic(filepath.Join(filepath.Dir(config.Probe.Home), "claims.json"), raw)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	api, err := OpenOwnedAPI(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		if err := api.Close(cleanup); err != nil {
			t.Error(err)
		}
		if err := process.ReconcileOwnerContext(cleanup, config.Probe.Process.Directory, config.Probe.Process.OwnerID); err != nil {
			t.Error(err)
		}
	}()
	if _, err := api.CreateSession(ctx, domain.NewID()); err != nil {
		t.Fatal(err)
	}
	if _, err := api.StartText(ctx, domain.NewID(), "Return the scripted text."); err != nil {
		t.Fatal(err)
	}
	for {
		value, err := api.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if value.Delta != nil && value.Delta.Text == "prefix" {
			break
		}
	}
	original := api.session.creation.identity
	// Closing only the response body produces real transport loss, not disposal,
	// cancellation of the execution lifetime, or a synthetic completion event.
	if err := api.session.events.body.Close(); err != nil {
		t.Fatal(err)
	}
	close(release)
	for {
		progress, err := api.Progress(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if progress.SettledObserved {
			break
		}
		if _, err := api.Next(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if api.session.creation.identity != original || calls.Load() != 1 || len(claims) != 2 || api.session.reconciliation != ReconciliationComplete {
		t.Fatal("native recovery replayed input or replaced original authority")
	}
	history, err := api.InspectHistory(ctx)
	if err != nil || len(history.Messages) != 2 {
		t.Fatal("native recovered history did not verify", err)
	}
	var text string
	for _, part := range api.session.observer.parts {
		if part.value.MessageID == history.AssistantID && part.value.Kind == TextPartKind {
			text += part.text
		}
	}
	if text != "prefix and final suffix" {
		t.Fatal("native recovery lost or duplicated the accepted prefix")
	}
	if _, err := api.CloseCompleted(ctx); err != nil {
		t.Fatal("recovered terminal did not join independent native cleanup", err)
	}
	if calls.Load() != 1 || len(claims) != 2 {
		t.Fatal("history or cleanup performed native inference")
	}
}
