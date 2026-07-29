import { copyFile, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { isAbsolute, resolve } from "node:path";

import { run } from "./process.mjs";

export const FIXTURE_EXTENSION_ID = "neiiglibncgobmehenjkhicabgfpggff";
export const FIXTURE_EXTENSION_KEY =
  "MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAryE0eBSTwCXYQfea466AEk8ZdgdfKi4HT/Go65Xcm5ZX1DqQc4s0Ckez6HCwQri0G9KxECP9dr1AWm+wJ1KBQn8CBzCBsQTjhmPmLSnwNNKKJFwgU7E+HR8lPGjNwB9VyLfzxciM0t34l1gH6Thq/D68Qy+c+jtqYYrwTHIpoA+HdqfjO+3eBQD5jX8dvVPJETs/CX3Lg8e0bmxrj5Wx6R67tJrFOaXxy4WHlvhYrZJ9pjRpYZ/xmgB1wWqq5kXrcNwUU4MJEaf51Jpsm9+vGBRRXFrmdGeicwkwxyu/hyDkkUf9t2/3ALEWryByxaf8N73GYeYFcsAnORg5T14wuwIDAQAB";

const appRoot = resolve(import.meta.dirname, "..");
const repositoryRoot = resolve(appRoot, "../..");
const extensionRoot = resolve(appRoot, "realqa-extension");
const outputRoot = resolve(appRoot, "build/realqa-extension");
const release = process.argv.includes("--release");
const checkOnly = process.argv.includes("--check");
const extensionId = release
  ? process.env.DEVHUD_CHROME_EXTENSION_ID
  : FIXTURE_EXTENSION_ID;
const approvedIds = new Set(
  (process.env.DEVHUD_APPROVED_CHROME_EXTENSION_IDS ?? "")
    .split(",")
    .map((value) => value.trim())
    .filter(Boolean),
);

function fail(message) {
  throw new Error(message);
}

export function resolveNativeHostPath(platform, configuredPath) {
  if (configuredPath !== undefined) return configuredPath;
  if (platform === "win32") {
    return "C:\\Program Files\\DevHud\\devhud-native-host.exe";
  }
  if (platform === "linux") {
    return "/opt/devhud/bin/devhud-native-host";
  }
  if (platform === "darwin") {
    fail("macOS extension packaging requires DEVHUD_NATIVE_HOST_PATH");
  }
  fail(`extension packaging is unsupported on ${platform}`);
}

if (extensionId === undefined || !/^[a-p]{32}$/u.test(extensionId)) {
  fail(
    "release packaging requires DEVHUD_CHROME_EXTENSION_ID as an exact 32-character Chrome extension ID",
  );
}
if (
  release &&
  (extensionId === FIXTURE_EXTENSION_ID || !approvedIds.has(extensionId))
) {
  fail(
    "release packaging requires the production ID in externally injected DEVHUD_APPROVED_CHROME_EXTENSION_IDS",
  );
}

const hostPath = resolveNativeHostPath(
  process.platform,
  process.env.DEVHUD_NATIVE_HOST_PATH,
);
if (release && process.platform !== "win32" && !isAbsolute(hostPath)) {
  fail("the release Native Messaging host path must be absolute");
}

if (release && checkOnly) {
  const cargo = process.platform === "win32" ? "cargo.exe" : "cargo";
  await run(
    cargo,
    [
      "check",
      "-p",
      "devhud",
      "--bin",
      "devhud-native-host",
      "--release",
      "--locked",
    ],
    {
      cwd: repositoryRoot,
      env: { ...process.env, DEVHUD_CHROME_EXTENSION_ID: extensionId },
    },
  );
}

if (!checkOnly) {
  const manifest = JSON.parse(
    await readFile(resolve(extensionRoot, "manifest.template.json"), "utf8"),
  );
  if (!release) manifest.key = FIXTURE_EXTENSION_KEY;
  await rm(outputRoot, { force: true, recursive: true });
  await mkdir(outputRoot, { recursive: true });
  await Promise.all(
    [
      "dom-selection.js",
      "popup.css",
      "popup.html",
      "popup.js",
      "protocol.js",
      "service-worker.js",
    ].map((file) =>
      copyFile(
        resolve(extensionRoot, "src", file),
        resolve(outputRoot, file),
      ),
    ),
  );
  await writeFile(
    resolve(outputRoot, "manifest.json"),
    `${JSON.stringify(manifest, null, 2)}\n`,
    "utf8",
  );
  const hostManifest = {
    name: "dev.deli.devhud.realqa",
    description: "DevHud RealQA capture host",
    path: hostPath,
    type: "stdio",
    allowed_origins: [`chrome-extension://${extensionId}/`],
  };
  await writeFile(
    resolve(outputRoot, "dev.deli.devhud.realqa.json"),
    `${JSON.stringify(hostManifest, null, 2)}\n`,
    "utf8",
  );
}

process.stdout.write(
  `RealQA extension package validated for ${release ? "release" : "fixture"} ID ${extensionId}.\n`,
);
