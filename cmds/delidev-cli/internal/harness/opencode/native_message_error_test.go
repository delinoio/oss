package opencode

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestManualNativeOpenCodeMessageError(t *testing.T) {
	if os.Getenv("DELIDEV_NATIVE_OPENCODE_EXECUTABLE") == "" {
		t.Skip("explicit private native OpenCode error fixture")
	}
	key := string(domain.NewID())
	var calls atomic.Int32
	const sentinel = "private-native-response-diagnostic"
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer "+key {
			t.Error("native error fixture provider scope mismatch")
			w.WriteHeader(400)
			return
		}
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"`+sentinel+`","type":"invalid_api_key"}}`)
	}))
	defer provider.Close()
	api, ctx := nativeSessionFixture(t, provider.URL, key)
	id, err := api.create(ctx, domain.NewID(), fixtureSettings())
	if err != nil {
		t.Fatal(err)
	}
	stream, err := api.openEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if _, err := api.submit(ctx, domain.NewID(), fixtureMessageID, fixturePartID, "Exercise the private error fixture."); err != nil {
		t.Fatal(err)
	}
	observer, err := api.observeInput(ctx, "/")
	if err != nil {
		t.Fatal(err)
	}
	messageError, eventError, idle := false, false, false
	check := func(native *NativeError) {
		if native == nil || native.Kind != APIErrorKind || native.StatusCode == nil || *native.StatusCode != 401 || native.Retryable == nil || *native.Retryable {
			t.Fatal("native error classification changed")
		}
		raw, _ := json.Marshal(native)
		if strings.Contains(string(raw), sentinel) || strings.Contains(string(raw), provider.URL) || strings.Contains(string(raw), key) {
			t.Fatal("native error diagnostics escaped typed classification")
		}
	}
	for count := 0; count < 256 && !(idle && messageError && eventError && observer.snapshot().SettledObserved); count++ {
		event, err := stream.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := observer.observe(ctx, event); err != nil {
			t.Fatalf("native owned error event %s: %v", event.Kind, err)
		}
		properties, err := object(event.Properties)
		if err != nil {
			t.Fatal("native error event property shape")
		}
		switch event.Kind {
		case MessageUpdatedEvent:
			message, err := decodeNativeMessage(properties["info"])
			if err != nil || message.SessionID != id {
				t.Fatalf("native failed message: %v", err)
			}
			if message.Assistant != nil && message.Assistant.Completed != nil {
				if message.Assistant.ParentID != fixtureMessageID {
					t.Fatal("failed assistant lost original parent input")
				}
				check(message.Assistant.Error)
				messageError = true
			}
		case SessionErrorEvent:
			if !scalar(properties["sessionID"], id) {
				t.Fatal("uncorrelated native failure assigned to original input")
			}
			value, err := decodeNativeError(properties["error"])
			if err != nil {
				t.Fatal(err)
			}
			check(value)
			eventError = true
		case SessionIdleEvent:
			idle = scalar(properties["sessionID"], id)
		}
	}
	if !messageError || !eventError || !idle || calls.Load() != 1 {
		t.Fatalf("native error observations: message=%t event=%t idle=%t requests=%d", messageError, eventError, idle, calls.Load())
	}
	if progress := observer.snapshot(); !progress.SettledObserved || progress.NeedsRecovery {
		t.Fatal("native failed input lost original terminal and idle ownership")
	}
	if receipt, err := api.inspectInput(ctx); err != nil || !receipt.Recorded {
		t.Fatal("native failure erased original input storage")
	}
}
