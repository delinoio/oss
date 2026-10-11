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

func remoteControlFixture(status remoteControlStatus) map[string]any {
	return map[string]any{"status": status, "serverName": "private-server", "installationId": "private-installation", "environmentId": nil}
}

func TestRemoteControlAllStatesPreserveOriginalAuthority(t *testing.T) {
	for _, status := range []remoteControlStatus{remoteControlDisabled, remoteControlConnecting, remoteControlConnected, remoteControlErrored} {
		for _, environment := range []string{"omitted", "null", "empty", "present"} {
			t.Run(string(status)+"/"+environment, func(t *testing.T) {
				c, turn := observationClient()
				settings := c.execution.settings
				params := remoteControlFixture(status)
				switch environment {
				case "omitted":
					delete(params, "environmentId")
				case "empty":
					params["environmentId"] = ""
				case "present":
					params["environmentId"] = "private-environment"
				}
				event, err := observeFixture(c, "remoteControl/status/changed", params)
				refused := status != remoteControlDisabled || environment == "empty" || environment == "present"
				if refused {
					var violation *remoteControlPolicyViolation
					if !errors.As(err, &violation) || violation.status != status || c.remoteControlRefusal != violation || c.problem == nil || c.problem.Code != domain.RecoveryRequired || !c.execution.paused || event.Kind != "" {
						t.Fatal("remote state did not retain a typed policy fence", event, err)
					}
					classification := remoteControlActiveRefused
					if status == remoteControlDisabled {
						classification = remoteControlEnvironmentRefused
					}
					if violation.classification != classification {
						t.Fatal("wrong policy classification")
					}
				} else if err != nil || event.Metadata != RemoteControlDisabled || c.problem != nil || c.execution.paused {
					t.Fatal("passive disabled observation gained authority", event, err)
				}
				raw, _ := json.Marshal(event)
				if strings.Contains(string(raw), "private-") || err != nil && strings.Contains(err.Error(), "private-") {
					t.Fatal("remote identity escaped")
				}
				if !reflect.DeepEqual(settings, c.execution.settings) || c.execution.active != turn || c.execution.turns[turn].Turn.Status != TurnRunning || len(c.execution.inputs) != 0 || len(c.execution.pending) != 0 {
					t.Fatal("remote state changed input, model, settings or outcome")
				}
			})
		}
	}
}

func TestRemoteControlRequiredStringsCanBeEmpty(t *testing.T) {
	c, _ := observationClient()
	event, err := observeFixture(c, "remoteControl/status/changed", map[string]any{"status": "disabled", "installationId": "", "serverName": ""})
	if err != nil || event.Metadata != RemoteControlDisabled || c.problem != nil {
		t.Fatal("empty schema strings were rejected", event, err)
	}
}

func TestRemoteControlReplayTerminalAndDisabledCannotClearFirstProblem(t *testing.T) {
	for _, prior := range []bool{false, true} {
		c, turn := observationClient()
		if prior {
			c.problem = turnUncertain()
		}
		firstProblem := c.problem
		params := remoteControlFixture(remoteControlConnected)
		_, err := observeFixture(c, "remoteControl/status/changed", params)
		if err == nil {
			t.Fatal("remote state was admitted")
		}
		if prior && c.problem != firstProblem {
			t.Fatal("first recovery cause was replaced")
		}
		firstProblem, firstRefusal := c.problem, c.remoteControlRefusal
		for _, status := range []remoteControlStatus{remoteControlConnected, remoteControlErrored, remoteControlDisabled} {
			params["status"] = status
			_, err := observeFixture(c, "remoteControl/status/changed", params)
			if status != remoteControlDisabled && err == nil {
				t.Fatal("refusal replay regained authority")
			}
			if c.problem != firstProblem || c.remoteControlRefusal != firstRefusal || !c.execution.paused {
				t.Fatal("later status cleared or replaced recovery")
			}
		}
		event, err := observeFixture(c, "turn/completed", map[string]any{"threadId": c.thread, "turn": fixtureTurn(turn, TurnCompleted)})
		if err != nil || event.Kind != TurnCompletedEvent || !event.Correlated || c.execution.turns[turn].Turn.Status != TurnCompleted {
			t.Fatal("original terminal evidence lost", event, err)
		}
		if c.problem != firstProblem || !c.execution.paused || c.remoteControlRefusal != firstRefusal {
			t.Fatal("terminal cleared policy fence")
		}
	}
}

func TestRemoteControlMalformedShapesAndRequestStaySeparate(t *testing.T) {
	for _, change := range []string{"status-missing", "status-null", "status-unknown", "serverName-missing", "serverName-null", "installationId-missing", "installationId-null", "unknown", "alias", "oversized-server", "oversized-installation", "oversized-environment", "collection", "nul", "envelope"} {
		t.Run(change, func(t *testing.T) {
			c, _ := observationClient()
			params := remoteControlFixture(remoteControlDisabled)
			switch change {
			case "status-missing", "serverName-missing", "installationId-missing":
				delete(params, strings.TrimSuffix(change, "-missing"))
			case "status-null", "serverName-null", "installationId-null":
				params[strings.TrimSuffix(change, "-null")] = nil
			case "status-unknown":
				params["status"] = "unknown"
			case "unknown":
				params["unknown"] = true
			case "alias":
				delete(params, "status")
				params["Status"] = "disabled"
			case "oversized-server":
				params["serverName"] = strings.Repeat("x", 1025)
			case "oversized-installation":
				params["installationId"] = strings.Repeat("x", 1025)
			case "oversized-environment":
				params["environmentId"] = strings.Repeat("x", 1025)
			case "collection":
				params["environmentId"] = []string{"private-environment"}
			case "nul":
				params["serverName"] = "private-\x00"
			case "envelope":
				params["serverName"] = strings.Repeat("x", 32*1024)
			}
			_, err := observeFixture(c, "remoteControl/status/changed", params)
			var refusal *remoteControlPolicyViolation
			if err == nil || errors.As(err, &refusal) || c.problem != nil || c.execution.paused || c.remoteControlRefusal != nil {
				t.Fatal("malformed observation became valid policy state", err)
			}
		})
	}
	for _, raw := range []string{`null`, `[]`, `{"status":"disabled","status":"connected","serverName":"x","installationId":"x"}`} {
		if _, _, err := decodeRemoteControl(json.RawMessage(raw)); err == nil {
			t.Fatal("invalid object or duplicate keys accepted")
		}
	}
	c, _ := observationClient()
	raw, _ := json.Marshal(remoteControlFixture(remoteControlConnected))
	event, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.ServerRequest, Method: "remoteControl/status/changed", Params: raw})
	if err != nil || event.Kind != NativeExtensionEvent || event.ExtensionStage != validationRemoteControl || c.remoteControlRefusal != nil || c.problem != nil || len(c.execution.interactions.arrivals) != 0 {
		t.Fatal("server request acquired notification or reply authority", event, err)
	}
}

func TestRemoteControlReaderFencesAcceptedInputAndJoinsOriginalClose(t *testing.T) {
	c, capture, _, _ := boundTurnFixture(t, "ready")
	result, err := c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.PlanMode))
	if err != nil {
		t.Fatal(err)
	}
	nextKind(t, c, MessageCompletedEvent)
	var logs bytes.Buffer
	c.logger = slog.New(slog.NewJSONHandler(&logs, nil))
	fixtureSignal(t, c, "notify", map[string]any{"method": "remoteControl/status/changed", "params": remoteControlFixture(remoteControlConnected)})
	_, err = c.NextEvent(context.Background())
	assertCode(t, err, domain.RecoveryRequired)
	problem := c.problem
	if c.remoteControlRefusal == nil || !c.execution.paused {
		t.Fatal("reader lost the policy cause")
	}
	_, err = c.Steer(context.Background(), domain.NewID(), domain.NewID(), result.TurnID, input(domain.PlanMode))
	assertCode(t, err, domain.RecoveryRequired)
	_, err = c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.PlanMode))
	assertCode(t, err, domain.RecoveryRequired)
	fixtureSignal(t, c, "notify", map[string]any{"method": "remoteControl/status/changed", "params": remoteControlFixture(remoteControlDisabled)})
	nextKind(t, c, MetadataEvent)
	fixtureSignal(t, c, "finish", map[string]any{"status": TurnCompleted})
	completed := nextKind(t, c, TurnCompletedEvent)
	if completed.Turn.Status != TurnCompleted || c.problem != problem || !c.execution.paused {
		t.Fatal("terminal replaced original recovery")
	}
	// The fixture emits a status supplement after the terminal event.
	nextKind(t, c, ThreadStatusEvent)
	// A malformed later event must not replace the first policy cause either.
	fixtureSignal(t, c, "notify", map[string]any{"method": "remoteControl/status/changed", "params": map[string]any{"status": "unknown"}})
	_, err = c.NextEvent(context.Background())
	assertCode(t, err, domain.RecoveryRequired)
	if c.problem != problem {
		t.Fatal("malformed event replaced the policy cause")
	}
	for _, method := range []string{"turn/steer", "remoteControl/enable", "remoteControl/disable", "remoteControl/pair", "remoteControl/reconnect"} {
		if len(requestsOf(t, capture, method)) != 0 {
			t.Fatal("remote observation sent another native operation", method)
		}
	}
	if len(requestsOf(t, capture, "turn/start")) != 1 {
		t.Fatal("input was replayed")
	}
	if strings.Contains(logs.String(), "private-") || !strings.Contains(logs.String(), "remote-state-refused") || !strings.Contains(logs.String(), "remote-control-policy") {
		t.Fatal("logs leaked identities or lost closed classifications", logs.String())
	}
	if err := c.Close(); err != nil {
		t.Fatal("original process close did not join independently", err)
	}
}
