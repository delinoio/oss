// SPDX-License-Identifier: Apache-2.0
import { ApiAuthentication, ApiProtocol, type ProviderApiFormat } from "./gen/delidev/v1/common_pb.js";

export enum APIFormatId {
  Responses = "openai-responses",
  ChatCompletions = "openai-chat",
  Messages = "anthropic-messages",
}
export enum APIAuthenticationId { Bearer = "bearer", APIKey = "api-key", Keyless = "keyless" }
export interface APIFormatProfile { protocol: APIFormatId; endpoint: string; authentication: APIAuthenticationId }
export const apiFormatLabels: Readonly<Record<APIFormatId, string>> = {
  [APIFormatId.Responses]: "OpenAI Responses",
  [APIFormatId.ChatCompletions]: "OpenAI Chat Completions",
  [APIFormatId.Messages]: "Anthropic Messages",
};
export function apiFormat(value: unknown): APIFormatId | undefined {
  return Object.values(APIFormatId).find(format => format === value);
}
export function apiFormatProfile(value: unknown): APIFormatProfile | undefined {
  if (!value || typeof value !== "object" || Array.isArray(value)) return undefined;
  const profile = value as Record<string, unknown>;
  const protocol = apiFormat(profile.protocol);
  const authentication = Object.values(APIAuthenticationId).find(auth => auth === profile.authentication);
  return protocol && authentication && typeof profile.endpoint === "string" && profile.endpoint.length > 0 ? { protocol, authentication, endpoint: profile.endpoint } : undefined;
}
export function apiFormatProfileFromWire(value: ProviderApiFormat): APIFormatProfile | undefined {
  const protocol = value.protocol === ApiProtocol.OPENAI_RESPONSES ? APIFormatId.Responses :
    value.protocol === ApiProtocol.OPENAI_CHAT ? APIFormatId.ChatCompletions :
      value.protocol === ApiProtocol.ANTHROPIC_MESSAGES ? APIFormatId.Messages : undefined;
  const authentication = value.authentication === ApiAuthentication.BEARER ? APIAuthenticationId.Bearer :
    value.authentication === ApiAuthentication.API_KEY ? APIAuthenticationId.APIKey :
      value.authentication === ApiAuthentication.KEYLESS ? APIAuthenticationId.Keyless : undefined;
  return protocol && authentication && value.endpoint.length > 0 ? { protocol, endpoint: value.endpoint, authentication } : undefined;
}
// Provider resources already contain server-projected managed profiles. Legacy
// servers expose only their original tuple and cannot authorize format editing.
export function providerAPIFormats(provider: Record<string, unknown>): APIFormatProfile[] {
  if (Array.isArray(provider.api_formats)) return provider.api_formats.flatMap(value => { const profile = apiFormatProfile(value); return profile ? [profile] : []; });
  const profile = apiFormatProfile(provider);
  return profile ? [profile] : [];
}
export function accountAPIProfile(provider: Record<string, unknown>, account: Record<string, unknown>): APIFormatProfile | undefined {
  if (account.type !== "api") return undefined;
  if (account.api_protocol === undefined) return apiFormatProfile(provider);
  return providerAPIFormats(provider).find(profile => profile.protocol === account.api_protocol);
}
export function apiFormatMatchesHarness(format: unknown, harness: unknown): boolean {
  return harness === "codex" ? format === APIFormatId.Responses : harness === "claude-code" ? format === APIFormatId.Messages : (harness === "opencode" || harness === "grok-build") && format === APIFormatId.ChatCompletions;
}
export function apiFormatToWire(format: APIFormatId): ApiProtocol {
  return format === APIFormatId.Responses ? ApiProtocol.OPENAI_RESPONSES : format === APIFormatId.ChatCompletions ? ApiProtocol.OPENAI_CHAT : ApiProtocol.ANTHROPIC_MESSAGES;
}
