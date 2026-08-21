import { fileURLToPath } from "node:url";

const desktopTauriFeatures = ["--features", "desktop-cef"];
export const desktopTauriConfigPath = fileURLToPath(
  new URL("../src-tauri/tauri.desktop.conf.json", import.meta.url),
);

export function desktopTauriArguments(command, forwardedArguments) {
  // The pinned CLI resolves bundle features through app-owned Cargo features;
  // passing tauri/cef directly builds CEF but leaves its bundle path unset.
  return command
    ? [command, ...desktopTauriFeatures, "--config", desktopTauriConfigPath, ...forwardedArguments]
    : [];
}
