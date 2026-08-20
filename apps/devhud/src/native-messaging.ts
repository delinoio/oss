import { invoke } from "@tauri-apps/api/core";
import type { CaptureDraft } from "./native-bridge.ts";
import { resolveLanguage } from "./shell.ts";
import type { DevHudSettingsV1 } from "./settings-contract.ts";
import { compareMappings, parseUrlPattern, type ParsedUrlPattern, type UrlRepositoryMapping } from "./url-mapping.ts";

export interface NativeMessagingPairingStatus {
  readonly paired: boolean;
  readonly pairingNonce?: string | null;
  readonly expiresInSeconds?: number | null;
}

export interface NativeMessagingConfiguredMapping {
  readonly mappingId: string;
  readonly matcher: ParsedUrlPattern;
}

export interface NativeMessagingConfiguredOrigin {
  readonly origin: string;
  readonly mappings: readonly NativeMessagingConfiguredMapping[];
}

export interface NativeMessagingConfiguration {
  readonly origins: readonly NativeMessagingConfiguredOrigin[];
  readonly language: "en" | "ko";
}

export function extensionConfiguration(settings: DevHudSettingsV1) {
  const grouped = new Map<string, UrlRepositoryMapping[]>();
  for (const mapping of settings.urlMappings) {
    if (mapping.chromeOrigin === null) continue;
    const current = grouped.get(mapping.chromeOrigin);
    if (current) current.push(mapping);
    else grouped.set(mapping.chromeOrigin, [mapping]);
  }
  return {
    origins: [...grouped.entries()]
      .sort(([left], [right]) => left.localeCompare(right))
      .map(([origin, mappings]) => ({
        origin,
        mappings: [...mappings].sort(compareMappings).map((mapping) => ({ mappingId: mapping.id, matcher: parseUrlPattern(mapping.pattern) })),
      })),
    language: resolveLanguage(settings.appearance.language, navigator.languages),
  } satisfies NativeMessagingConfiguration;
}

async function nativeCommand<Result>(command: string, args?: Record<string, unknown>): Promise<Result> {
  if (!window.__TAURI_INTERNALS__) throw new Error("unsupported");
  return await invoke<Result>(command, args);
}

export const nativeMessaging = {
  status: () => nativeCommand<NativeMessagingPairingStatus>("native_messaging_status"),
  beginPairing: () => nativeCommand<NativeMessagingPairingStatus>("native_messaging_begin_pairing"),
  unpair: () => nativeCommand<NativeMessagingPairingStatus>("native_messaging_unpair"),
  configure: (settings: DevHudSettingsV1, scopeId: string) => nativeCommand<void>("native_messaging_replace_configuration", { configuration: extensionConfiguration(settings), scopeId }),
  takeContext: (draftId: string, expectedRevision: number) => nativeCommand<CaptureDraft | null>("native_messaging_take_context", { draftId, expectedRevision }),
};
