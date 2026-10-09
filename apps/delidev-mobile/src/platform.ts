// SPDX-License-Identifier: Apache-2.0
import { invoke } from "@tauri-apps/api/core";
import { listen } from "@tauri-apps/api/event";
export interface ProtectedStorage {
  read(): Promise<string | null>;
  write(value: string): Promise<void>;
}
export const storage: ProtectedStorage = {
  read: async () =>
    (
      await invoke<{ value: string | null }>("native_mobile_v1", {
        request: { operation: "read-state" },
      })
    ).value,
  write: async (value) => {
    await invoke("native_mobile_v1", {
      request: { operation: "write-state", value },
    });
  },
};
export async function permission(): Promise<boolean> {
  return (
    await invoke<{ granted: boolean }>("native_mobile_v1", {
      request: { operation: "permission" },
    })
  ).granted;
}
export async function notify(
  profileId: string,
  inboxId: string,
  language: string,
): Promise<boolean> {
  const result = await invoke<{ submitted: boolean }>("native_mobile_v1", {
    request: {
      operation: "notify",
      profile_id: profileId,
      inbox_id: inboxId,
      language,
    },
  });
  return result.submitted;
}
export async function observeForeground(
  callback: (active: boolean) => void,
): Promise<() => void> {
  const web = () => callback(document.visibilityState === "visible");
  document.addEventListener("visibilitychange", web);
  const stop = await listen<boolean>("delidev-mobile:lifecycle", (event) =>
    callback(event.payload),
  );
  return () => {
    document.removeEventListener("visibilitychange", web);
    stop();
  };
}

export async function tls(
  origin: string,
): Promise<"ok" | "certificate" | "network"> {
  const result = await invoke<{ status: "ok" | "certificate" | "network" }>(
    "native_mobile_v1",
    { request: { operation: "tls", origin } },
  );
  return result.status;
}
