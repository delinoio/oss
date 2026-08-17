const desktopTauriFeatures = ["--features", "desktop-cef"];

export function desktopTauriArguments(command, forwardedArguments) {
  // The pinned CLI resolves bundle features through app-owned Cargo features;
  // passing tauri/cef directly builds CEF but leaves its bundle path unset.
  return command ? [command, ...desktopTauriFeatures, ...forwardedArguments] : [];
}
