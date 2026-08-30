import { spawnSync } from "node:child_process";
import {
  accessSync,
  constants,
  lstatSync,
  mkdtempSync,
  readdirSync,
  readlinkSync,
  renameSync,
  rmSync,
  statSync,
  symlinkSync,
  unlinkSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { basename, join } from "node:path";

const cefResourceNames = [
  "chrome_100_percent.pak",
  "chrome_200_percent.pak",
  "icudtl.dat",
  "libEGL.so",
  "libGLESv2.so",
  "libcef.so",
  "libvk_swiftshader.so",
  "libvulkan.so.1",
  "resources.pak",
  "v8_context_snapshot.bin",
  "vk_swiftshader_icd.json",
  "locales",
];

const appImageArchitectures = {
  arm64: "aarch64",
  x64: "x86_64",
};

function onlyEntry(directory, predicate, description) {
  const matches = readdirSync(directory, { withFileTypes: true }).filter(predicate);
  if (matches.length !== 1) {
    throw new Error(`expected exactly one ${description}, found ${matches.length}`);
  }
  return join(directory, matches[0].name);
}

function optionalSymlinkTarget(path) {
  try {
    const metadata = lstatSync(path);
    return metadata.isSymbolicLink() ? readlinkSync(path) : null;
  } catch (error) {
    if (error.code === "ENOENT") return undefined;
    throw error;
  }
}

export function finalizeAppImageLayout(appDirectory) {
  const wrapper = join(appDirectory, "bin/devhud");
  const sharun = join(appDirectory, "sharun");
  const executable = join(appDirectory, "shared/bin/devhud");
  const wrapperTarget = "../shared/bin/devhud";
  const currentWrapperTarget = optionalSymlinkTarget(wrapper);

  if (currentWrapperTarget === null) {
    const wrapperMetadata = statSync(wrapper);
    const sharunMetadata = statSync(sharun);
    if (
      wrapperMetadata.dev !== sharunMetadata.dev
      || wrapperMetadata.ino !== sharunMetadata.ino
    ) {
      throw new Error("AppImage devhud wrapper is not the expected sharun hard link");
    }
  } else if (currentWrapperTarget !== wrapperTarget) {
    throw new Error("AppImage devhud wrapper has an unexpected symlink target");
  }
  if (!statSync(executable).isFile()) {
    throw new Error("AppImage real devhud executable is missing");
  }

  const resourceLinks = cefResourceNames.map((name) => {
    const source = join(appDirectory, "bin", name);
    const destination = join(appDirectory, "shared/bin", name);
    const target = `../../bin/${name}`;
    statSync(source);
    const currentTarget = optionalSymlinkTarget(destination);
    if (currentTarget === null) {
      throw new Error(`AppImage shared CEF resource already exists: ${name}`);
    }
    if (currentTarget !== undefined && currentTarget !== target) {
      throw new Error(`AppImage shared CEF resource has an unexpected target: ${name}`);
    }
    return { currentTarget, destination, target };
  });

  // Scope: CEF AppImages built by pinned Tauri. Chromium must re-exec the real
  // ELF through the kernel because sharun's user-space exec omits GLib auxv data.
  if (currentWrapperTarget === null) {
    unlinkSync(wrapper);
    symlinkSync(wrapperTarget, wrapper);
  }
  for (const { currentTarget, destination, target } of resourceLinks) {
    if (currentTarget === undefined) symlinkSync(target, destination);
  }
}

function commandOutcome(result) {
  return result.signal ? `signal ${result.signal}` : `exit ${result.status ?? "unknown"}`;
}

export function finalizeLinuxAppImage({
  targetDirectory,
  architecture = process.arch,
  environment = process.env,
  run = spawnSync,
  appImageTool = join(tmpdir(), "appimagetool"),
}) {
  const appImageArchitecture = appImageArchitectures[architecture];
  if (!appImageArchitecture) {
    throw new Error(`unsupported Linux AppImage architecture ${architecture}`);
  }

  const bundleDirectory = join(targetDirectory, "release/bundle/appimage");
  const appDirectory = onlyEntry(
    bundleDirectory,
    (entry) => entry.isDirectory() && entry.name.endsWith(".AppDir"),
    "AppImage staging directory",
  );
  const appImage = onlyEntry(
    bundleDirectory,
    (entry) => entry.isFile() && entry.name.endsWith(".AppImage"),
    "AppImage artifact",
  );
  accessSync(appImageTool, constants.X_OK);
  finalizeAppImageLayout(appDirectory);

  const updateInformationResult = run(appImage, ["--appimage-updateinformation"], {
    encoding: "utf8",
    env: environment,
    shell: false,
  });
  if (updateInformationResult.error) throw updateInformationResult.error;
  if (updateInformationResult.status !== 0) {
    throw new Error(`AppImage update-information query failed with ${commandOutcome(updateInformationResult)}`);
  }
  const updateInformation = (updateInformationResult.stdout ?? "").trim();
  if (/\r|\n/u.test(updateInformation)) {
    throw new Error("AppImage update information must be one line");
  }

  const repackDirectory = mkdtempSync(join(bundleDirectory, ".devhud-finalize-"));
  try {
    const arguments_ = [
      "--output",
      repackDirectory,
      "--name",
      basename(appImage),
      "--appimage-arch",
      appImageArchitecture,
    ];
    if (updateInformation) arguments_.push("--update-info", updateInformation);
    arguments_.push(appDirectory);
    const result = run(appImageTool, arguments_, {
      env: environment,
      shell: false,
      stdio: "inherit",
    });
    if (result.error) throw result.error;
    if (result.status !== 0) {
      throw new Error(`AppImage repack failed with ${commandOutcome(result)}`);
    }
    const replacement = join(repackDirectory, basename(appImage));
    accessSync(replacement, constants.X_OK);
    renameSync(replacement, appImage);
  } finally {
    rmSync(repackDirectory, { force: true, recursive: true });
  }

  return appImage;
}
