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

func TestGatewayOAuthNotificationDiscardsPrivatePayloadWithoutAuthority(t *testing.T) {
	for _, status := range []GatewayOAuthStatus{GatewayOAuthNotReady, GatewayOAuthStarted, GatewayOAuthSucceeded, GatewayOAuthFailed} {
		c, turn := observationClient()
		state := c.execution
		beforeSettings, beforeThread := state.settings, state.thread
		beforeInputs, beforePending, beforeTurns := len(state.inputs), len(state.pending), len(state.turns)
		params := map[string]any{"providerId": "foreign-private-marker", "status": status, "authUrl": "https://fixture.invalid/authorize?callback=private-marker", "error": "private-marker"}
		for repeat := 0; repeat < 3; repeat++ {
			event, err := observeFixture(c, "account/gatewayOAuth/changed", params)
			if err != nil || event.Kind != MetadataEvent || event.Metadata != GatewayOAuthStatusDiscarded || event.GatewayOAuthStatus != status || !event.Correlated || event.ThreadID != c.thread || event.TurnID != "" || event.Native != nil {
				t.Fatal("gateway observation did not discard private metadata", err)
			}
			if c.execution != state || state.active != turn || state.paused || c.problem != nil || !reflect.DeepEqual(state.settings, beforeSettings) || !reflect.DeepEqual(state.thread, beforeThread) || len(state.inputs) != beforeInputs || len(state.pending) != beforePending || len(state.turns) != beforeTurns {
				t.Fatal("passive gateway observation changed original input or settings")
			}
			serialized, _ := json.Marshal(event)
			if strings.Contains(string(serialized), "private-marker") || strings.Contains(string(serialized), "fixture.invalid") || strings.Contains(string(serialized), string(status)) {
				t.Fatal("private gateway metadata entered serialized events")
			}
		}
		request, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.ServerRequest, Method: "account/gatewayOAuth/changed", Params: json.RawMessage(`{"providerId":"fixture","status":"succeeded"}`)})
		if err == nil && request.Metadata == GatewayOAuthStatusDiscarded {
			t.Fatal("same-name server request gained notification admission")
		}
	}
	c := &Client{}
	event, err := observeFixture(c, "account/gatewayOAuth/changed", map[string]any{"providerId": "fixture", "status": "notReady"})
	if err != nil || event.Metadata != GatewayOAuthStatusDiscarded || c.execution != nil || c.thread != "" {
		t.Fatal("connection telemetry invented an execution owner", err)
	}
}

func TestGatewayOAuthNextEventPreservesOriginalTurnAndRedactedLogs(t *testing.T) {
	c, capture, _, _ := boundTurnFixture(t, "normal")
	var logs bytes.Buffer
	c.logger = slog.New(slog.NewJSONHandler(&logs, nil))
	settings := c.execution.settings
	accepted, err := c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
	if err != nil {
		t.Fatal(err)
	}
	nextKind(t, c, MessageCompletedEvent)
	beforeStarts := len(requestsOf(t, capture, "turn/start"))
	for _, status := range []GatewayOAuthStatus{GatewayOAuthNotReady, GatewayOAuthStarted, GatewayOAuthSucceeded, GatewayOAuthFailed} {
		fixtureSignal(t, c, "notify", map[string]any{"method": "account/gatewayOAuth/changed", "params": map[string]any{"providerId": "private-provider-marker", "status": status, "authUrl": "javascript:private-auth-marker", "error": "private-error-marker"}})
		event := nextKind(t, c, MetadataEvent)
		if event.Metadata != GatewayOAuthStatusDiscarded || event.GatewayOAuthStatus != status || c.problem != nil || c.execution.active != accepted.TurnID || !reflect.DeepEqual(c.execution.settings, settings) || len(requestsOf(t, capture, "turn/start")) != beforeStarts {
			t.Fatal("gateway status changed original execution or resent input")
		}
	}
	if strings.Contains(logs.String(), "private-") || !strings.Contains(logs.String(), "observed-only") || !strings.Contains(logs.String(), "account-gateway") {
		t.Fatal("gateway diagnostic reflected private fields or lost closed policy")
	}
	fixtureSignal(t, c, "finish", map[string]any{"status": TurnCompleted})
	if terminal := nextKind(t, c, TurnCompletedEvent); terminal.TurnID != accepted.TurnID || terminal.Turn.Status != TurnCompleted {
		t.Fatal("passive status supplied or prevented original completion")
	}
}
