// SPDX-License-Identifier: Apache-2.0
package providers

import (
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// presetAPIFormats declares direct REST profiles only. Provider documentation
// and regional constraints are recorded in the provider contract. Catalog
// inspection uses the separate original profile, never the inference suffix.
func presetAPIFormats(id domain.ProviderPresetID, legacy domain.ProviderAPIFormat) []domain.ProviderAPIFormat {
	formats := []domain.ProviderAPIFormat{legacy}
	add := func(protocol domain.APIProtocol, endpoint string, authentication domain.Authentication) {
		if protocol != legacy.Protocol {
			formats = append(formats, domain.ProviderAPIFormat{Protocol: protocol, Endpoint: endpoint, Authentication: authentication})
		}
	}
	responses := func() { add(domain.OpenAIResponses, legacy.Endpoint, legacy.Authentication) }
	chat := func() { add(domain.OpenAIChat, legacy.Endpoint, domain.BearerAuth) }
	messages := func() { add(domain.AnthropicMessages, legacy.Endpoint, legacy.Authentication) }
	switch id {
	case domain.PresetOpenAI:
		chat()
	case domain.PresetAnthropic:
		chat()
	case domain.PresetOpenRouter, domain.PresetVercel, domain.PresetFireworksAI, domain.PresetPerplexity, domain.PresetHuggingFace, domain.PresetOllama, domain.PresetLMStudio, domain.PresetVLLM:
		responses()
		messages()
	case domain.PresetXAI:
		chat()
		messages()
	case domain.PresetGroq, domain.PresetQianfan:
		responses()
	case domain.PresetDeepSeek:
		responses()
		add(domain.AnthropicMessages, "https://api.deepseek.com/anthropic/v1", domain.APIKeyAuth)
	case domain.PresetDeepInfra:
		add(domain.OpenAIResponses, "https://api.deepinfra.com/v1", domain.BearerAuth)
		add(domain.AnthropicMessages, "https://api.deepinfra.com/anthropic/v1", domain.BearerAuth)
	case domain.PresetMoonshot, domain.PresetMoonshotCN, domain.PresetMiniMax, domain.PresetMiniMaxCN:
		responses()
		add(domain.AnthropicMessages, strings.TrimSuffix(legacy.Endpoint, "/v1")+"/anthropic/v1", domain.BearerAuth)
	case domain.PresetSiliconFlow, domain.PresetSiliconFlowCN, domain.PresetBaseten:
		messages()
	case domain.PresetTencentTokenHub, domain.PresetTencentTokenHubInternational:
		responses()
		add(domain.AnthropicMessages, legacy.Endpoint, domain.APIKeyAuth)
	case domain.PresetAlibabaModelStudioInternational, domain.PresetAlibabaModelStudioHongKong:
		responses()
		add(domain.AnthropicMessages, strings.TrimSuffix(legacy.Endpoint, "/compatible-mode/v1")+"/apps/anthropic/v1", domain.BearerAuth)
	}
	return formats
}

// WithAPIFormats enriches unchanged managed presets without rewriting legacy
// documents or changing their default connection authority. A copied preset ID
// alone is insufficient; its original tuple must also match the registry.
func WithAPIFormats(p domain.Provider) domain.Provider {
	if len(p.APIFormats) != 0 || p.PresetID == nil {
		return p
	}
	for _, preset := range Presets() {
		if preset.ID == *p.PresetID && preset.Provider.LegacyAPIFormat() == p.LegacyAPIFormat() {
			p.APIFormats = preset.Provider.APIFormats
			break
		}
	}
	return p
}

func APIFormats(p domain.Provider) []domain.ProviderAPIFormat {
	p = WithAPIFormats(p)
	if len(p.APIFormats) != 0 {
		return p.APIFormats
	}
	if p.Protocol.API() {
		return []domain.ProviderAPIFormat{p.LegacyAPIFormat()}
	}
	return nil
}

func HarnessMatches(h domain.Harness, protocol domain.APIProtocol) bool {
	switch h {
	case domain.Codex:
		return protocol == domain.OpenAIResponses
	case domain.ClaudeCode:
		return protocol == domain.AnthropicMessages
	case domain.OpenCode, domain.GrokBuild:
		return protocol == domain.OpenAIChat
	default:
		return false
	}
}

// ResolveAccountProfile is shared by configuration, inspection, routing and
// execution admission. It does not select an alternative format or translate.
func ResolveAccountProfile(p domain.Provider, a domain.Account) (domain.Provider, error) {
	if a.Type != domain.APIAccount {
		return p, nil
	}
	profile := p.LegacyAPIFormat()
	if a.APIProtocol != "" {
		found := false
		for _, candidate := range APIFormats(p) {
			if candidate.Protocol == a.APIProtocol {
				profile, found = candidate, true
				break
			}
		}
		if !found {
			return domain.Provider{}, domain.Fail(domain.Unsupported, "The account API format is unavailable.", "Choose a declared provider format after disconnecting and completing credential cleanup.")
		}
	}
	if !profile.Protocol.API() {
		return domain.Provider{}, domain.Fail(domain.Unsupported, "The account needs an API profile.", "Select an API provider.")
	}
	if a.Connection != nil {
		if a.Connection.Authentication != profile.Authentication || a.Connection.APIFormat != nil && *a.Connection.APIFormat != profile {
			return domain.Provider{}, domain.Fail(domain.Conflict, "The account connection profile changed.", "Restore its original profile or disconnect and finish credential cleanup.")
		}
	}
	p.Protocol, p.Endpoint, p.Authentication = profile.Protocol, profile.Endpoint, profile.Authentication
	p.APIFormats = nil
	return p, p.Validate()
}

// inspectionProvider matches the complete selected official tuple before using
// its fixed catalog/authentication destination. Custom catalogs remain advisory.
func inspectionProvider(p domain.Provider) domain.Provider {
	for _, preset := range Presets() {
		for _, profile := range preset.Provider.APIFormats {
			if profile == p.LegacyAPIFormat() {
				p.Protocol, p.Endpoint, p.Authentication = preset.Provider.Protocol, preset.Provider.Endpoint, preset.Provider.Authentication
				p.APIFormats = nil
				return p
			}
		}
	}
	return p
}
