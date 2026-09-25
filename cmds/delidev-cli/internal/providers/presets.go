package providers

import "github.com/delinoio/oss/cmds/delidev-cli/internal/domain"

// Presets are editable starting configurations, not an account connection or a
// claim that an installed harness supports every model/protocol on an endpoint.
func Presets() []domain.ProviderPreset {
	const compatibility = "Select a model compatible with the chosen API protocol and installed harness. Presets do not grant execution capabilities or validate an account. Localhost is the DeliDev server."
	items := []domain.ProviderPreset{
		{ID: domain.PresetVercel, Provider: domain.Provider{Name: "Vercel AI Gateway", Endpoint: "https://ai-gateway.vercel.sh/v1", Protocol: domain.OpenAIChat, Authentication: domain.BearerAuth}, Documentation: "https://vercel.com/docs/ai-gateway/authentication-and-byok", KeyGuidance: "Create an AI Gateway API key in the Vercel dashboard; connect it through account connect --key-stdin. No billing or administrator key is required."},
		{ID: domain.PresetOpenRouter, Provider: domain.Provider{Name: "OpenRouter", Endpoint: "https://openrouter.ai/api/v1", Protocol: domain.OpenAIChat, Authentication: domain.BearerAuth}, Documentation: "https://openrouter.ai/docs/api/reference/authentication", KeyGuidance: "Create an OpenRouter API key in account settings; connect it through account connect --key-stdin. No management key is required."},
		{ID: domain.PresetOpenAI, Provider: domain.Provider{Name: "OpenAI", Endpoint: "https://api.openai.com/v1", Protocol: domain.OpenAIResponses, Authentication: domain.BearerAuth}, Documentation: "https://developers.openai.com/api/reference/overview", KeyGuidance: "Create a project API key in the OpenAI developer platform and grant the required model/API permissions. Connect it through account connect --key-stdin; no organization admin key is required."},
		{ID: domain.PresetAnthropic, Provider: domain.Provider{Name: "Anthropic", Endpoint: "https://api.anthropic.com/v1", Protocol: domain.AnthropicMessages, Authentication: domain.APIKeyAuth}, Documentation: "https://platform.claude.com/docs/en/api/overview", KeyGuidance: "Create an API key for the intended Claude Console workspace; connect it through account connect --key-stdin. This is separate from a Claude subscription login."},
		{ID: domain.PresetXAI, Provider: domain.Provider{Name: "xAI", Endpoint: "https://api.x.ai/v1", Protocol: domain.OpenAIResponses, Authentication: domain.BearerAuth}, Documentation: "https://docs.x.ai/developers/rest-api-reference/inference", KeyGuidance: "Create an inference API key in the xAI console and connect it through account connect --key-stdin. Do not supply a management key."},
		{ID: domain.PresetDeepSeek, Provider: domain.Provider{Name: "DeepSeek", Endpoint: "https://api.deepseek.com/v1", Protocol: domain.OpenAIChat, Authentication: domain.BearerAuth}, Documentation: "https://api-docs.deepseek.com/", KeyGuidance: "Create a DeepSeek API key and connect it through account connect --key-stdin."},
		{ID: domain.PresetOllama, Provider: domain.Provider{Name: "Ollama", Endpoint: "http://127.0.0.1:11434/v1", Protocol: domain.OpenAIChat, Authentication: domain.KeylessAuth}, Documentation: "https://docs.ollama.com/api/openai-compatibility", KeyGuidance: "Run the local Ollama API on the server machine and explicitly connect with --keyless. Configure models in Ollama; DeliDev does not install them."},
		{ID: domain.PresetLMStudio, Provider: domain.Provider{Name: "LM Studio", Endpoint: "http://127.0.0.1:1234/v1", Protocol: domain.OpenAIChat, Authentication: domain.KeylessAuth}, Documentation: "https://lmstudio.ai/docs/developer/openai-compat", KeyGuidance: "Enable the LM Studio server on the DeliDev server machine. Use --keyless only when server authentication is disabled; otherwise edit authentication before connecting its key."},
		{ID: domain.PresetVLLM, Provider: domain.Provider{Name: "vLLM", Endpoint: "http://127.0.0.1:8000/v1", Protocol: domain.OpenAIChat, Authentication: domain.KeylessAuth}, Documentation: "https://docs.vllm.ai/en/latest/serving/online_serving/", KeyGuidance: "Run the vLLM server on the DeliDev server machine. Use --keyless only without API-key enforcement; otherwise configure bearer authentication and connect its key."},
	}
	for i := range items {
		items[i].Provider.Discovery = true
		items[i].Compatibility = compatibility
	}
	return items
}
