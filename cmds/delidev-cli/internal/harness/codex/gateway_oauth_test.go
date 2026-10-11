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

func gatewayFixture(status gatewayOAuthStatus, url string) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{"providerId": "private-provider-marker", "status": status, "authUrl": url, "error": "private-error-marker"})
	return raw
}

func TestGatewayOAuthPreservesPrivateOptionalStates(t *testing.T) {
	for _, tc := range []struct {
		raw   string
		state gatewayFieldState
	}{
		{`{"providerId":"provider","status":"notReady"}`, gatewayFieldOmitted},
		{`{"providerId":"provider","status":"notReady","authUrl":null,"error":null}`, gatewayFieldNull},
		{`{"providerId":"provider","status":"notReady","authUrl":"","error":""}`, gatewayFieldString},
	} {
		observed, err := decodeGatewayOAuth(json.RawMessage(tc.raw))
		if err != nil || observed.authURL.state != tc.state || observed.failure.state != tc.state {
			t.Fatal("optional native field state was lost", tc.state, err)
		}
		encoded, err := json.Marshal(observed)
		if err != nil || string(encoded) != "{}" {
			t.Fatal("private gateway observation is serializable", err)
		}
	}
}

func TestGatewayOAuthStatusesAreInertAndDiscarded(t *testing.T) {
	for _, status := range []gatewayOAuthStatus{gatewayOAuthNotReady, gatewayOAuthStarted, gatewayOAuthSucceeded, gatewayOAuthFailed} {
		for _, url := range []string{"https://fixture.invalid/authorize?code=private-url-marker", "javascript:private-url-marker", "file:///private-url-marker", "not a URL private-url-marker"} {
			c, turn := observationClient()
			settings, execution, paused := c.execution.settings, c.execution, c.execution.paused
			beforeInputs, beforePending := len(c.execution.inputs), len(c.execution.pending)
			for i := 0; i < 3; i++ {
				event, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.Notification, Method: "account/gatewayOAuth/changed", Params: gatewayFixture(status, url)})
				if err != nil || event.Kind != MetadataEvent || event.Metadata != GatewayOAuthStatusDiscarded || event.gatewayStatus != status || event.Native != nil || !event.Correlated || event.ThreadID != c.thread || event.TurnID != "" {
					t.Fatal("valid gateway notification did not become discarded metadata", err)
				}
				encoded, _ := json.Marshal(event)
				if strings.Contains(string(encoded), "private-") || strings.Contains(string(encoded), "authUrl") {
					t.Fatal("gateway metadata escaped into a serialized event")
				}
				if c.execution != execution || c.execution.active != turn || c.execution.paused != paused || c.problem != nil || len(c.execution.inputs) != beforeInputs || len(c.execution.pending) != beforePending || !reflect.DeepEqual(c.execution.settings, settings) {
					t.Fatal("passive status changed original execution authority")
				}
			}
		}
	}
	if validationStage("account/gatewayOAuth/changed") != validationGateway {
		t.Fatal("gateway validation lost its closed diagnostic stage")
	}
	c, _ := observationClient()
	event, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.ServerRequest, Method: "account/gatewayOAuth/changed", ID: json.RawMessage(`7`), Token: domain.NewID(), Params: gatewayFixture(gatewayOAuthSucceeded, "private-url-marker")})
	if err == nil && event.Kind != NativeExtensionEvent {
		t.Fatal("server request acquired passive notification authority")
	}
	event, err = (&Client{}).observeEventLocked(nativewire.Event{Kind: nativewire.Notification, Method: "account/gatewayOAuth/changed", Params: gatewayFixture(gatewayOAuthSucceeded, "private-url-marker")})
	if err != nil || event.Kind != NativeExtensionEvent {
		t.Fatal("unbound notification acquired execution authority", err)
	}
}

func TestGatewayOAuthRejectsMalformedPrivateMetadata(t *testing.T) {
	invalid := []string{
		``, `null`, `[]`, `{}`, `{"providerId":"p"}`, `{"status":"started"}`,
		`{"providerId":null,"status":"started"}`, `{"providerId":"p","status":null}`,
		`{"providerId":"p","status":"unknown"}`, `{"providerId":" ","status":"started"}`,
		`{"providerId":1,"status":"started"}`, `{"providerId":"p","status":1}`,
		`{"providerId":"p","status":"started","authUrl":{}}`, `{"providerId":"p","status":"started","error":false}`,
		`{"providerId":"p","providerId":"q","status":"started"}`, `{"providerId":"p","status":"started","status":"failed"}`,
		`{"providerId":"p","status":"started","authUrl":null,"authUrl":"url"}`,
		`{"providerId":"p","status":"started","error":null,"error":"error"}`,
		`{"providerId":"p","status":"started","unknown":true}`,
		`{"providerId":"p","status":"started","authUrl":"bad\u0000url"}`,
	}
	longProvider, _ := json.Marshal(map[string]any{"providerId": strings.Repeat("p", 1025), "status": "started"})
	largeFrame, _ := json.Marshal(map[string]any{"providerId": "p", "status": "started", "error": strings.Repeat("e", nativewire.MaxFrame)})
	invalid = append(invalid, string(longProvider), string(largeFrame), string([]byte{'{', '"', 0xff, '"', ':', '1', '}'}))
	for index, raw := range invalid {
		if _, err := decodeGatewayOAuth(json.RawMessage(raw)); err == nil {
			t.Fatalf("malformed gateway payload %d was accepted", index)
		}
	}
}

func TestGatewayOAuthOriginalTurnAndSafeLogs(t *testing.T) {
	c, capture, _, _ := boundTurnFixture(t, "normal")
	var logs bytes.Buffer
	c.logger = slog.New(slog.NewJSONHandler(&logs, nil))
	accepted, err := c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
	if err != nil {
		t.Fatal(err)
	}
	nextKind(t, c, MessageCompletedEvent)
	for _, status := range []gatewayOAuthStatus{gatewayOAuthNotReady, gatewayOAuthStarted, gatewayOAuthSucceeded, gatewayOAuthFailed} {
		fixtureSignal(t, c, "notify", map[string]any{"method": "account/gatewayOAuth/changed", "params": json.RawMessage(gatewayFixture(status, "https://fixture.invalid/?token=private-url-marker"))})
		event := nextKind(t, c, MetadataEvent)
		if event.Metadata != GatewayOAuthStatusDiscarded || c.problem != nil || c.execution.active != accepted.TurnID || c.execution.paused {
			t.Fatal("gateway status interrupted the original accepted turn")
		}
	}
	if strings.Contains(logs.String(), "private-") || !strings.Contains(logs.String(), `"status":"succeeded"`) || !strings.Contains(logs.String(), string(GatewayOAuthStatusDiscarded)) {
		t.Fatal("gateway logs omitted the safe classification or retained private data")
	}
	fixtureSignal(t, c, "finish", map[string]any{"status": TurnCompleted})
	terminal := nextKind(t, c, TurnCompletedEvent)
	nextKind(t, c, ThreadStatusEvent)
	if terminal.TurnID != accepted.TurnID || c.problem != nil || c.execution.active != "" || len(requestsOf(t, capture, "turn/start")) != 1 || len(requestsOf(t, capture, "turn/steer")) != 0 || len(requestsOf(t, capture, "account/login/start")) != 0 {
		t.Fatal("passive status supplied or replaced original native authority")
	}
}
