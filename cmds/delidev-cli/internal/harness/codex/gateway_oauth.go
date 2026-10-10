// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

type gatewayOAuthStatus string

const (
	gatewayNotReady  gatewayOAuthStatus = "notReady"
	gatewayStarted   gatewayOAuthStatus = "started"
	gatewaySucceeded gatewayOAuthStatus = "succeeded"
	gatewayFailed    gatewayOAuthStatus = "failed"
)

type gatewayStringPresence uint8

const (
	gatewayStringOmitted gatewayStringPresence = iota
	gatewayStringNull
	gatewayStringValue
)

// All fields stay private. In particular, an original URL is inert text, never
// parsed into provider authority or passed to a browser/callback owner.
type gatewayPrivateString struct {
	presence gatewayStringPresence
	value    string
}
type gatewayOAuthNotification struct {
	providerID string
	status     gatewayOAuthStatus
	authURL    gatewayPrivateString
	diagnostic gatewayPrivateString
}

func decodeGatewayPrivateString(raw json.RawMessage) (gatewayPrivateString, error) {
	if len(raw) == 0 {
		return gatewayPrivateString{presence: gatewayStringOmitted}, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return gatewayPrivateString{presence: gatewayStringNull}, nil
	}
	var value string
	if domain.Decode(raw, &value) != nil || domain.Text(value, "native gateway metadata", nativewire.MaxFrame, false) != nil {
		return gatewayPrivateString{}, incompatible()
	}
	return gatewayPrivateString{presence: gatewayStringValue, value: value}, nil
}
func decodeGatewayOAuthNotification(raw json.RawMessage) (gatewayOAuthNotification, error) {
	var wire struct {
		ProviderID string             `json:"providerId"`
		Status     gatewayOAuthStatus `json:"status"`
		AuthURL    json.RawMessage    `json:"authUrl"`
		Error      json.RawMessage    `json:"error"`
	}
	if domain.DecodeBounded(raw, &wire, nativewire.MaxFrame) != nil || domain.Text(wire.ProviderID, "native gateway provider", 1024, true) != nil {
		return gatewayOAuthNotification{}, incompatible()
	}
	switch wire.Status {
	case gatewayNotReady, gatewayStarted, gatewaySucceeded, gatewayFailed:
	default:
		return gatewayOAuthNotification{}, incompatible()
	}
	authURL, err := decodeGatewayPrivateString(wire.AuthURL)
	if err != nil {
		return gatewayOAuthNotification{}, err
	}
	diagnostic, err := decodeGatewayPrivateString(wire.Error)
	if err != nil {
		return gatewayOAuthNotification{}, err
	}
	return gatewayOAuthNotification{providerID: wire.ProviderID, status: wire.Status, authURL: authURL, diagnostic: diagnostic}, nil
}
