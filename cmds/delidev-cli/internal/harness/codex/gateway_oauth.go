// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

type gatewayOAuthStatus string

const (
	gatewayOAuthNotReady  gatewayOAuthStatus = "notReady"
	gatewayOAuthStarted   gatewayOAuthStatus = "started"
	gatewayOAuthSucceeded gatewayOAuthStatus = "succeeded"
	gatewayOAuthFailed    gatewayOAuthStatus = "failed"
)

type gatewayFieldState uint8

const (
	gatewayFieldOmitted gatewayFieldState = iota
	gatewayFieldNull
	gatewayFieldString
)

// All descriptors stay private, including whether optional values were sent.
// A passive status cannot establish an original gateway login owner.
type gatewayOAuthField struct {
	state gatewayFieldState
	value string
}

type gatewayOAuthObservation struct {
	providerID string
	status     gatewayOAuthStatus
	authURL    gatewayOAuthField
	failure    gatewayOAuthField
}

// Source: openai/codex a06545b311fe01e51ce855c7aa5d8da21e9e7aaf,
// app-server-protocol/schema/json/v2/GatewayOAuthChangedNotification.json.
func decodeGatewayOAuth(raw json.RawMessage) (gatewayOAuthObservation, error) {
	var wire *struct {
		ProviderID *string             `json:"providerId"`
		Status     *gatewayOAuthStatus `json:"status"`
		AuthURL    json.RawMessage     `json:"authUrl"`
		Error      json.RawMessage     `json:"error"`
	}
	if domain.DecodeBounded(raw, &wire, nativewire.MaxFrame) != nil || wire == nil || wire.ProviderID == nil || wire.Status == nil || domain.Text(*wire.ProviderID, "native gateway provider", 1024, true) != nil {
		return gatewayOAuthObservation{}, incompatible()
	}
	switch *wire.Status {
	case gatewayOAuthNotReady, gatewayOAuthStarted, gatewayOAuthSucceeded, gatewayOAuthFailed:
	default:
		return gatewayOAuthObservation{}, incompatible()
	}
	authURL, err := decodeGatewayOAuthField(wire.AuthURL)
	if err != nil {
		return gatewayOAuthObservation{}, err
	}
	failure, err := decodeGatewayOAuthField(wire.Error)
	if err != nil {
		return gatewayOAuthObservation{}, err
	}
	return gatewayOAuthObservation{providerID: *wire.ProviderID, status: *wire.Status, authURL: authURL, failure: failure}, nil
}

func decodeGatewayOAuthField(raw json.RawMessage) (gatewayOAuthField, error) {
	if len(raw) == 0 {
		return gatewayOAuthField{state: gatewayFieldOmitted}, nil
	}
	var value *string
	if json.Unmarshal(raw, &value) != nil {
		return gatewayOAuthField{}, incompatible()
	}
	if value == nil {
		return gatewayOAuthField{state: gatewayFieldNull}, nil
	}
	if domain.Text(*value, "native gateway metadata", nativewire.MaxFrame, false) != nil {
		return gatewayOAuthField{}, incompatible()
	}
	// URLs are bounded text, never parsed, fetched or used as provider authority.
	return gatewayOAuthField{state: gatewayFieldString, value: *value}, nil
}

func (c *Client) observeGatewayOAuthLocked(native nativewire.Event) (Event, error) {
	observation, err := decodeGatewayOAuth(native.Params)
	if err != nil {
		return Event{}, err
	}
	// There is no gateway operation owner. Discard provider, URL and error before
	// returning an event; retain only the closed status for safe diagnostics.
	event := c.metadata(GatewayOAuthStatusDiscarded)
	event.gatewayStatus = observation.status
	return event, nil
}
