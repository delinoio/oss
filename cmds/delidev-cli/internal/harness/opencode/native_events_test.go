package opencode

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestManualNativeOpenCodeEvents(t *testing.T) {
	if os.Getenv("DELIDEV_NATIVE_OPENCODE_EXECUTABLE") == "" {
		t.Skip("explicit private native OpenCode event fixture")
	}
	nativeTextEventFixture(t, FinishStop)
}

func TestManualNativeOpenCodeContentFilter(t *testing.T) {
	if os.Getenv("DELIDEV_NATIVE_OPENCODE_EXECUTABLE") == "" {
		t.Skip("explicit private native OpenCode content-filter fixture")
	}
	nativeTextEventFixture(t, FinishContentFilter)
}

func nativeTextEventFixture(t *testing.T, reason FinishReason) {
	key := string(domain.NewID())
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		raw, err := io.ReadAll(io.LimitReader(r.Body, maxHTTPBody+1))
		if err != nil || domain.Decode(raw, &body) != nil || r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer "+key || !scalar(body["model"], fixtureSettings().Model) || string(body["stream"]) != "true" {
			t.Error("native event fixture provider scope mismatch")
			w.WriteHeader(400)
			return
		}
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"id":"chatcmpl-private","object":"chat.completion.chunk","created":1,"model":"private-model","choices":[{"index":0,"delta":{"role":"assistant","content":"Private fixture response"},"finish_reason":null}]}`+"\n\n")
		finish := "stop"
		if reason == FinishContentFilter {
			finish = "content_filter"
		}
		_, _ = fmt.Fprintf(w, `data: {"id":"chatcmpl-private","object":"chat.completion.chunk","created":1,"model":"private-model","choices":[{"index":0,"delta":{},"finish_reason":%q}],"usage":{"prompt_tokens":20,"completion_tokens":4,"total_tokens":24}}`+"\n\ndata: [DONE]\n\n", finish)
	}))
	defer provider.Close()
	api, ctx := nativeSessionFixture(t, provider.URL, key)
	id, err := api.create(ctx, domain.NewID(), fixtureSettings())
	if err != nil {
		t.Fatal(err)
	}
	stream, err := api.openEvents(ctx)
	if err != nil {
		t.Fatalf("native event connection: %v", err)
	}
	defer stream.Close()
	if _, err := api.submit(ctx, domain.NewID(), fixtureMessageID, fixturePartID, "Reply with the private fixture response."); err != nil {
		t.Fatal(err)
	}
	observer, err := api.observeInput(ctx, fixtureNativeRoot())
	if err != nil {
		t.Fatal(err)
	}
	userSeen, partSeen, deltaSeen, assistantDone, idle := false, false, false, false, false
	for count := 0; count < 256 && !(idle && userSeen && partSeen && deltaSeen && assistantDone && observer.snapshot().SettledObserved); count++ {
		event, err := stream.Next(ctx)
		if err != nil {
			t.Fatalf("native event read: %v", err)
		}
		if _, err := observer.observe(ctx, event); err != nil {
			t.Fatalf("native owned event %s: %v", event.Kind, err)
		}
		properties, err := object(event.Properties)
		if err != nil {
			t.Fatal("native event property shape")
		}
		switch event.Kind {
		case MessageUpdatedEvent:
			info, err := object(properties["info"])
			if err != nil || !scalar(properties["sessionID"], id) || !scalar(info["sessionID"], id) {
				t.Fatal("native message escaped original session")
			}
			decoded, err := decodeNativeMessage(properties["info"])
			if err != nil || decoded.SessionID != id {
				t.Fatalf("native typed message: %v", err)
			}
			if scalar(info["role"], "user") {
				userSeen = scalar(info["id"], fixtureMessageID)
			} else if scalar(info["parentID"], fixtureMessageID) && scalar(info["finish"], string(reason)) {
				times, err := object(info["time"])
				if err == nil {
					completed, ok := nativeCount(times["completed"])
					assistantDone = completed > 0 && ok
					if assistantDone && (decoded.Assistant == nil || decoded.Assistant.Completed == nil || decoded.Assistant.Finish == nil || *decoded.Assistant.Finish != reason || decoded.Assistant.Usage.Input != 20 || decoded.Assistant.Usage.Output != 4 || decoded.Assistant.Usage.Total == nil || *decoded.Assistant.Usage.Total != 24 || decoded.Assistant.Cost.String() != "0") {
						t.Fatal("native usage or terminal metadata changed during typed decoding")
					}
					if reason == FinishContentFilter {
						assistantDone = assistantDone && decoded.Assistant.Error != nil && decoded.Assistant.Error.Kind == ContentErrorKind
					}
				}
			}
		case MessagePartUpdatedEvent:
			part, err := object(properties["part"])
			if err != nil || !scalar(properties["sessionID"], id) {
				t.Fatal("native part escaped original session")
			}
			if typed, err := decodeNativePart(properties["part"]); err != nil || typed.SessionID != id {
				t.Fatalf("native typed part: %v", err)
			}
			if scalar(part["id"], fixturePartID) {
				partSeen = scalar(part["messageID"], fixtureMessageID)
			}
		case MessagePartDeltaEvent:
			deltaSeen = scalar(properties["sessionID"], id) && scalar(properties["field"], "text") && scalar(properties["delta"], "Private fixture response")
		case SessionIdleEvent:
			idle = scalar(properties["sessionID"], id)
		}
	}
	if !userSeen || !partSeen || !deltaSeen || !assistantDone || !idle || calls.Load() != 1 {
		t.Fatalf("native event observations: user=%t, part=%t, delta=%t, assistant=%t, idle=%t, provider_calls=%d", userSeen, partSeen, deltaSeen, assistantDone, idle, calls.Load())
	}
	if progress := observer.snapshot(); !progress.SettledObserved || progress.NeedsRecovery {
		t.Fatal("native success did not preserve independent owned terminal and idle")
	}
	if receipt, err := api.inspectInput(ctx); err != nil || !receipt.Recorded {
		t.Fatal("event observations did not retain independent original storage")
	}
}

func TestManualNativeOpenCodeEventHeartbeat(t *testing.T) {
	if os.Getenv("DELIDEV_NATIVE_OPENCODE_EXECUTABLE") == "" {
		t.Skip("explicit private native OpenCode event heartbeat fixture")
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("heartbeat-only fixture invoked a provider")
		w.WriteHeader(400)
	}))
	defer provider.Close()
	api, ctx := nativeSessionFixture(t, provider.URL, string(domain.NewID()))
	if _, err := api.create(ctx, domain.NewID(), fixtureSettings()); err != nil {
		t.Fatal(err)
	}
	stream, err := api.openEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	for count := 0; count < 256; count++ {
		event, err := stream.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if event.Kind == ServerHeartbeatEvent {
			return
		}
	}
	t.Fatal("native event stream did not emit its heartbeat")
}
