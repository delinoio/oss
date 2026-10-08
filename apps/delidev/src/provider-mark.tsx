// SPDX-License-Identifier: Apache-2.0
import { useState } from "react";
import { ProviderPresetId } from "@delinoio/delidev-api-client";

// Presentation follows only the server's preset identity. A custom copy removes
// that identity and must never inherit branding from a name or endpoint.
export const providerMarkFamilies: ReadonlyMap<ProviderPresetId, string> = new Map([
  [ProviderPresetId.OPENAI, "openai"], [ProviderPresetId.ANTHROPIC, "anthropic"],
  [ProviderPresetId.OPENROUTER, "openrouter"], [ProviderPresetId.VERCEL_AI_GATEWAY, "vercel"],
  [ProviderPresetId.XAI, "xai"], [ProviderPresetId.DEEPSEEK, "deepseek"],
  [ProviderPresetId.GEMINI, "gemini"], [ProviderPresetId.GROQ, "groq"],
  [ProviderPresetId.MISTRAL, "mistral"], [ProviderPresetId.TOGETHER_AI, "together"],
  [ProviderPresetId.FIREWORKS_AI, "fireworks"], [ProviderPresetId.PERPLEXITY, "perplexity"],
  [ProviderPresetId.COHERE, "cohere"], [ProviderPresetId.CEREBRAS, "cerebras"],
  [ProviderPresetId.NEBIUS, "nebius"], [ProviderPresetId.NOVITA, "novita"],
  [ProviderPresetId.DEEPINFRA, "deepinfra"], [ProviderPresetId.HUGGING_FACE, "huggingface"],
  [ProviderPresetId.VENICE, "venice"], [ProviderPresetId.SCALEWAY, "scaleway"],
  [ProviderPresetId.BASETEN, "baseten"], [ProviderPresetId.MOONSHOT, "moonshot"],
  [ProviderPresetId.MOONSHOT_CN, "moonshot"], [ProviderPresetId.MINIMAX, "minimax"],
  [ProviderPresetId.MINIMAX_CN, "minimax"], [ProviderPresetId.SILICONFLOW, "siliconcloud"],
  [ProviderPresetId.SILICONFLOW_CN, "siliconcloud"], [ProviderPresetId.QIANFAN, "baiducloud"],
  [ProviderPresetId.TENCENT_TOKENHUB, "tencentcloud"], [ProviderPresetId.TENCENT_TOKENHUB_INTERNATIONAL, "tencentcloud"],
  [ProviderPresetId.ALIBABA_MODEL_STUDIO_INTERNATIONAL, "alibabacloud"], [ProviderPresetId.ALIBABA_MODEL_STUDIO_HONG_KONG, "alibabacloud"],
  [ProviderPresetId.OLLAMA, "ollama"], [ProviderPresetId.LM_STUDIO, "lmstudio"], [ProviderPresetId.VLLM, "vllm"],
]);

export function ProviderMark({ preset }: { preset: ProviderPresetId }) {
  const family = providerMarkFamilies.get(preset);
  const source = family ? `./provider-marks/${family}.svg` : undefined;
  const [failedSource, setFailedSource] = useState<string>();
  return <span className="api-provider-mark" aria-hidden="true">
    {source && source !== failedSource ? <img key={source} src={source} alt="" onError={() => setFailedSource(source)} /> :
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" aria-hidden="true"><rect x="3" y="3" width="7" height="7" rx="2" /><rect x="14" y="3" width="7" height="7" rx="2" /><rect x="3" y="14" width="7" height="7" rx="2" /><rect x="14" y="14" width="7" height="7" rx="2" /></svg>}
  </span>;
}
