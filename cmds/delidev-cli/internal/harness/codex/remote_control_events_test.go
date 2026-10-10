// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRemoteControlPolicyPreservesOriginalOwnership(t *testing.T) {
	c, turn := observationClient()
	before := c.execution.settings
	paused := c.execution.paused
	for _, status := range []RemoteControlStatus{RemoteControlStatusDisabled, RemoteControlStatusConnecting, RemoteControlStatusConnected, RemoteControlStatusErrored} {
		for _, environment := range []any{nil, "private-env"} {
			params := map[string]any{"status": status, "serverName": "private-server", "installationId": "private-install", "environmentId": environment}
			for i := 0; i < 2; i++ {
				event, err := observeFixture(c, "remoteControl/status/changed", params)
				if status == RemoteControlStatusDisabled && environment == nil {
					if err != nil || event.Kind != MetadataEvent || event.Metadata != RemoteControlDisabled || event.RemoteControl == nil || event.RemoteControl.Policy != RemoteControlPassive {
						t.Fatal(event, err)
					}
					raw, _ := json.Marshal(event)
					if strings.Contains(string(raw), "private-") {
						t.Fatal("identity published")
					}
				} else {
					var violation *RemoteControlPolicyViolation
					if !errors.As(err, &violation) || !violation.Observation.Valid() || violation.Observation.Status != status || event.Kind != "" {
						t.Fatal("typed policy lost", event, err)
					}
				}
			}
		}
	}
	if !reflect.DeepEqual(before, c.execution.settings) || c.execution.active != turn || c.execution.paused != paused {
		t.Fatal("original authority changed")
	}
	event, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.ServerRequest, Method: "remoteControl/status/changed", Params: []byte(`{"status":"disabled","serverName":"s","installationId":"i"}`)})
	if err == nil && event.Kind != NativeExtensionEvent {
		t.Fatal("server request gained notification admission", event)
	}
}

func TestRemoteControlRefusalRetainsAcknowledgedInputAndJoinedCleanup(t *testing.T) {
	for _, status := range []string{"connecting", "connected", "errored", "disabled"} {
		t.Run(status, func(t *testing.T) {
			c, capture, _, _ := boundTurnFixture(t, "normal")
			accepted, err := c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
			if err != nil {
				t.Fatal(err)
			}
			nextKind(t, c, MessageCompletedEvent)
			var logs bytes.Buffer
			c.logger = slog.New(slog.NewJSONHandler(&logs, nil))
			environment := any(nil)
			if status == "disabled" {
				environment = "private-env"
			}
			fixtureSignal(t, c, "notify", map[string]any{"method": "remoteControl/status/changed", "params": map[string]any{"status": status, "serverName": "private-server", "installationId": "private-install", "environmentId": environment}})
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			for {
				_, err = c.NextEvent(ctx)
				if err != nil {
					break
				}
			}
			if domain.SafeError(err).Code != domain.RecoveryRequired || c.problem == nil || !c.execution.paused || c.execution.active != accepted.TurnID {
				t.Fatal("policy refusal lost acknowledged original", err)
			}
			if !strings.Contains(logs.String(), "Codex remote-control policy refused") || !strings.Contains(logs.String(), status) || strings.Contains(logs.String(), "private-") {
				t.Fatal("closed redacted diagnostic missing", logs.String())
			}
			if len(requestsOf(t, capture, "turn/start")) != 1 || len(requestsOf(t, capture, "turn/steer")) != 0 || len(requestsOf(t, capture, "remoteControl/enable")) != 0 || len(requestsOf(t, capture, "remoteControl/disable")) != 0 {
				t.Fatal("refusal triggered native work")
			}
			if err := c.Close(); err != nil {
				t.Fatal("original cleanup not independently joined", err)
			}
		})
	}
}
func TestRemoteControlDisabledReplayPreservesOrdinaryCompletion(t *testing.T) {
	c, capture, _, _ := boundTurnFixture(t, "normal")
	accepted, err := c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
	if err != nil {
		t.Fatal(err)
	}
	nextKind(t, c, MessageCompletedEvent)
	for i := 0; i < 3; i++ {
		fixtureSignal(t, c, "notify", map[string]any{"method": "remoteControl/status/changed", "params": map[string]any{"status": "disabled", "serverName": "private-server", "installationId": "private-install", "environmentId": nil}})
		event := nextKind(t, c, MetadataEvent)
		if event.Metadata != RemoteControlDisabled || c.problem != nil || c.execution.active != accepted.TurnID {
			t.Fatal("passive replay replaced original", event)
		}
	}
	fixtureSignal(t, c, "finish", map[string]any{"status": TurnCompleted})
	terminal := nextKind(t, c, TurnCompletedEvent)
	nextKind(t, c, ThreadStatusEvent)
	if terminal.TurnID != accepted.TurnID || c.execution.active != "" || c.problem != nil || len(requestsOf(t, capture, "turn/start")) != 1 {
		t.Fatal("original completion changed", terminal)
	}
}
