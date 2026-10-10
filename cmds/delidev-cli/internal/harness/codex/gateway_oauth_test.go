// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

func TestGatewayOAuthDecoderPreservesPrivateNullableFields(t *testing.T) {
	for _, status := range []GatewayOAuthStatus{GatewayOAuthNotReady, GatewayOAuthStarted, GatewayOAuthSucceeded, GatewayOAuthFailed} {
		for _, optional := range []string{"", `,"authUrl":null,"error":null`, `,"authUrl":"javascript:private-marker?callback=private-marker","error":"private-marker"`} {
			raw := []byte(`{"providerId":"foreign-private-provider","status":"` + string(status) + `"` + optional + `}`)
			value, err := decodeGatewayOAuthNotification(raw)
			if err != nil || value.status != status || value.providerID != "foreign-private-provider" {
				t.Fatal("complete official gateway status was rejected", err)
			}
			if optional == "" {
				if value.authURL.present || value.errorText.present {
					t.Fatal("omitted private metadata became explicit null")
				}
			} else if !value.authURL.present || !value.errorText.present {
				t.Fatal("private metadata presence was discarded during decoding")
			}
			if strings.Contains(optional, "null") && (value.authURL.value != nil || value.errorText.value != nil) {
				t.Fatal("explicit null private metadata became a string")
			}
			if strings.Contains(optional, "javascript") && (value.authURL.value == nil || *value.authURL.value != "javascript:private-marker?callback=private-marker" || value.errorText.value == nil || *value.errorText.value != "private-marker") {
				t.Fatal("inert private strings were parsed or rewritten")
			}
			serialized, err := json.Marshal(value)
			if err != nil || strings.Contains(string(serialized), "private") || strings.Contains(string(serialized), "javascript") {
				t.Fatal("private decoder value became serializable product data")
			}
		}
	}
}

func TestGatewayOAuthDecoderRejectsMalformedPrivateMetadata(t *testing.T) {
	invalid := []string{
		`null`, `{}`, `[]`,
		`{"status":"started"}`, `{"providerId":"private-provider"}`,
		`{"providerId":null,"status":"started"}`, `{"providerId":"private-provider","status":null}`,
		`{"providerId":"","status":"started"}`, `{"providerId":"private-provider","status":"unknown-private-marker"}`,
		`{"providerId":7,"status":"started"}`, `{"providerId":"private-provider","status":7}`,
		`{"providerId":"private-provider","status":"started","authUrl":{}}`,
		`{"providerId":"private-provider","status":"started","error":true}`,
		`{"providerId":"private-provider","status":"started","unknown":null}`,
		`{"providerId":"private-provider","providerId":"other","status":"started"}`,
		`{"providerId":"private-provider","status":"started","status":"failed"}`,
		`{"providerId":"private-provider","status":"started","authUrl":null,"authUrl":null}`,
		`{"providerId":"private-provider","status":"started","error":null,"error":null}`,
		`{"providerId":"private-provider","status":"started","authUrl":"private\u0000marker"}`,
		`{"providerId":"private-provider","status":"started","error":"private\u0000marker"}`,
	}
	longProvider, _ := json.Marshal(map[string]any{"providerId": strings.Repeat("x", 1025), "status": "started"})
	longURL, _ := json.Marshal(map[string]any{"providerId": "provider", "status": "started", "authUrl": strings.Repeat("x", nativewire.MaxFrame+1)})
	longError, _ := json.Marshal(map[string]any{"providerId": "provider", "status": "failed", "error": strings.Repeat("x", nativewire.MaxFrame+1)})
	invalid = append(invalid, string(longProvider), string(longURL), string(longError))
	for _, raw := range invalid {
		_, err := decodeGatewayOAuthNotification([]byte(raw))
		if err == nil || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), raw) {
			t.Fatal("malformed gateway metadata accepted or reflected")
		}
	}
}
