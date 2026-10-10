// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

const GatewayOAuthStatusDiscarded MetadataKind = "gateway-oauth-status-discarded"

type nativeGatewayStatus string

const (
	nativeGatewayNotReady  nativeGatewayStatus = "notReady"
	nativeGatewayStarted   nativeGatewayStatus = "started"
	nativeGatewaySucceeded nativeGatewayStatus = "succeeded"
	nativeGatewayFailed    nativeGatewayStatus = "failed"
)

// Presence and null are distinct native observations. Neither the private value
// nor its presence supplies a login, provider or callback owner.
type nativeGatewayString struct {
	Present bool
	Null    bool
	Value   string
}

type nativeGatewayObservation struct {
	ProviderID string
	Status     nativeGatewayStatus
	AuthURL    nativeGatewayString
	Error      nativeGatewayString
}

func decodeGatewayString(raw json.RawMessage) (nativeGatewayString, error) {
	value := nativeGatewayString{Present: len(raw) != 0}
	if !value.Present {
		return value, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		value.Null = true
		return value, nil
	}
	if domain.Decode(raw, &value.Value) != nil || domain.Text(value.Value, "native gateway metadata", nativewire.MaxFrame, false) != nil {
		return nativeGatewayString{}, incompatible()
	}
	return value, nil
}

func decodeGatewayObservation(raw json.RawMessage) (nativeGatewayObservation, error) {
	var wire struct {
		ProviderID *string             `json:"providerId"`
		Status     nativeGatewayStatus `json:"status"`
		AuthURL    json.RawMessage     `json:"authUrl"`
		Error      json.RawMessage     `json:"error"`
	}
	if len(raw) > nativewire.MaxFrame || domain.Decode(raw, &wire) != nil || wire.ProviderID == nil || domain.Text(*wire.ProviderID, "native gateway provider", 1024, true) != nil {
		return nativeGatewayObservation{}, incompatible()
	}
	switch wire.Status {
	case nativeGatewayNotReady, nativeGatewayStarted, nativeGatewaySucceeded, nativeGatewayFailed:
	default:
		return nativeGatewayObservation{}, incompatible()
	}
	authURL, err := decodeGatewayString(wire.AuthURL)
	if err != nil {
		return nativeGatewayObservation{}, err
	}
	nativeError, err := decodeGatewayString(wire.Error)
	if err != nil {
		return nativeGatewayObservation{}, err
	}
	return nativeGatewayObservation{ProviderID: *wire.ProviderID, Status: wire.Status, AuthURL: authURL, Error: nativeError}, nil
}

func (c *Client) observeGatewayOAuthLocked(native nativewire.Event) (Event, error) {
	if _, err := decodeGatewayObservation(native.Params); err != nil {
		return Event{}, err
	}
	// No gateway operation is admitted by this adapter. Validate and discard
	// every status, including success and inert URL strings, without adopting
	// its provider or granting account readiness or an authentication reply.
	if c.logger != nil {
		c.logger.Debug("Codex native gateway metadata discarded", "owner_id", c.ownerID, "metadata", GatewayOAuthStatusDiscarded)
	}
	return c.metadata(GatewayOAuthStatusDiscarded), nil
}
