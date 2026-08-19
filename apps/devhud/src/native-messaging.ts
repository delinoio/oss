import { invoke } from "@tauri-apps/api/core";
import { resolveLanguage } from "./shell.ts";
import type { DevHudSettingsV1 } from "./settings-contract.ts";

export interface NativeMessagingPairingStatus {
  readonly paired: boolean;
  readonly pairingNonce?: string | null;
  readonly expiresInSeconds?: number | null;
}

export function extensionConfiguration(settings: DevHudSettingsV1) {
  return {
    origins: settings.urlMappings
      .filter((mapping) => mapping.chromeOrigin !== null)
      .map((mapping) => ({ origin: mapping.chromeOrigin!, mappingId: mapping.id }))
      .sort((left, right) => left.origin.localeCompare(right.origin) || left.mappingId.localeCompare(right.mappingId)),
    language: resolveLanguage(settings.appearance.language, navigator.languages),
  } as const;
}

async function nativeCommand<Result>(command: string, args?: Record<string, unknown>): Promise<Result> {
  if (!window.__TAURI_INTERNALS__) throw new Error("unsupported");
  return await invoke<Result>(command, args);
}

export const nativeMessaging = {
  status: () => nativeCommand<NativeMessagingPairingStatus>("native_messaging_status"),
  beginPairing: () => nativeCommand<NativeMessagingPairingStatus>("native_messaging_begin_pairing"),
  unpair: () => nativeCommand<NativeMessagingPairingStatus>("native_messaging_unpair"),
  configure: (settings: DevHudSettingsV1) => nativeCommand<void>("native_messaging_replace_configuration", { configuration: extensionConfiguration(settings) }),
};
