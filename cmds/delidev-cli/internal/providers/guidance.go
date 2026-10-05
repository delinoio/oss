// SPDX-License-Identifier: Apache-2.0
package providers

import (
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Only static official key-creation pages/guides are published. Regional keys
// remain distinct; an inspector never derives or detects their type from data.
func keyCreationURL(id domain.ProviderPresetID) string {
	switch id {
	case domain.PresetOpenAI:
		return "https://platform.openai.com/api-keys"
	case domain.PresetAnthropic:
		return "https://platform.claude.com/settings/keys"
	case domain.PresetOpenRouter:
		return "https://openrouter.ai/settings/keys"
	case domain.PresetVercel:
		return "https://vercel.com/docs/ai-gateway/authentication-and-byok"
	case domain.PresetXAI:
		return "https://console.x.ai/"
	case domain.PresetDeepSeek:
		return "https://platform.deepseek.com/api_keys"
	case domain.PresetGemini:
		return "https://aistudio.google.com/apikey"
	case domain.PresetGroq:
		return "https://console.groq.com/keys"
	case domain.PresetMistral:
		return "https://docs.mistral.ai/admin/identity-access/api-keys"
	case domain.PresetTogetherAI:
		return "https://api.together.ai/settings/projects/~current/api-keys"
	case domain.PresetFireworksAI:
		return "https://app.fireworks.ai/settings/users/api-keys"
	case domain.PresetPerplexity:
		return "https://console.perplexity.ai/project/keys"
	case domain.PresetCohere:
		return "https://dashboard.cohere.com/api-keys"
	case domain.PresetCerebras:
		return "https://inference-docs.cerebras.ai/console/api-keys"
	case domain.PresetNebius:
		return "https://docs.tokenfactory.nebius.com/quickstart"
	case domain.PresetNovita:
		return "https://docs.novita.ai/api-reference/basic-authentication"
	case domain.PresetDeepInfra:
		return "https://deepinfra.com/dash/api_keys"
	case domain.PresetHuggingFace:
		return "https://huggingface.co/settings/tokens"
	case domain.PresetVenice:
		return "https://venice.ai/settings/api"
	case domain.PresetScaleway:
		return "https://www.scaleway.com/en/docs/generative-apis/quickstart/"
	case domain.PresetBaseten:
		return "https://app.baseten.co/settings/api_keys"
	case domain.PresetMoonshot:
		return "https://platform.kimi.ai/console/api-keys"
	case domain.PresetMoonshotCN:
		return "https://platform.kimi.com/console/api-keys"
	case domain.PresetMiniMax:
		return "https://platform.minimax.io/user-center/basic-information/interface-key"
	case domain.PresetMiniMaxCN:
		return "https://platform.minimax.cn/user-center/basic-information/interface-key"
	case domain.PresetSiliconFlow:
		return "https://cloud.siliconflow.com/account/ak"
	case domain.PresetSiliconFlowCN:
		return "https://cloud.siliconflow.cn/account/ak"
	case domain.PresetQianfan:
		return "https://cloud.baidu.com/doc/qianfan-api/s/ym9chdsy5"
	case domain.PresetTencentTokenHub, domain.PresetTencentTokenHubInternational:
		return "https://cloud.tencent.com/document/product/1823/130090"
	case domain.PresetAlibabaModelStudioInternational, domain.PresetAlibabaModelStudioHongKong:
		return "https://help.aliyun.com/en/model-studio/get-api-key"
	default:
		return ""
	}
}

// GuidanceJSON is the tool-owned compiled native presentation allowlist. It
// contains no API endpoint overrides or credential/execution authority.
func GuidanceJSON() ([]byte, error) {
	type entry struct {
		ID             domain.ProviderPresetID `json:"id"`
		Documentation  string                  `json:"documentation"`
		KeyCreationURL string                  `json:"key_creation_url"`
	}
	entries := make([]entry, 0, 35)
	for _, preset := range Presets() {
		entries = append(entries, entry{preset.ID, preset.Documentation, preset.KeyCreationURL})
	}
	raw, err := json.MarshalIndent(struct {
		Version uint32  `json:"version"`
		Entries []entry `json:"entries"`
	}{1, entries}, "", "  ")
	return append(raw, '\n'), err
}
