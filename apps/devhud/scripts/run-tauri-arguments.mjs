const desktopTauriFeatures = ["--features", "tauri/cef"];

export function desktopTauriArguments(command, forwardedArguments) {
  // The pinned CLI does not discover CEF in a target-scoped dependency when
  // assembling bundle settings. Keep this explicit until it supports that layout.
  return command ? [command, ...desktopTauriFeatures, ...forwardedArguments] : [];
}
