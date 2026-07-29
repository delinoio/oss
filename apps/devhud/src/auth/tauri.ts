import { invoke } from "@tauri-apps/api/core";

import {
  AuthFeature,
  isNativeSessionSnapshot,
  type NativeSessionBridge,
  type NativeSessionSnapshot,
} from "./contracts";

function validatedSnapshot(value: unknown): NativeSessionSnapshot {
  if (!isNativeSessionSnapshot(value)) {
    throw "token-invalid";
  }
  return value;
}

export const tauriSessionBridge: NativeSessionBridge = {
  restore: async () =>
    validatedSnapshot(await invoke<unknown>("get_auth_session")),
  start: async (feature: AuthFeature) =>
    validatedSnapshot(
      await invoke<unknown>("start_authentication", { feature }),
    ),
  logout: async () =>
    validatedSnapshot(await invoke<unknown>("logout_authentication")),
};
