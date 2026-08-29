import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";

const desktopTauriFeatures = ["--features", "desktop-cef"];
export const repositoryAppleSigningIdentityKey = "devhud.appleSigningIdentity";
export const desktopTauriConfigPath = fileURLToPath(
  new URL("../src-tauri/tauri.desktop.conf.json", import.meta.url),
);
export const privateReleaseTauriConfigPath = fileURLToPath(
  new URL("../src-tauri/tauri.private-release.conf.json", import.meta.url),
);

function requestedPackageBundle(forwardedArguments, platformName, packageKinds) {
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
  if (selected.length !== 1 || !Object.hasOwn(packageKinds, selected[0])) {
    const choices = Object.keys(packageKinds).map((bundle) => `--bundles ${bundle}`).join(" or ");
    throw new Error(`${platformName} package builds require exactly one ${choices} selection`);
  }
  return selected[0];
}

const packageKindsByPlatform = {
  win32: {
    name: "Windows",
    bundles: { msi: "windows-msi", nsis: "windows-nsis" },
  },
  linux: {
    name: "Linux",
    bundles: { deb: "linux-deb", appimage: "linux-appimage" },
  },
};

export function repositoryAppleSigningEnvironment(
  command,
  platform = process.platform,
  environment = process.env,
  runGit = spawnSync,
) {
  if (
    platform !== "darwin" ||
    Object.hasOwn(environment, "APPLE_SIGNING_IDENTITY") ||
    environment.DEVHUD_PRIVATE_RELEASE === "1"
  ) {
    return environment;
  }

  const result = runGit(
    "git",
    ["config", "--local", "--get", repositoryAppleSigningIdentityKey],
    {
      encoding: "utf8",
      shell: false,
      stdio: ["ignore", "pipe", "pipe"],
    },
  );
  if (result.error) {
    throw new Error(`failed to read ${repositoryAppleSigningIdentityKey}: ${result.error.message}`);
  }
  // Git returns 1 when the key is unset and 128 when a source copy has no
  // repository metadata. In both cases the opt-in is unavailable, so retain
  // the committed ad hoc signing default.
  if (result.status === 1 || result.status === 128) {
    return environment;
  }
  if (result.status !== 0) {
    throw new Error(
      `git config --local --get ${repositoryAppleSigningIdentityKey} exited with status ${result.status ?? "unknown"}`,
    );
  }

  const identity = typeof result.stdout === "string" ? result.stdout.trim() : "";
  if (
    !identity ||
    /[\0\r\n]/u.test(identity) ||
    Buffer.byteLength(identity, "utf8") > 1024
  ) {
    throw new Error(`${repositoryAppleSigningIdentityKey} must be one non-empty line of at most 1024 UTF-8 bytes`);
  }

  return { ...environment, APPLE_SIGNING_IDENTITY: identity };
}

export function desktopTauriArguments(command, forwardedArguments, environment = process.env) {
  // The pinned CLI resolves bundle features through app-owned Cargo features;
  // passing tauri/cef directly builds CEF but leaves its bundle path unset.
  const config = command === "build" && environment.DEVHUD_PRIVATE_RELEASE === "1"
    ? privateReleaseTauriConfigPath
    : desktopTauriConfigPath;
  return command
    ? [command, ...desktopTauriFeatures, "--config", config, ...forwardedArguments]
    : [];
}

export function desktopTauriEnvironment(
  command,
  forwardedArguments,
  platform = process.platform,
  environment = process.env,
) {
  const packageConfiguration = packageKindsByPlatform[platform];
  if (command !== "build" || !packageConfiguration) return environment;

  const bundle = requestedPackageBundle(
    forwardedArguments,
    packageConfiguration.name,
    packageConfiguration.bundles,
  );
  const packageKind = packageConfiguration.bundles[bundle];
  if (environment.DEVHUD_PACKAGE_KIND && environment.DEVHUD_PACKAGE_KIND !== packageKind) {
    throw new Error(
      `DEVHUD_PACKAGE_KIND ${environment.DEVHUD_PACKAGE_KIND} does not match the selected ${bundle} bundle`,
    );
  }
  return { ...environment, DEVHUD_PACKAGE_KIND: packageKind };
}
