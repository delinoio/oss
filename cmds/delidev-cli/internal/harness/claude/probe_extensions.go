package claude

import (
	"bytes"
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Claude Code 2.1.287 sends one non-authoritative system/ui_invalidate startup
// frame before its initialize response and adds bounded fields to the response.
// These are private handshake metadata only: they never grant account, tool,
// remote-control or execution readiness, and every unknown shape fails closed.
const (
	probePreludeType     = "system"
	probePreludeSubtype  = "ui_invalidate"
	maxProbeIdentity     = 256
	maxProbeCapability   = 128
	maxProbeCapabilities = 64
	maxProbeStylesDir    = 4096
)

// probePrelude is the exact accepted startup frame schema. domain.Decode
// rejects unknown and duplicate keys, so only these optional identity fields can
// accompany the closed type/subtype pair.
type probePrelude struct {
	Type      string  `json:"type"`
	Subtype   string  `json:"subtype"`
	UUID      *string `json:"uuid,omitempty"`
	SessionID *string `json:"session_id,omitempty"`
}

// probeFrameIsPrelude reports whether a frame claims the startup event
// envelope. Any other frame type is validated as the initialize response.
func probeFrameIsPrelude(raw []byte) bool {
	var envelope struct {
		Type string `json:"type"`
	}
	return json.Unmarshal(raw, &envelope) == nil && envelope.Type == probePreludeType
}

// validateProbePrelude accepts only the closed ui_invalidate startup event. It
// never retains or logs the frame.
func validateProbePrelude(raw []byte) error {
	var prelude probePrelude
	if domain.Decode(raw, &prelude) != nil || prelude.Type != probePreludeType || prelude.Subtype != probePreludeSubtype {
		return incompatible()
	}
	for _, value := range []*string{prelude.UUID, prelude.SessionID} {
		if value != nil && domain.Text(*value, "native startup identity", maxProbeIdentity, true) != nil {
			return incompatible()
		}
	}
	return nil
}

// validateInitializeExtensions checks the 2.1.287 additions. Each field is
// optional so earlier verified profiles keep passing, but a declared field must
// have the exact safe shape; null, wrong types and unsafe values are rejected.
func validateInitializeExtensions(result initializeResult) error {
	for _, pending := range []json.RawMessage{result.PendingPermissionRequests, result.PendingUserDialogRequests} {
		if len(pending) == 0 {
			continue
		}
		var items []json.RawMessage
		if json.Unmarshal(pending, &items) != nil || items == nil || len(items) != 0 {
			return incompatible()
		}
	}
	if !optionalBool(result.AnalyticsDisabled) {
		return incompatible()
	}
	for _, gate := range []json.RawMessage{result.RemoteControlAvailable, result.RemoteControlAutoConnectDefault} {
		if len(gate) != 0 && !bytes.Equal(bytes.TrimSpace(gate), []byte("false")) {
			return incompatible()
		}
	}
	if len(result.FeedbackMode) != 0 {
		var mode string
		if json.Unmarshal(result.FeedbackMode, &mode) != nil || mode != "disabled" && mode != "off" {
			return incompatible()
		}
	}
	if len(result.SessionState) != 0 {
		var state string
		if json.Unmarshal(result.SessionState, &state) != nil || state != "idle" {
			return incompatible()
		}
	}
	if len(result.UserOutputStylesDir) != 0 {
		var dir string
		if json.Unmarshal(result.UserOutputStylesDir, &dir) != nil || domain.Text(dir, "native output styles directory", maxProbeStylesDir, true) != nil {
			return incompatible()
		}
	}
	if len(result.Capabilities) != 0 && !validProbeCapabilities(result.Capabilities) {
		return incompatible()
	}
	return nil
}

func optionalBool(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return true
	}
	trimmed := bytes.TrimSpace(raw)
	return bytes.Equal(trimmed, []byte("true")) || bytes.Equal(trimmed, []byte("false"))
}

// validProbeCapabilities admits a bounded list of unique descriptor names or a
// bounded flat object of scalar descriptors. Descriptors are discarded after
// validation and never become authority.
func validProbeCapabilities(raw json.RawMessage) bool {
	switch bytes.TrimSpace(raw)[0] {
	case '[':
		var names []string
		if json.Unmarshal(raw, &names) != nil || names == nil || len(names) > maxProbeCapabilities {
			return false
		}
		seen := map[string]bool{}
		for _, name := range names {
			if seen[name] || domain.Text(name, "native capability", maxProbeCapability, true) != nil {
				return false
			}
			seen[name] = true
		}
		return true
	case '{':
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || len(fields) > maxProbeCapabilities {
			return false
		}
		for key, value := range fields {
			if domain.Text(key, "native capability", maxProbeCapability, true) != nil {
				return false
			}
			var scalar any
			if json.Unmarshal(value, &scalar) != nil {
				return false
			}
			switch typed := scalar.(type) {
			case bool:
			case string:
				if domain.Text(typed, "native capability value", maxProbeCapability, false) != nil {
					return false
				}
			default:
				return false
			}
		}
		return true
	default:
		return false
	}
}
