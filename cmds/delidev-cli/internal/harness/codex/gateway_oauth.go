// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

// GatewayOAuthStatus is passive native telemetry, never login or readiness proof.
type GatewayOAuthStatus string

const (
	GatewayOAuthNotReady  GatewayOAuthStatus = "notReady"
	GatewayOAuthStarted   GatewayOAuthStatus = "started"
	GatewayOAuthSucceeded GatewayOAuthStatus = "succeeded"
	GatewayOAuthFailed    GatewayOAuthStatus = "failed"
)

func (s GatewayOAuthStatus) Valid() bool {
	switch s {
	case GatewayOAuthNotReady, GatewayOAuthStarted, GatewayOAuthSucceeded, GatewayOAuthFailed:
		return true
	default:
		return false
	}
}

type gatewayOAuthOptionalString struct {
	present bool
	value   *string
}

type gatewayOAuthNotification struct {
	providerID string
	status     GatewayOAuthStatus
	authURL    gatewayOAuthOptionalString
	errorText  gatewayOAuthOptionalString
}

func decodeGatewayOAuthOptionalString(raw json.RawMessage) (gatewayOAuthOptionalString, error) {
	value := gatewayOAuthOptionalString{present: len(raw) != 0}
	if !value.present {
		return value, nil
	}
	if domain.DecodeWithLimit(raw, &value.value, nativewire.MaxFrame) != nil || value.value != nil && domain.Text(*value.value, "native gateway metadata", nativewire.MaxFrame, false) != nil {
		return gatewayOAuthOptionalString{}, invalidGatewayOAuthNotification()
	}
	return value, nil
}

func decodeGatewayOAuthNotification(raw []byte) (gatewayOAuthNotification, error) {
	var wire struct {
		ProviderID *string             `json:"providerId"`
		Status     *GatewayOAuthStatus `json:"status"`
		AuthURL    json.RawMessage     `json:"authUrl"`
		Error      json.RawMessage     `json:"error"`
	}
	if domain.DecodeWithLimit(raw, &wire, nativewire.MaxFrame) != nil || wire.ProviderID == nil || domain.Text(*wire.ProviderID, "native gateway provider", 1024, true) != nil || wire.Status == nil || !wire.Status.Valid() {
		return gatewayOAuthNotification{}, invalidGatewayOAuthNotification()
	}
	authURL, err := decodeGatewayOAuthOptionalString(wire.AuthURL)
	if err != nil {
		return gatewayOAuthNotification{}, err
	}
	errorText, err := decodeGatewayOAuthOptionalString(wire.Error)
	if err != nil {
		return gatewayOAuthNotification{}, err
	}
	return gatewayOAuthNotification{providerID: *wire.ProviderID, status: *wire.Status, authURL: authURL, errorText: errorText}, nil
}

func invalidGatewayOAuthNotification() error {
	return domain.Fail(domain.Unsupported, "The native gateway OAuth notification does not match its closed protocol shape.", "Retain original execution and cleanup ownership; no gateway login was authorized.")
}
