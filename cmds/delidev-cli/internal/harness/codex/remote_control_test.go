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

func remoteFixture(status remoteControlStatus, environment any) map[string]any {
	return map[string]any{"status": status, "serverName": "private-remote-server", "installationId": "private-remote-installation", "environmentId": environment}
}
func TestRemoteControlRecognizesClosedPolicyStates(t *testing.T) {
	for _, status := range []remoteControlStatus{remoteDisabled, remoteConnecting, remoteConnected, remoteErrored} {
		for _, environment := range []any{nil, "", "private-remote-environment"} {
			c, turn := observationClient()
			settings := c.execution.settings
			event, err := observeFixture(c, "remoteControl/status/changed", remoteFixture(status, environment))
			if status == remoteDisabled && environment == nil {
				if err != nil || event.Metadata != RemoteControlDisabled || event.Native != nil {
					t.Fatal("disabled passive state lost")
				}
			} else {
				var policy *remoteControlPolicyViolation
				if !errors.As(err, &policy) || policy.status != status || event.Kind == NativeExtensionEvent {
					t.Fatal("valid status became unknown protocol instead of policy violation")
				}
			}
			if c.execution.active != turn || !reflect.DeepEqual(c.execution.settings, settings) {
				t.Fatal("observation changed original execution")
			}
			encoded, _ := json.Marshal(event)
			if strings.Contains(string(encoded), "private-remote") {
				t.Fatal("private identity exposed")
			}
		}
	}
}
func TestRemoteControlRejectsMalformedPrivateMetadata(t *testing.T) {
	prefix := `{"status":"disabled","serverName":"private-remote-server","installationId":"private-remote-installation"`
	raws := []string{"", "null", "[]", "{}", `{"status":null,"serverName":"s","installationId":"i"}`, `{"status":"unknown","serverName":"s","installationId":"i"}`, `{"status":"disabled","installationId":"i"}`, `{"status":"disabled","serverName":"s"}`, `{"status":"disabled","serverName":null,"installationId":"i"}`, `{"status":"disabled","serverName":"s","installationId":null}`, prefix + `,"unknown":"private-remote"}`, prefix + `,"status":"connected"}`, prefix + `,"serverName":"s"}`, prefix + `,"installationId":"i"}`, prefix + `,"environmentId":null,"environmentId":null}`, prefix + `,"environmentId":1}`, prefix + `,"environmentId":[]}`, prefix + `,"environmentId":"private\u0000remote"}`}
	for _, field := range []string{"serverName", "installationId", "environmentId"} {
		value := remoteFixture(remoteDisabled, nil)
		value[field] = strings.Repeat("x", 1025)
		raw, _ := json.Marshal(value)
		raws = append(raws, string(raw))
	}
	raws = append(raws, prefix+`,"padding":"`+strings.Repeat("x", nativewire.MaxFrame)+`"}`)
	for i, raw := range raws {
		c, turn := observationClient()
		_, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.Notification, Method: "remoteControl/status/changed", Params: json.RawMessage(raw)})
		var policy *remoteControlPolicyViolation
		if err == nil || errors.As(err, &policy) || strings.Contains(err.Error(), "private-remote") || c.execution.active != turn {
			t.Fatalf("malformed metadata %d accepted or misclassified", i)
		}
	}
	c, _ := observationClient()
	raw, _ := json.Marshal(remoteFixture(remoteDisabled, nil))
	event, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.ServerRequest, Method: "remoteControl/status/changed", Params: raw})
	if err == nil && event.Kind != NativeExtensionEvent {
		t.Fatal("server request gained notification authority")
	}
	if validationStage("remoteControl/status/changed") != validationRemoteControl {
		t.Fatal("redacted validation stage missing")
	}
}
func TestRemoteControlPolicyFencesOriginalAcceptedInputAndJoinsCleanup(t *testing.T) {
	for _, status := range []remoteControlStatus{remoteConnecting, remoteConnected, remoteErrored, remoteDisabled} {
		t.Run(string(status), func(t *testing.T) {
			c, capture, _, _ := boundTurnFixture(t, "normal")
			settings := c.execution.settings
			accepted, err := c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
			if err != nil {
				t.Fatal(err)
			}
			nextKind(t, c, MessageCompletedEvent)
			var logs bytes.Buffer
			c.logger = slog.New(slog.NewJSONHandler(&logs, nil))
			environment := any(nil)
			if status == remoteDisabled {
				environment = "private-remote-environment"
			}
			for i := 0; i < 2; i++ {
				fixtureSignal(t, c, "notify", map[string]any{"method": "remoteControl/status/changed", "params": remoteFixture(status, environment)})
				_, err = c.NextEvent(context.Background())
				assertCode(t, err, domain.RecoveryRequired)
				if c.remoteControlPolicy == nil || c.remoteControlPolicy.status != status || c.problem == nil || !c.execution.paused || c.execution.active != accepted.TurnID || !reflect.DeepEqual(c.execution.settings, settings) {
					t.Fatal("original execution was not fenced")
				}
			}
			problem, policy := c.problem, c.remoteControlPolicy
			fixtureSignal(t, c, "notify", map[string]any{"method": "remoteControl/status/changed", "params": remoteFixture(remoteDisabled, nil)})
			nextKind(t, c, MetadataEvent)
			if c.problem != problem || c.remoteControlPolicy != policy || !c.execution.paused {
				t.Fatal("disabled replay cleared original uncertainty")
			}
			fixtureSignal(t, c, "finish", map[string]any{"status": TurnCompleted})
			terminal := nextKind(t, c, TurnCompletedEvent)
			nextKind(t, c, ThreadStatusEvent)
			if terminal.TurnID != accepted.TurnID || c.problem != problem || !c.execution.paused {
				t.Fatal("terminal observation replaced original recovery ownership")
			}
			_, err = c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
			assertCode(t, err, domain.RecoveryRequired)
			_, err = c.Steer(context.Background(), domain.NewID(), domain.NewID(), accepted.TurnID, input(domain.ExecuteMode))
			if err == nil {
				t.Fatal("policy failure granted input")
			}
			if !strings.Contains(logs.String(), "remote-control-policy") || strings.Contains(logs.String(), "private-remote") {
				t.Fatal("policy diagnostic missing or identity exposed")
			}
			if len(requestsOf(t, capture, "turn/start")) != 1 || len(requestsOf(t, capture, "turn/steer")) != 0 || len(requestsOf(t, capture, "remoteControl/enable")) != 0 || len(requestsOf(t, capture, "remoteControl/disable")) != 0 || len(requestsOf(t, capture, "remoteControl/pair")) != 0 {
				t.Fatal("policy observation sent native operation")
			}
			if err := c.Close(); err != nil {
				t.Fatal("original fixture cleanup did not join", err)
			}
		})
	}
}
func TestRemoteControlDisabledReplayKeepsOriginalTerminalAndInput(t *testing.T) {
	c, capture, _, _ := boundTurnFixture(t, "normal")
	accepted, err := c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
	if err != nil {
		t.Fatal(err)
	}
	nextKind(t, c, MessageCompletedEvent)
	for i := 0; i < 3; i++ {
		fixtureSignal(t, c, "notify", map[string]any{"method": "remoteControl/status/changed", "params": remoteFixture(remoteDisabled, nil)})
		event := nextKind(t, c, MetadataEvent)
		if event.Metadata != RemoteControlDisabled || c.problem != nil || c.execution.paused {
			t.Fatal("disabled metadata changed execution")
		}
	}
	fixtureSignal(t, c, "finish", map[string]any{"status": TurnCompleted})
	terminal := nextKind(t, c, TurnCompletedEvent)
	nextKind(t, c, ThreadStatusEvent)
	if terminal.TurnID != accepted.TurnID || c.execution.active != "" || len(requestsOf(t, capture, "turn/start")) != 1 {
		t.Fatal("passive replay changed original terminal")
	}
}

func TestRemoteControlPolicyRetainsExistingUncertainty(t *testing.T) {
	c, _, _, _ := boundTurnFixture(t, "normal")
	accepted, err := c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
	if err != nil {
		t.Fatal(err)
	}
	nextKind(t, c, MessageCompletedEvent)
	original := turnUncertain()
	c.problem = original
	c.execution.paused = true
	fixtureSignal(t, c, "notify", map[string]any{"method": "remoteControl/status/changed", "params": remoteFixture(remoteConnected, nil)})
	_, err = c.NextEvent(context.Background())
	assertCode(t, err, domain.RecoveryRequired)
	if c.problem != original || c.remoteControlPolicy == nil || c.execution.active != accepted.TurnID || !c.execution.paused {
		t.Fatal("policy observation replaced earlier uncertainty")
	}
}
