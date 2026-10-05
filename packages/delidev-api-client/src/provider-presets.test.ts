// SPDX-License-Identifier: Apache-2.0
import { expect, it } from "vitest";
import { ProviderPresetId } from "./gen/delidev/v1/provider_pb.js";
import { hostedProviderPresetOrder, providerPresetNames } from "./provider-presets.js";

it("retains the old IDs and presents all 26 appended hosted identities before local presets", () => {
  expect([...providerPresetNames.keys()]).toEqual([3, 4, 2, 1, 5, 6, ...Array.from({ length: 26 }, (_, i) => i + 10), 7, 8, 9]);
  expect([...providerPresetNames.values()]).toEqual([
    "openai", "anthropic", "openrouter", "vercel-ai-gateway", "xai", "deepseek",
    "gemini", "groq", "mistral", "together-ai", "fireworks-ai", "perplexity", "cohere", "cerebras", "nebius", "novita", "deepinfra", "hugging-face", "venice", "scaleway", "baseten", "moonshot", "moonshot-cn", "minimax", "minimax-cn", "siliconflow", "siliconflow-cn", "qianfan", "tencent-tokenhub", "tencent-tokenhub-international", "alibaba-model-studio-international", "alibaba-model-studio-hong-kong",
    "ollama", "lm-studio", "vllm",
  ]);
  expect(hostedProviderPresetOrder).toEqual([...providerPresetNames.keys()].slice(0, 32));
  expect(providerPresetNames.get(ProviderPresetId.UNSPECIFIED)).toBeUndefined();
  expect(providerPresetNames.get(999 as ProviderPresetId)).toBeUndefined();
});
