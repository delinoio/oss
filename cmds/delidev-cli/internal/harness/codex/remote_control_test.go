// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"
	"errors"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"strings"
	"testing"
)

func TestRemoteControlDecoderStates(t *testing.T) {
	for _, status := range []RemoteControlStatus{RemoteControlStatusDisabled, RemoteControlStatusConnecting, RemoteControlStatusConnected, RemoteControlStatusErrored} {
		for _, environment := range []any{nil, "private-env", ""} {
			raw, _ := json.Marshal(map[string]any{"status": status, "serverName": "private-server", "installationId": "private-install", "environmentId": environment})
			v, err := decodeRemoteControlStatus(raw)
			if err != nil || !v.Valid() || v.Status != status || v.EnvironmentPresent != (environment != nil) {
				t.Fatal(v, err)
			}
			expected := RemoteControlStateForbidden
			if environment != nil {
				expected = RemoteControlEnvironmentForbidden
			} else if status == RemoteControlStatusDisabled {
				expected = RemoteControlPassive
			}
			if v.Policy != expected {
				t.Fatal(v)
			}
			safe, _ := json.Marshal(v)
			if strings.Contains(string(safe), "private-") {
				t.Fatal("identity escaped")
			}
		}
	}
	if v, err := decodeRemoteControlStatus([]byte(`{"status":"disabled","serverName":"","installationId":""}`)); err != nil || v.Policy != RemoteControlPassive {
		t.Fatal(v, err)
	}
	violation := &RemoteControlPolicyViolation{Observation: RemoteControlObservation{Status: RemoteControlStatusConnected, Policy: RemoteControlStateForbidden}}
	var known *domain.Error
	if !errors.As(violation, &known) || known.Code != domain.Unsupported {
		t.Fatal("typed policy lost")
	}
}
func TestRemoteControlDecoderMalformed(t *testing.T) {
	for _, raw := range []string{`null`, `{}`, `{"status":null,"serverName":"s","installationId":"i"}`, `{"status":"disabled","serverName":null,"installationId":"i"}`, `{"status":"disabled","serverName":"s","installationId":null}`, `{"status":"unknown","serverName":"s","installationId":"i"}`, `{"status":"disabled","serverName":"s","installationId":"i","environmentId":3}`, `{"status":"disabled","serverName":"s","installationId":"i","unknown":true}`, `{"status":"disabled","status":"connected","serverName":"s","installationId":"i"}`, `{"status":"disabled","serverName":"\u0000private-marker","installationId":"i"}`} {
		_, err := decodeRemoteControlStatus([]byte(raw))
		if err == nil {
			t.Fatal("malformed accepted", raw)
		}
		if strings.Contains(err.Error(), "private-marker") {
			t.Fatal("payload reflected")
		}
	}
	for _, field := range []string{"serverName", "installationId", "environmentId"} {
		payload := map[string]any{"status": "disabled", "serverName": "s", "installationId": "i", "environmentId": nil}
		payload[field] = strings.Repeat("x", 4097)
		raw, _ := json.Marshal(payload)
		if _, err := decodeRemoteControlStatus(raw); err == nil {
			t.Fatal("unbounded accepted", field)
		}
	}
	if _, err := decodeRemoteControlStatus([]byte(`{"status":"disabled","serverName":"` + strings.Repeat("x", 1<<20) + `","installationId":"i"}`)); err == nil {
		t.Fatal("unbounded frame accepted")
	}
}
