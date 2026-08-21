import { fileURLToPath } from "node:url";

const desktopTauriFeatures = ["--features", "desktop-cef"];
export const desktopTauriConfigPath = fileURLToPath(
  new URL("../src-tauri/tauri.desktop.conf.json", import.meta.url),
);

function requestedWindowsBundle(forwardedArguments) {
  const bundles = [];
  for (let index = 0; index < forwardedArguments.length; index += 1) {
    const argument = forwardedArguments[index];
    if (argument === "--bundles" || argument === "-b") {
      let valueIndex = index + 1;
      while (valueIndex < forwardedArguments.length && !forwardedArguments[valueIndex].startsWith("-")) {
        bundles.push(...forwardedArguments[valueIndex].split(","));
        valueIndex += 1;
      }
      index = valueIndex - 1;
      continue;
    }
    if (argument.startsWith("--bundles=") || argument.startsWith("-b=")) {
      bundles.push(...argument.slice(argument.indexOf("=") + 1).split(","));
    }
  }

  const selected = [...new Set(bundles.filter(Boolean))];
  if (selected.length !== 1 || !["msi", "nsis"].includes(selected[0])) {
    throw new Error("Windows package builds require exactly one --bundles msi or --bundles nsis selection");
  }
  return selected[0];
}

export function desktopTauriArguments(command, forwardedArguments) {
  // The pinned CLI resolves bundle features through app-owned Cargo features;
  // passing tauri/cef directly builds CEF but leaves its bundle path unset.
  return command
    ? [command, ...desktopTauriFeatures, "--config", desktopTauriConfigPath, ...forwardedArguments]
    : [];
}

export function desktopTauriEnvironment(
  command,
  forwardedArguments,
  platform = process.platform,
  environment = process.env,
) {
  if (command !== "build" || platform !== "win32") return environment;

  const bundle = requestedWindowsBundle(forwardedArguments);
  const packageKind = `windows-${bundle}`;
  if (environment.DEVHUD_PACKAGE_KIND && environment.DEVHUD_PACKAGE_KIND !== packageKind) {
    throw new Error(
      `DEVHUD_PACKAGE_KIND ${environment.DEVHUD_PACKAGE_KIND} does not match the selected ${bundle} bundle`,
    );
  }
  return { ...environment, DEVHUD_PACKAGE_KIND: packageKind };
}
