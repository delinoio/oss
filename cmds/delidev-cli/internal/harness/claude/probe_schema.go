package claude

import (
	"encoding/json"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// These descriptors are private handshake data, not account model discovery,
// execution capabilities, pricing or permission authority. No raw descriptor
// escapes the probe. Optional model features vary within this native profile.
type initializeResult struct {
	Commands                     []json.RawMessage `json:"commands"`
	Agents                       []probeAgent      `json:"agents"`
	OutputStyle                  string            `json:"output_style"`
	AvailableOutputStyles        []string          `json:"available_output_styles"`
	Models                       []probeModel      `json:"models"`
	Account                      *probeAccount     `json:"account"`
	PID                          int               `json:"pid"`
	CurrentPermissionMode        string            `json:"current_permission_mode"`
	RemoteControlAutoEnable      *bool             `json:"remote_control_auto_enable"`
	RemoteControlAutoOnByDefault *bool             `json:"remote_control_auto_on_by_default"`
	IDERCAutoEnableGate          *bool             `json:"ide_rc_auto_enable_gate"`
	FastModeState                string            `json:"fast_mode_state"`
	FastModeDisabledReason       string            `json:"fast_mode_disabled_reason"`
}

type probeAccount struct {
	TokenSource string `json:"tokenSource"`
	APIProvider string `json:"apiProvider"`
}

type probeAgent struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Model       string `json:"model,omitempty"`
}

type probeModel struct {
	Value                    string   `json:"value"`
	ResolvedModel            string   `json:"resolvedModel"`
	DisplayName              string   `json:"displayName"`
	Description              string   `json:"description"`
	SupportsEffort           *bool    `json:"supportsEffort,omitempty"`
	SupportedEffortLevels    []string `json:"supportedEffortLevels,omitempty"`
	SupportsAdaptiveThinking *bool    `json:"supportsAdaptiveThinking,omitempty"`
	SupportsFastMode         *bool    `json:"supportsFastMode,omitempty"`
	SupportsAutoMode         *bool    `json:"supportsAutoMode,omitempty"`
}

func validateInitialize(raw []byte, expected domain.ID) error {
	var message struct {
		Type     string `json:"type"`
		Response *struct {
			Subtype   string          `json:"subtype"`
			RequestID domain.ID       `json:"request_id"`
			Response  json.RawMessage `json:"response,omitempty"`
			Error     json.RawMessage `json:"error,omitempty"`
		} `json:"response"`
	}
	if domain.Decode(raw, &message) != nil || message.Type != "control_response" || message.Response == nil || message.Response.RequestID != expected {
		return incompatible()
	}
	response := message.Response
	if response.Subtype == "error" && len(response.Error) != 0 && len(response.Response) == 0 {
		var message *string
		if json.Unmarshal(response.Error, &message) != nil || message == nil {
			return incompatible()
		}
		// Native errors have no stable code. Their untrusted text can reflect
		// credentials/paths; return a closed classification without retaining it.
		return probeUnavailable()
	}
	if response.Subtype != "success" || len(response.Error) != 0 {
		return incompatible()
	}
	var result initializeResult
	if domain.Decode(response.Response, &result) != nil || result.Commands == nil || len(result.Commands) != 0 ||
		result.Account == nil || result.Account.TokenSource != "none" || result.Account.APIProvider != "firstParty" ||
		result.PID <= 0 || result.CurrentPermissionMode != "dontAsk" || result.OutputStyle != "default" ||
		!explicitFalse(result.RemoteControlAutoEnable) || !explicitFalse(result.RemoteControlAutoOnByDefault) || !explicitFalse(result.IDERCAutoEnableGate) ||
		result.FastModeState != "off" || result.FastModeDisabledReason != "sdk_opt_in_required" {
		return incompatible()
	}
	if !uniqueText(result.AvailableOutputStyles, 32, 128) || !slices.Contains(result.AvailableOutputStyles, result.OutputStyle) ||
		len(result.Agents) == 0 || len(result.Agents) > 64 || len(result.Models) == 0 || len(result.Models) > 256 {
		return incompatible()
	}
	names := map[string]bool{}
	for _, agent := range result.Agents {
		if names[agent.Name] || domain.Text(agent.Name, "native agent", 128, true) != nil ||
			domain.Text(agent.Description, "native agent description", 16<<10, true) != nil || domain.Text(agent.Model, "native agent model", 256, false) != nil {
			return incompatible()
		}
		names[agent.Name] = true
	}
	names = map[string]bool{}
	for _, model := range result.Models {
		if names[model.Value] || domain.Text(model.Value, "native model", 256, true) != nil ||
			domain.Text(model.ResolvedModel, "native resolved model", 256, true) != nil ||
			domain.Text(model.DisplayName, "native model display name", 256, true) != nil || domain.Text(model.Description, "native model description", 16<<10, true) != nil {
			return incompatible()
		}
		names[model.Value] = true
		if model.SupportsEffort != nil && *model.SupportsEffort {
			if !uniqueText(model.SupportedEffortLevels, 5, 16) {
				return incompatible()
			}
			for _, level := range model.SupportedEffortLevels {
				if !slices.Contains([]string{"low", "medium", "high", "xhigh", "max"}, level) {
					return incompatible()
				}
			}
		} else if len(model.SupportedEffortLevels) != 0 {
			return incompatible()
		}
	}
	return nil
}

func explicitFalse(value *bool) bool { return value != nil && !*value }

func uniqueText(values []string, count, length int) bool {
	if len(values) == 0 || len(values) > count {
		return false
	}
	seen := map[string]bool{}
	for _, value := range values {
		if seen[value] || domain.Text(value, "native descriptor", length, true) != nil {
			return false
		}
		seen[value] = true
	}
	return true
}
