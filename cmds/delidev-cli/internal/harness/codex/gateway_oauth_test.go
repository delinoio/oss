// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

func TestGatewayOAuthNotificationsRemainPrivateAndInert(t *testing.T) {
	for _, status := range []nativeGatewayStatus{nativeGatewayNotReady, nativeGatewayStarted, nativeGatewaySucceeded, nativeGatewayFailed} {
		for _, url := range []string{"https://fixture.invalid/authorize?private=marker", "javascript:private-marker", "callback:private-marker"} {
			t.Run(string(status)+"/"+url, func(t *testing.T) {
				c, turn := observationClient()
				var logs bytes.Buffer
				c.logger = slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
				state, settings, paused := c.execution, c.execution.settings, c.execution.paused
				inputs, turns := len(c.execution.inputs), len(c.execution.turns)
				params := map[string]any{"providerId": "foreign-private-provider", "status": status, "authUrl": url, "error": "private-marker"}
				for i := 0; i < 2; i++ {
					event, err := observeFixture(c, "account/gatewayOAuth/changed", params)
					if err != nil || event.Kind != MetadataEvent || event.Metadata != GatewayOAuthStatusDiscarded || event.Native != nil || !event.Correlated || event.TurnID != "" {
						t.Fatal("gateway status was not discarded private metadata", err)
					}
					raw, err := json.Marshal(event)
					if err != nil || strings.Contains(string(raw), "private") || strings.Contains(string(raw), "fixture.invalid") || strings.Contains(string(raw), url) || strings.Contains(logs.String(), "private") || strings.Contains(logs.String(), url) {
						t.Fatal("gateway descriptor escaped in event")
					}
					if c.execution != state || c.execution.active != turn || c.execution.paused != paused || !reflect.DeepEqual(c.execution.settings, settings) || len(c.execution.inputs) != inputs || len(c.execution.turns) != turns || c.problem != nil {
						t.Fatal("passive gateway status changed execution authority")
					}
				}
			})
		}
	}
}

func TestGatewayOAuthOptionalStringsPreserveOmissionAndNullPrivately(t *testing.T) {
	for _, fields := range []string{"", `,"authUrl":null,"error":null`, `,"authUrl":"","error":""`} {
		raw := json.RawMessage(`{"providerId":"fixture","status":"notReady"` + fields + `}`)
		value, err := decodeGatewayObservation(raw)
		if err != nil || value.AuthURL.Present != (fields != "") || value.Error.Present != (fields != "") || value.AuthURL.Null != strings.Contains(fields, "null") || value.Error.Null != strings.Contains(fields, "null") {
			t.Fatal("optional gateway presence/null changed", err)
		}
		c, _ := observationClient()
		event, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.Notification, Method: "account/gatewayOAuth/changed", Params: raw})
		if err != nil || event.Metadata != GatewayOAuthStatusDiscarded || event.Native != nil {
			t.Fatal("optional gateway observation was not discarded", err)
		}
	}
}

func TestGatewayOAuthRejectsMalformedAndWrongEnvelope(t *testing.T) {
	for _, raw := range []string{
		`null`, `[]`, `{}`, `{"providerId":null,"status":"started"}`, `{"providerId":"fixture"}`,
		`{"providerId":"fixture","status":null}`, `{"providerId":"fixture","status":"unknown"}`,
		`{"providerId":"fixture","status":"started","authUrl":false}`,
		`{"providerId":"fixture","status":"failed","error":{}}`,
		`{"providerId":"fixture","status":"started","status":"started"}`,
		`{"providerId":"fixture","status":"started","unknown":true}`,
		`{"providerId":"` + strings.Repeat("p", 1025) + `","status":"started"}`,
		`{"providerId":"fixture","status":"failed","error":"` + strings.Repeat("p", nativewire.MaxFrame) + `"}`,
	} {
		c, _ := observationClient()
		_, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.Notification, Method: "account/gatewayOAuth/changed", Params: json.RawMessage(raw)})
		if err == nil || strings.Contains(err.Error(), "fixture") || strings.Contains(err.Error(), "unknown") {
			t.Fatal("malformed gateway observation accepted or diagnostic reflected content")
		}
	}
	c, _ := observationClient()
	event, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.ServerRequest, Method: "account/gatewayOAuth/changed", ID: json.RawMessage(`1`), Token: domain.NewID(), Params: json.RawMessage(`{"providerId":"fixture","status":"succeeded"}`)})
	if err != nil || event.Kind != NativeExtensionEvent || event.Metadata != "" {
		t.Fatal("same-name request gained notification or login authority", err)
	}
}
