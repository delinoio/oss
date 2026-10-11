// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

func TestGatewayOAuthPassiveStatusPrivacyAndOwnership(t *testing.T) {
	for _, status := range []gatewayOAuthStatus{gatewayNotReady, gatewayStarted, gatewaySucceeded, gatewayFailed} {
		for _, optional := range []string{"", `,"authUrl":null,"error":null`, `,"authUrl":"","error":""`, `,"authUrl":"javascript:private-marker?callback=private-marker","error":"private-marker"`} {
			c, turn := observationClient()
			var logs bytes.Buffer
			c.logger = slog.New(slog.NewJSONHandler(&logs, nil))
			settings, paused := c.execution.settings, c.execution.paused
			raw := json.RawMessage(`{"providerId":"private-marker-foreign-provider","status":"` + string(status) + `"` + optional + `}`)
			for i := 0; i < 3; i++ {
				event, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.Notification, Method: "account/gatewayOAuth/changed", Params: raw})
				if err != nil || event.Kind != MetadataEvent || event.Metadata != GatewayOAuthStatusDiscarded || !event.Correlated || event.ThreadID != c.thread || event.TurnID != "" || event.Native != nil {
					t.Fatal("passive status rejected or exposed", event, err)
				}
				encoded, _ := json.Marshal(event)
				if strings.Contains(string(encoded)+logs.String(), "private-marker") {
					t.Fatal("private gateway data escaped")
				}
				if c.execution.active != turn || c.execution.paused != paused || !reflect.DeepEqual(c.execution.settings, settings) || c.problem != nil {
					t.Fatal("passive status changed original authority")
				}
			}
			if !strings.Contains(logs.String(), string(status)) || !strings.Contains(logs.String(), "passive-discarded") {
				t.Fatal("safe status or policy diagnostic missing")
			}
		}
	}
}

func TestGatewayOAuthRejectsMalformedMetadataAndWrongEnvelopes(t *testing.T) {
	for _, raw := range []string{
		"", "null", "[]", "{}", `{"providerId":"p"}`, `{"status":"started"}`,
		`{"providerId":null,"status":"started"}`, `{"providerId":"p","status":null}`,
		`{"providerId":"","status":"started"}`, `{"providerId":1,"status":"started"}`,
		`{"providerId":"p","status":"unknown"}`, `{"providerId":"p","status":1}`,
		`{"providerId":"p","status":"started","authUrl":{}}`, `{"providerId":"p","status":"started","error":false}`,
		`{"providerId":"p","status":"started","unknown":true}`, `{"providerId":"p","providerId":"q","status":"started"}`,
		`{"providerId":"p","status":"started","status":"failed"}`, `{"providerId":"p","status":"started","authUrl":null,"authUrl":null}`,
		`{"providerId":"p","status":"started","error":"nul\u0000"}`,
		`{"providerId":"p","status":"started","threadId":"foreign-root","turnId":"foreign-turn"}`,
	} {
		c, turn := observationClient()
		if _, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.Notification, Method: "account/gatewayOAuth/changed", Params: json.RawMessage(raw)}); err == nil {
			t.Fatal("malformed status accepted")
		}
		if c.execution.active != turn {
			t.Fatal("rejection replaced original input")
		}
	}
	valid := json.RawMessage(`{"providerId":"p","status":"started"}`)
	c, _ := observationClient()
	event, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.ServerRequest, Method: "account/gatewayOAuth/changed", Params: valid})
	if err == nil && event.Kind != NativeExtensionEvent {
		t.Fatal("request acquired notification authority")
	}
	event, err = (&Client{}).observeEventLocked(nativewire.Event{Kind: nativewire.Notification, Method: "account/gatewayOAuth/changed", Params: valid})
	if err != nil || event.Kind != NativeExtensionEvent {
		t.Fatal("unbound process acquired authority")
	}
	if validationStage("account/gatewayOAuth/changed") != validationGateway {
		t.Fatal("redacted failure classification missing")
	}
}

func TestGatewayOAuthExactBounds(t *testing.T) {
	for _, size := range []int{1024, 1025} {
		c, _ := observationClient()
		_, err := observeFixture(c, "account/gatewayOAuth/changed", map[string]any{"providerId": strings.Repeat("p", size), "status": "notReady"})
		if (err == nil) != (size == 1024) {
			t.Fatal("provider bound mismatch", size)
		}
	}
	c, _ := observationClient()
	prefix, suffix := `{"providerId":"p","status":"failed","error":"`, `"}`
	for _, extra := range []int{0, 1} {
		raw := json.RawMessage(prefix + strings.Repeat("x", c.nativeFrameLimit()-len(prefix)-len(suffix)+extra) + suffix)
		_, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.Notification, Method: "account/gatewayOAuth/changed", Params: raw})
		if (err == nil) != (extra == 0) {
			t.Fatal("native frame bound mismatch", extra)
		}
	}
}

func TestGatewayOAuthPreservesOriginalTurnAndNativeOperations(t *testing.T) {
	c, capture, _, _ := boundTurnFixture(t, "normal")
	settings := c.execution.settings
	accepted, err := c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
	if err != nil {
		t.Fatal(err)
	}
	nextKind(t, c, MessageCompletedEvent)
	for _, status := range []gatewayOAuthStatus{gatewayNotReady, gatewayStarted, gatewaySucceeded, gatewayFailed} {
		fixtureSignal(t, c, "notify", map[string]any{"method": "account/gatewayOAuth/changed", "params": map[string]any{"providerId": "foreign-provider", "status": status, "authUrl": "https://fixture.invalid/authorize?private=marker", "error": "private-marker"}})
		event := nextKind(t, c, MetadataEvent)
		if event.Metadata != GatewayOAuthStatusDiscarded || event.Native != nil || c.problem != nil || c.execution.active != accepted.TurnID || !reflect.DeepEqual(c.execution.settings, settings) {
			t.Fatal("status changed original input")
		}
	}
	fixtureSignal(t, c, "finish", map[string]any{"status": TurnCompleted})
	terminal := nextKind(t, c, TurnCompletedEvent)
	nextKind(t, c, ThreadStatusEvent)
	if terminal.TurnID != accepted.TurnID || c.execution.active != "" || c.problem != nil {
		t.Fatal("original turn did not complete")
	}
	if len(requestsOf(t, capture, "turn/start")) != 1 || len(requestsOf(t, capture, "turn/steer")) != 0 || len(requestsOf(t, capture, "account/login/start")) != 0 || len(requestsOf(t, capture, "account/gatewayOAuth/start")) != 0 {
		t.Fatal("status triggered native work")
	}
}
