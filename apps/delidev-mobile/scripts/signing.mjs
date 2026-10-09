// SPDX-License-Identifier: Apache-2.0
import {
  mkdtempSync,
  writeFileSync,
  readFileSync,
  copyFileSync,
  mkdirSync,
  existsSync,
  rmSync,
} from "node:fs";
import { tmpdir, homedir } from "node:os";
import { join, resolve, dirname } from "node:path";
import { spawnSync } from "node:child_process";
import { randomBytes, createHash } from "node:crypto";
import { fileURLToPath } from "node:url";
import { Bundletool } from "./artifacts.mjs";
import { inputs, source } from "./pipeline.mjs";
import { validateProvisioning } from "./signing-input.mjs";
const root = resolve(dirname(fileURLToPath(import.meta.url)), "../../.."),
  environment = process.env;
function capture(program, args) {
  const r = spawnSync(program, args, { encoding: "utf8", maxBuffer: 4 << 20 });
  if (r.error || r.status !== 0)
    throw new Error("Protected signing setup failed");
  return r.stdout.trim();
}
function build(args, env) {
  const r = spawnSync(
    process.execPath,
    ["apps/delidev-mobile/scripts/pipeline.mjs", ...args],
    { cwd: root, env, stdio: "inherit" },
  );
  if (r.error || r.status !== 0)
    throw new Error("Original signed candidate build failed");
}
try {
  const input = inputs(environment);
  source(input);
  for (const name of [
    "DELIDEV_MOBILE_IOS_P12",
    "DELIDEV_MOBILE_IOS_P12_PASSWORD",
    "DELIDEV_MOBILE_IOS_PROFILE",
    "DELIDEV_MOBILE_APPLE_TEAM",
    "DELIDEV_MOBILE_ANDROID_KEYSTORE",
    "DELIDEV_MOBILE_ANDROID_KEY_ALIAS",
    "DELIDEV_MOBILE_ANDROID_KEY_PASSWORD",
    "DELIDEV_MOBILE_ANDROID_STORE_PASSWORD",
  ])
    if (!environment[name]) throw new Error("Protected signing input missing");
  const directory = mkdtempSync(join(tmpdir(), "delidev-mobile-signing-")),
    password = randomBytes(32).toString("base64url"),
    keychain = join(directory, "mobile.keychain-db"),
    previous =
      capture("security", ["list-keychains", "-d", "user"])
        .match(/"([^"]+)"/g)
        ?.map((v) => v.slice(1, -1)) ?? [];
  let created = false,
    installedProfile;
  try {
    const p12 = join(directory, "certificate.p12"),
      profile = join(directory, "profile.mobileprovision"),
      keystore = join(directory, "upload.jks");
    writeFileSync(
      p12,
      Buffer.from(environment.DELIDEV_MOBILE_IOS_P12, "base64"),
      { mode: 0o600 },
    );
    writeFileSync(
      profile,
      Buffer.from(environment.DELIDEV_MOBILE_IOS_PROFILE, "base64"),
      { mode: 0o600 },
    );
    writeFileSync(
      keystore,
      Buffer.from(environment.DELIDEV_MOBILE_ANDROID_KEYSTORE, "base64"),
      { mode: 0o600 },
    );
    const plist = join(directory, "profile.plist");
    writeFileSync(plist, capture("security", ["cms", "-D", "-i", profile]), {
      mode: 0o600,
    });
    const get = (name) =>
      capture("/usr/libexec/PlistBuddy", ["-c", `Print :${name}`, plist]);
    const optional = (name) => {
      try {
        return get(name);
      } catch {
        return "";
      }
    };
    const uuid = validateProvisioning(
      {
        uuid: get("UUID"),
        team: get("TeamIdentifier:0"),
        identity: get("Entitlements:application-identifier"),
        getTaskAllow: get("Entitlements:get-task-allow"),
        betaReports: get("Entitlements:beta-reports-active"),
        devices: !!optional("ProvisionedDevices"),
        allDevices: optional("ProvisionsAllDevices") === "true",
        expiration: capture("/usr/bin/plutil", [
          "-extract",
          "ExpirationDate",
          "raw",
          "-o",
          "-",
          plist,
        ]),
      },
      environment.DELIDEV_MOBILE_APPLE_TEAM,
    );
    capture("security", ["create-keychain", "-p", password, keychain]);
    created = true;
    capture("security", ["unlock-keychain", "-p", password, keychain]);
    capture("security", [
      "import",
      p12,
      "-k",
      keychain,
      "-P",
      environment.DELIDEV_MOBILE_IOS_P12_PASSWORD,
      "-T",
      "/usr/bin/codesign",
    ]);
    capture("security", [
      "set-key-partition-list",
      "-S",
      "apple-tool:,apple:",
      "-s",
      "-k",
      password,
      keychain,
    ]);
    capture("security", [
      "list-keychains",
      "-d",
      "user",
      "-s",
      keychain,
      ...previous,
    ]);
    const profileDirectory = join(
      homedir(),
      "Library/MobileDevice/Provisioning Profiles",
    );
    mkdirSync(profileDirectory, { recursive: true });
    const destination = join(profileDirectory, `${uuid}.mobileprovision`);
    if (existsSync(destination)) {
      if (!readFileSync(destination).equals(readFileSync(profile)))
        throw new Error("Existing provisioning profile conflict");
    } else {
      copyFileSync(profile, destination);
      installedProfile = destination;
    }
    const bundletool = join(directory, "bundletool.jar"),
      response = await fetch(Bundletool.url);
    if (!response.ok) throw new Error("Bundletool download failed");
    const bytes = Buffer.from(await response.arrayBuffer());
    if (
      bytes.length > 100 << 20 ||
      createHash("sha256").update(bytes).digest("hex") !== Bundletool.sha256
    )
      throw new Error("Bundletool checksum failed");
    writeFileSync(bundletool, bytes, { mode: 0o600 });
    const env = {
      ...environment,
      DELIDEV_MOBILE_IOS_PROFILE_UUID: uuid,
      DELIDEV_MOBILE_BUNDLETOOL: bundletool,
      TAURI_ANDROID_KEYSTORE_PATH: keystore,
      TAURI_ANDROID_KEYSTORE_PASSWORD:
        environment.DELIDEV_MOBILE_ANDROID_STORE_PASSWORD,
      TAURI_ANDROID_KEY_ALIAS: environment.DELIDEV_MOBILE_ANDROID_KEY_ALIAS,
      TAURI_ANDROID_KEY_PASSWORD:
        environment.DELIDEV_MOBILE_ANDROID_KEY_PASSWORD,
    };
    build(["build", "ios"], env);
    build(["build", "android"], env);
    build(["assemble"], env);
  } finally {
    try {
      capture("security", ["list-keychains", "-d", "user", "-s", ...previous]);
    } finally {
      if (created)
        try {
          capture("security", ["delete-keychain", keychain]);
        } catch {}
      if (installedProfile) rmSync(installedProfile);
      rmSync(directory, { recursive: true, force: true });
    }
  }
} catch {
  process.stderr.write(
    "Protected mobile signing failed. No store submission was attempted.\n",
  );
  process.exitCode = 1;
}
