// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

func remoteControlFixture(status nativeRemoteControlStatus, environment any) map[string]any {
	return map[string]any{"status": status, "serverName": "private-server-marker", "installationId": "private-installation-marker", "environmentId": environment}
}

func TestRemoteControlValidStatesHaveClosedPolicyClassification(t *testing.T) {
	for _, status := range []nativeRemoteControlStatus{nativeRemoteDisabled, nativeRemoteConnecting, nativeRemoteConnected, nativeRemoteErrored} {
		for _, environment := range []any{nil, "private-environment-marker"} {
			c, turn := observationClient()
			settings, paused := c.execution.settings, c.execution.paused
			var logs bytes.Buffer
			c.logger = slog.New(slog.NewJSONHandler(&logs, nil))
			event, err := observeFixture(c, "remoteControl/status/changed", remoteControlFixture(status, environment))
			if status == nativeRemoteDisabled && environment == nil {
				if err != nil || event.Kind != MetadataEvent || event.Metadata != RemoteControlDisabled || event.Native != nil {
					t.Fatal("disabled-only metadata changed", err)
				}
			} else {
				var policy remoteControlPolicyViolation
				if !errors.As(err, &policy) || policy.Status != status || remoteControlFailureStage(err, validationRemoteControl) != validationRemoteControlPolicy || event.Kind != "" {
					t.Fatal("valid remote state lost its closed policy refusal", err)
				}
			}
			raw, _ := json.Marshal(event)
			if strings.Contains(string(raw), "private-") || strings.Contains(logs.String(), "private-") || err != nil && strings.Contains(err.Error(), "private-") {
				t.Fatal("remote descriptor escaped")
			}
			if c.execution.active != turn || c.execution.paused != paused || !reflect.DeepEqual(c.execution.settings, settings) {
				t.Fatal("observation changed original execution settings or turn")
			}
		}
	}
}

func TestRemoteControlMalformedMetadataRetainsProtocolRejection(t *testing.T) {
	for _, raw := range []string{
		`null`, `[]`, `{}`, `{"status":"disabled","serverName":null,"installationId":"id"}`,
		`{"status":"disabled","serverName":"name"}`, `{"status":null,"serverName":"name","installationId":"id"}`,
		`{"status":"unknown","serverName":"name","installationId":"id"}`,
		`{"status":"disabled","serverName":"name","installationId":"id","environmentId":[]}`,
		`{"status":"disabled","status":"disabled","serverName":"name","installationId":"id"}`,
		`{"status":"disabled","serverName":"name","installationId":"id","unknown":true}`,
		`{"status":"disabled","serverName":"` + strings.Repeat("p", 1025) + `","installationId":"id"}`,
		`{"status":"disabled","serverName":"name","installationId":"` + strings.Repeat("p", 1025) + `"}`,
		`{"status":"disabled","serverName":"name","installationId":"id","environmentId":"` + strings.Repeat("p", 1025) + `"}`,
	} {
		c, _ := observationClient()
		_, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.Notification, Method: "remoteControl/status/changed", Params: json.RawMessage(raw)})
		var policy remoteControlPolicyViolation
		if err == nil || errors.As(err, &policy) || remoteControlFailureStage(err, validationRemoteControl) != validationRemoteControl {
			t.Fatal("malformed native state became valid policy telemetry", err)
		}
	}
	c, _ := observationClient()
	raw, _ := json.Marshal(remoteControlFixture(nativeRemoteDisabled, nil))
	event, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.ServerRequest, Method: "remoteControl/status/changed", ID: json.RawMessage(`1`), Token: domain.NewID(), Params: raw})
	if err != nil || event.Kind != NativeExtensionEvent {
		t.Fatal("server request gained remote observation/reply authority", err)
	}
}

func TestRemoteControlPolicyFencesAcknowledgedInputAndJoinsOriginalCleanup(t *testing.T) {
	for _, status := range []nativeRemoteControlStatus{nativeRemoteConnecting, nativeRemoteConnected, nativeRemoteErrored, nativeRemoteDisabled} {
		t.Run(string(status), func(t *testing.T) {
			client, capture, _, _ := boundTurnFixture(t, "ready")
			turn, err := client.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
			if err != nil {
				t.Fatal(err)
			}
			nextKind(t, client, TurnStartedEvent)
			var logs bytes.Buffer
			client.logger = slog.New(slog.NewJSONHandler(&logs, nil))
			params, _ := json.Marshal(remoteControlFixture(status, "private-environment-marker"))
			client.pendingEvent = &nativewire.Event{Kind: nativewire.Notification, Method: "remoteControl/status/changed", Params: params}
			_, err = client.NextEvent(context.Background())
			assertCode(t, err, domain.RecoveryRequired)
			if !client.execution.paused || client.execution.turns[turn.TurnID].Turn.Status != TurnRunning || !strings.Contains(logs.String(), string(validationRemoteControlPolicy)) || strings.Contains(logs.String(), "private-") {
				t.Fatal("policy failure replaced original outcome or leaked native identity")
			}
			_, err = client.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
			assertCode(t, err, domain.RecoveryRequired)
			if len(requestsOf(t, capture, "turn/start")) != 1 {
				t.Fatal("policy observation replayed acknowledged input")
			}
			if err := client.Close(); err != nil {
				t.Fatal("original process cleanup failed", err)
			}
		})
	}
}

func TestRemoteControlDisabledReplayCannotReplaceTerminalEvidence(t *testing.T) {
	client, capture, _, _ := boundTurnFixture(t, "ready")
	if _, err := client.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode)); err != nil {
		t.Fatal(err)
	}
	nextKind(t, client, TurnStartedEvent)
	for i := 0; i < 2; i++ {
		raw, _ := json.Marshal(remoteControlFixture(nativeRemoteDisabled, nil))
		client.pendingEvent = &nativewire.Event{Kind: nativewire.Notification, Method: "remoteControl/status/changed", Params: raw}
		event, err := client.NextEvent(context.Background())
		if err != nil || event.Metadata != RemoteControlDisabled || client.execution.paused {
			t.Fatal("passive replay fenced ordinary input", err)
		}
	}
	fixtureSignal(t, client, "finish", map[string]any{"status": TurnCompleted})
	if event := nextKind(t, client, TurnCompletedEvent); event.Turn.Status != TurnCompleted || len(requestsOf(t, capture, "turn/start")) != 1 {
		t.Fatal("remote metadata replaced original terminal evidence")
	}
}
