import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const desktopTauriFeatures = ["--features", "desktop-cef"];
export const repositoryAppleSigningIdentityKey = "devhud.appleSigningIdentity";
const noRepositoryGitConfigError = "fatal: --local can only be used inside a git repository";
export const desktopTauriConfigPath = fileURLToPath(
  new URL("../src-tauri/tauri.desktop.conf.json", import.meta.url),
);
export const privateReleaseTauriConfigPath = fileURLToPath(
  new URL("../src-tauri/tauri.private-release.conf.json", import.meta.url),
);
const cefPins = JSON.parse(
  readFileSync(fileURLToPath(new URL("../cef-pins.json", import.meta.url)), "utf8"),
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

function appImageSharunAsset(architecture, sharunPin = cefPins.appImage.sharun) {
  const asset = sharunPin.assets[architecture];
  if (!asset) {
    throw new Error(`unsupported Linux AppImage architecture ${architecture}`);
  }
  return {
    sha256: asset.sha256,
    url: `${sharunPin.repository}/releases/download/${sharunPin.version}/${asset.name}`,
  };
}

export async function prepareVerifiedAppImageSharun(
  architecture = process.arch,
  fetchAsset = globalThis.fetch,
  sharunPin = cefPins.appImage.sharun,
  { anylinuxPin = cefPins.appImage.anylinux, runCompiler = spawnSync } = {},
) {
  const asset = appImageSharunAsset(architecture, sharunPin);
  const response = await fetchAsset(asset.url, { signal: AbortSignal.timeout(120_000) });
  if (!response.ok) {
    throw new Error(`AppImage launcher download failed with HTTP ${response.status}`);
  }
  const bytes = Buffer.from(await response.arrayBuffer());
  const observedSha256 = createHash("sha256").update(bytes).digest("hex");
  if (observedSha256 !== asset.sha256) {
    throw new Error(
      `AppImage launcher checksum mismatch: expected ${asset.sha256}, observed ${observedSha256}`,
    );
  }

  // The pinned upstream script's default anylinux.c URL returns HTTP 404.
  // Verify the source and license from its owning fork before compiling locally;
  // quick-sharun preserves an existing lib/anylinux.so and adds it to .preload.
  // Remove this staging when Tauri pins and verifies these helper inputs itself.
  const directory = mkdtempSync(join(tmpdir(), "devhud-verified-sharun-"));
  const launcher = join(directory, "sharun");
  try {
    writeFileSync(launcher, bytes, { mode: 0o755, flag: "wx" });
    const base = `${anylinuxPin.repository.replace("https://github.com/", "https://raw.githubusercontent.com/")}/${anylinuxPin.revision}`;
    const verifiedFiles = {};
    for (const [kind, pin] of Object.entries({ source: anylinuxPin.source, license: anylinuxPin.license })) {
      const result = await fetchAsset(`${base}/${pin.path}`, { signal: AbortSignal.timeout(120_000) });
      if (!result.ok) throw new Error(`AppImage anylinux ${kind} download failed with HTTP ${result.status}`);
      const content = Buffer.from(await result.arrayBuffer());
      const digest = createHash("sha256").update(content).digest("hex");
      if (digest !== pin.sha256) throw new Error(`AppImage anylinux ${kind} checksum mismatch: expected ${pin.sha256}, observed ${digest}`);
      const path = join(directory, kind === "source" ? "anylinux.c" : "LICENSE-anylinux");
      writeFileSync(path, content, { mode: kind === "license" ? 0o644 : 0o600, flag: "wx" });
      verifiedFiles[kind] = path;
    }
    const library = join(directory, "anylinux.so");
    const compiled = runCompiler("cc", ["-shared", "-fPIC", "-O2", verifiedFiles.source, "-o", library], {
      shell: false, encoding: "utf8", timeout: 120_000,
    });
    if (compiled.error || compiled.status !== 0) {
      throw new Error(`AppImage anylinux compilation failed: ${compiled.error?.message ?? compiled.stderr ?? compiled.status}`);
    }
    return {
      config: { bundle: { linux: { appimage: { files: {
        sharun: launcher,
        "lib/anylinux.so": library,
        "share/licenses/anylinux/LICENSE": verifiedFiles.license,
      } } } } },
      close: () => rmSync(directory, { recursive: true, force: true }),
    };
  } catch (error) {
    rmSync(directory, { recursive: true, force: true });
    throw error;
  }
}
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
      env: { ...environment, LC_ALL: "C" },
      shell: false,
      stdio: ["ignore", "pipe", "pipe"],
    },
  );
  if (result.error) {
    throw new Error(`failed to read ${repositoryAppleSigningIdentityKey}: ${result.error.message}`);
  }
  const stderr = typeof result.stderr === "string" ? result.stderr.trim() : "";
  // Git uses status 1 for both a missing key and some unreadable-config errors,
  // and status 128 for absent repository metadata plus other fatal failures.
  // Stabilize its diagnostic locale and preserve ad hoc signing only for the
  // silent missing-key or exact source-copy cases.
  if (
    (result.status === 1 && stderr === "") ||
    (result.status === 128 && stderr === noRepositoryGitConfigError)
  ) {
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
  // The pinned CLI detects the desktop runtime dependency by target. Keep the
  // app-owned desktop marker and bundle configuration together.
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
  const result = {
    ...environment,
    DEVHUD_PACKAGE_KIND: packageKind,
    ...(bundle === "appimage"
      ? {
          // Scope: CEF AppImages. Remove these overrides when pinned Tauri uses a
          // sharun release with complete GLib auxv support and a non-hanging probe.
          STRACE_MODE: "0",
        }
      : {}),
  };
  if (bundle === "appimage") {
    // Use only the verified staged helpers. Ambient overrides must neither
    // redirect their downloads nor bypass upstream integrity checks.
    delete result.SHARUN_LINK;
    delete result.SKIP_INTEGRITY_CHECKS;
    delete result.ANYLINUX_LIB_SOURCE;
    result.ANYLINUX_LIB = "1";
  }
  return result;
}
