import { invoke } from "@tauri-apps/api/core";

export interface StartupReceipt {
  applicationId: "dev.deli.devhud";
  bundledOrigin: string;
  runtime: "cef" | "system-webview";
  sandboxEnabled: boolean;
}

interface NativeCommandResults {
  probe_bundled_asset_ready: StartupReceipt;
  probe_denial_observed: null;
  probe_forbidden: never;
}

export interface ProbeBridge {
  invoke<K extends keyof NativeCommandResults>(
    command: K,
  ): Promise<NativeCommandResults[K]>;
}

export interface StartupHandshake {
  receipt: StartupReceipt;
  capabilityDenied: true;
}

export const tauriProbeBridge: ProbeBridge = {
  invoke: <K extends keyof NativeCommandResults>(command: K) =>
    invoke<NativeCommandResults[K]>(command),
};

export function isCapabilityDenial(error: unknown): boolean {
  const message =
    error instanceof Error
      ? error.message
      : typeof error === "string"
        ? error
        : "";

  return /(?:not allowed|denied|permission|capability)/iu.test(message);
}

export async function runBundledStartupHandshake(
  bridge: ProbeBridge,
): Promise<StartupHandshake> {
  const receipt = await bridge.invoke("probe_bundled_asset_ready");

  try {
    await bridge.invoke("probe_forbidden");
  } catch (error) {
    if (!isCapabilityDenial(error)) {
      throw error;
    }

    await bridge.invoke("probe_denial_observed");
    return {
      receipt,
      capabilityDenied: true,
    };
  }

  throw new Error("Forbidden probe command unexpectedly passed capability enforcement");
}
