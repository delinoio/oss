// SPDX-License-Identifier: Apache-2.0
import { spawnSync } from "node:child_process";
import { mkdtempSync, readFileSync, readdirSync, rmSync } from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { X509Certificate, createHash } from "node:crypto";
import { Identity } from "./beta.mjs";
export const Bundletool = {
  version: "1.18.3",
  url: "https://github.com/google/bundletool/releases/download/1.18.3/bundletool-all-1.18.3.jar",
  sha256: "a099cfa1543f55593bc2ed16a70a7c67fe54b1747bb7301f37fdfd6d91028e29",
};
function run(program, args) {
  const r = spawnSync(program, args, { encoding: "utf8", maxBuffer: 4 << 20 });
  if (r.error || r.status !== 0)
    throw new Error("Artifact signature or metadata validation failed");
  return r.stdout.trim();
}
export function verifyMetadata(value, input, platform) {
  if (
    value.identity !== Identity ||
    value.version !== input.version ||
    value.build !== (platform === "ios" ? input.iosBuild : input.androidCode) ||
    value.signer !==
      (platform === "ios" ? input.appleSigner : input.androidSigner) ||
    value.architectures.join(",") !==
      (platform === "ios" ? "arm64" : "arm64-v8a,armeabi-v7a") ||
    value.minimumVersion !== (platform === "ios" ? "18.0" : "31")
  )
    throw new Error(
      "Signed artifact does not match the original release identity",
    );
  return value;
}
export function inspectAndroid(file, input, bundletool) {
  if (
    createHash("sha256").update(readFileSync(bundletool)).digest("hex") !==
    Bundletool.sha256
  )
    throw new Error("Bundletool checksum mismatch");
  run("jarsigner", ["-verify", file]);
  const certificate = run("keytool", ["-printcert", "-jarfile", file]);
  const signer = certificate
    .match(/SHA256:\s*([A-Fa-f0-9:]+)/)?.[1]
    ?.replaceAll(":", "")
    .toLowerCase();
  const attribute = (x) =>
    run("java", [
      "-jar",
      bundletool,
      "dump",
      "manifest",
      `--bundle=${file}`,
      "--module=base",
      `--xpath=${x}`,
    ]);
  const names = run("unzip", ["-Z1", file]),
    architectures = [
      ...new Set(
        [
          ...names.matchAll(/^base\/lib\/([^/]+)\/libdelidev_mobile\.so$/gm),
        ].map((v) => v[1]),
      ),
    ].sort();
  const value = verifyMetadata(
    {
      identity: attribute("/manifest/@package"),
      version: attribute("/manifest/@android:versionName"),
      build: attribute("/manifest/@android:versionCode"),
      minimumVersion: attribute("/manifest/uses-sdk/@android:minSdkVersion"),
      architectures,
      signer,
    },
    input,
    "android",
  );
  return { ...value, sourceSha: input.sourceSha, bytes: readFileSync(file) };
}
export function inspectIos(file, input) {
  const directory = mkdtempSync(join(tmpdir(), "delidev-mobile-signature-"));
  try {
    const names = run("unzip", ["-Z1", file]).split("\n");
    if (
      names.some(
        (n) =>
          n.startsWith("/") ||
          n.split("/").includes("..") ||
          ![
            "Payload/",
            "SwiftSupport/",
            "Symbols/",
            "BCSymbolMaps/",
            "iTunesMetadata.plist",
          ].some((root) =>
            root.endsWith("/") ? n.startsWith(root) : n === root,
          ),
      )
    )
      throw new Error("Invalid IPA member ownership");
    run("unzip", ["-q", file, "-d", directory]);
    const apps = readdirSync(join(directory, "Payload")).filter((n) =>
      n.endsWith(".app"),
    );
    if (apps.length !== 1) throw new Error("IPA app identity is ambiguous");
    const app = join(directory, "Payload", apps[0]);
    run("codesign", ["--verify", "--deep", "--strict", app]);
    run("codesign", [
      "-d",
      `--extract-certificates=${directory}/certificate`,
      app,
    ]);
    const plist = join(app, "Info.plist"),
      get = (name) =>
        run("/usr/libexec/PlistBuddy", ["-c", `Print :${name}`, plist]);
    const signer = new X509Certificate(
      readFileSync(join(directory, "certificate0")),
    ).fingerprint256
      .replaceAll(":", "")
      .toLowerCase();
    const value = verifyMetadata(
      {
        identity: get("CFBundleIdentifier"),
        version: get("CFBundleShortVersionString"),
        build: get("CFBundleVersion"),
        minimumVersion: get("MinimumOSVersion"),
        architectures: run("lipo", [
          "-archs",
          join(app, get("CFBundleExecutable")),
        ]).split(/\s+/),
        signer,
      },
      input,
      "ios",
    );
    return { ...value, sourceSha: input.sourceSha, bytes: readFileSync(file) };
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
}
