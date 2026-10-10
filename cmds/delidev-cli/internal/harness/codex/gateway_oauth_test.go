// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

func TestGatewayOAuthDiscardsCompletePrivateNotifications(t *testing.T) {
	for _, status := range []gatewayOAuthStatus{gatewayNotReady, gatewayStarted, gatewaySucceeded, gatewayFailed} {
		for _, url := range []string{"https://fixture.invalid/authorize?private=marker", "javascript:private-marker", "file:///private-marker", "custom:callback?code=private-marker"} {
			raw, _ := json.Marshal(map[string]any{"providerId": "foreign-private-marker", "status": status, "authUrl": url, "error": "private-marker"})
			c, turn := observationClient()
			settings, paused := c.execution.settings, c.execution.paused
			for i := 0; i < 3; i++ {
				event, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.Notification, Method: "account/gatewayOAuth/changed", Params: raw})
				if err != nil || event.Kind != MetadataEvent || event.Metadata != GatewayOAuthStatusDiscarded || event.Native != nil || !event.Correlated || event.ThreadID != c.thread || event.TurnID != "" {
					t.Fatal("passive gateway metadata was not discarded")
				}
				encoded, _ := json.Marshal(event)
				if strings.Contains(string(encoded), "private-marker") || strings.Contains(string(encoded), url) {
					t.Fatal("private gateway metadata escaped")
				}
				if c.problem != nil || c.execution.active != turn || c.execution.paused != paused || !reflect.DeepEqual(c.execution.settings, settings) {
					t.Fatal("gateway status changed original authority")
				}
			}
		}
	}
}
func TestGatewayOAuthRetainsPrivateOmissionAndNull(t *testing.T) {
	for _, tc := range []struct {
		fields   string
		presence gatewayStringPresence
		value    string
	}{{"", gatewayStringOmitted, ""}, {`,"authUrl":null,"error":null`, gatewayStringNull, ""}, {`,"authUrl":"","error":""`, gatewayStringValue, ""}, {`,"authUrl":"inert","error":"inert"`, gatewayStringValue, "inert"}} {
		observed, err := decodeGatewayOAuthNotification(json.RawMessage(`{"providerId":"provider","status":"started"` + tc.fields + `}`))
		if err != nil || observed.authURL.presence != tc.presence || observed.diagnostic.presence != tc.presence || observed.authURL.value != tc.value || observed.diagnostic.value != tc.value {
			t.Fatal("private optional state lost")
		}
		encoded, _ := json.Marshal(observed)
		if string(encoded) != "{}" {
			t.Fatal("private decoder exposed metadata")
		}
	}
}
func TestGatewayOAuthRejectsMalformedMetadata(t *testing.T) {
	valid := `{"providerId":"private-marker","status":"started"`
	raws := []string{"", "null", "[]", "{}", `{"providerId":null,"status":"started"}`, `{"providerId":"","status":"started"}`, `{"status":"started"}`, `{"providerId":"private-marker"}`, valid + `,"status":"failed"}`, valid + `,"providerId":"other"}`, valid + `,"unknown":"private-marker"}`, valid + `,"authUrl":1}`, valid + `,"error":{}}`, valid + `,"authUrl":null,"authUrl":null}`, valid + `,"error":null,"error":null}`, `{"providerId":"provider","status":null}`, `{"providerId":"provider","status":"unknown"}`, valid + `,"authUrl":"private\u0000marker"}`, valid + `,"error":"private\u0000marker"}`}
	longProvider, _ := json.Marshal(map[string]any{"providerId": strings.Repeat("x", 1025), "status": "started"})
	longURL, _ := json.Marshal(map[string]any{"providerId": "private-marker", "status": "started", "authUrl": strings.Repeat("x", nativewire.MaxFrame+1)})
	longError, _ := json.Marshal(map[string]any{"providerId": "private-marker", "status": "failed", "error": strings.Repeat("x", nativewire.MaxFrame+1)})
	raws = append(raws, string(longProvider), string(longURL), string(longError))
	for i, raw := range raws {
		c, turn := observationClient()
		_, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.Notification, Method: "account/gatewayOAuth/changed", Params: json.RawMessage(raw)})
		if err == nil || strings.Contains(err.Error(), "private-marker") || c.execution.active != turn {
			t.Fatalf("malformed gateway payload %d accepted or exposed", i)
		}
	}
	c, _ := observationClient()
	event, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.ServerRequest, Method: "account/gatewayOAuth/changed", Params: json.RawMessage(valid + `}`)})
	if err == nil && event.Kind != NativeExtensionEvent {
		t.Fatal("server request gained notification authority")
	}
	if validationStage("account/gatewayOAuth/changed") != validationGateway {
		t.Fatal("missing redacted gateway classification")
	}
}
func TestGatewayOAuthPreservesOriginalAcceptedTurn(t *testing.T) {
	c, capture, _, _ := boundTurnFixture(t, "normal")
	settings := c.execution.settings
	accepted, err := c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
	if err != nil {
		t.Fatal(err)
	}
	nextKind(t, c, MessageCompletedEvent)
	for _, status := range []gatewayOAuthStatus{gatewayNotReady, gatewayStarted, gatewaySucceeded, gatewayFailed} {
		fixtureSignal(t, c, "notify", map[string]any{"method": "account/gatewayOAuth/changed", "params": map[string]any{"providerId": "foreign", "status": status, "authUrl": "custom:inert", "error": "private"}})
		event := nextKind(t, c, MetadataEvent)
		if event.Metadata != GatewayOAuthStatusDiscarded || c.problem != nil || c.execution.active != accepted.TurnID || !reflect.DeepEqual(c.execution.settings, settings) {
			t.Fatal("passive status changed original turn")
		}
	}
	fixtureSignal(t, c, "finish", map[string]any{"status": TurnCompleted})
	terminal := nextKind(t, c, TurnCompletedEvent)
	nextKind(t, c, ThreadStatusEvent)
	if terminal.TurnID != accepted.TurnID || c.execution.active != "" || c.problem != nil {
		t.Fatal("original turn completion changed")
	}
	if len(requestsOf(t, capture, "turn/start")) != 1 || len(requestsOf(t, capture, "turn/steer")) != 0 || len(requestsOf(t, capture, "account/login/start")) != 0 || len(requestsOf(t, capture, "account/gatewayOAuth/login")) != 0 {
		t.Fatal("notification triggered another native operation")
	}
}
