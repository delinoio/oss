// SPDX-License-Identifier: Apache-2.0
import { createHash } from "node:crypto";
import {
  copyFileSync,
  existsSync,
  mkdirSync,
  readFileSync,
  readdirSync,
  statSync,
  writeFileSync,
} from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

export const platforms = Object.freeze({
  "macos-arm64": {
    target: "darwin-aarch64",
    installer: "FixKey-macos-arm64.dmg",
    payload: "FixKey-macos-arm64.app.tar.gz",
    bundle: ".app.tar.gz",
    package: ".dmg",
  },
  "windows-x64": {
    target: "windows-x86_64",
    installer: "FixKey-windows-x64.exe",
    payload: "FixKey-windows-x64.exe",
    bundle: "-setup.exe",
    package: "-setup.exe",
  },
  "linux-x11-x64": {
    target: "linux-x86_64",
    installer: "FixKey-linux-x11-x64.AppImage",
    payload: "FixKey-linux-x11-x64.AppImage",
    bundle: ".AppImage",
    package: ".AppImage",
  },
});
const root = fileURLToPath(new URL("../..", import.meta.url));
const hash = (data) => createHash("sha256").update(data).digest("hex");
function requireValue(condition, code) {
  if (!condition) throw new Error(code);
}
export function version(value) {
  requireValue(
    /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/u.test(value),
    "fixkey-version-invalid",
  );
  return value;
}
export function validateSource(directory, selected) {
  version(selected);
  for (const file of [
    "apps/fixkey/package.json",
    "apps/fixkey/src-tauri/tauri.conf.json",
  ]) {
    requireValue(
      JSON.parse(readFileSync(path.join(directory, file), "utf8")).version ===
        selected,
      "fixkey-source-version-mismatch",
    );
  }
  const manifest = readFileSync(
    path.join(directory, "apps/fixkey/src-tauri/Cargo.toml"),
    "utf8",
  );
  requireValue(
    manifest.match(/^version = "([^"]+)"$/mu)?.[1] === selected,
    "fixkey-source-version-mismatch",
  );
  const lock = readFileSync(path.join(directory, "Cargo.lock"), "utf8");
  requireValue(
    lock.includes(`name = "fixkey"\nversion = "${selected}"`),
    "fixkey-lock-version-mismatch",
  );
}
function files(directory) {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    requireValue(!entry.isSymbolicLink(), "fixkey-artifact-symlink");
    const file = path.join(directory, entry.name);
    return entry.isDirectory() ? files(file) : [file];
  });
}
export function configure(output, platform, publicKey, thumbprint) {
  requireValue(platforms[platform], "fixkey-platform-invalid");
  requireValue(publicKey?.trim(), "fixkey-updater-key-missing");
  const config = {
    plugins: {
      updater: {
        pubkey: publicKey.trim(),
        requireSignedVersion: true,
        allowDowngrades: false,
      },
    },
  };
  if (platform === "windows-x64") {
    requireValue(
      /^[a-f0-9]{40}$/iu.test(thumbprint ?? ""),
      "fixkey-windows-certificate-invalid",
    );
    config.bundle = {
      windows: {
        certificateThumbprint: thumbprint,
        digestAlgorithm: "sha256",
        timestampUrl: "http://timestamp.digicert.com",
      },
    };
  }
  writeFileSync(output, JSON.stringify(config, null, 2) + "\n");
}
export function stage(bundle, output, platform, selected, revision, publicKey) {
  version(selected);
  const spec = platforms[platform];
  requireValue(spec, "fixkey-platform-invalid");
  requireValue(/^[a-f0-9]{40}$/u.test(revision), "fixkey-revision-invalid");
  requireValue(publicKey?.trim(), "fixkey-updater-key-missing");
  const inventory = files(bundle);
  const unique = (suffix) => {
    const matches = inventory.filter((file) => file.endsWith(suffix));
    requireValue(matches.length === 1, "fixkey-bundle-missing-or-ambiguous");
    return matches[0];
  };
  const payload = unique(spec.bundle),
    installer = unique(spec.package),
    signature = `${payload}.sig`;
  requireValue(
    existsSync(signature) &&
      statSync(payload).size > 0 &&
      statSync(installer).size > 0,
    "fixkey-signed-payload-missing",
  );
  const encoded = readFileSync(signature, "utf8").trim();
  requireValue(
    encoded.length > 32 && /^[A-Za-z0-9+/=\r\n]+$/u.test(encoded),
    "fixkey-signature-invalid",
  );
  const trusted = Buffer.from(encoded, "base64")
    .toString("utf8")
    .split(/\r?\n/u)
    .find((line) => line.startsWith("trusted comment: "));
  const signedVersions =
    trusted
      ?.slice("trusted comment: ".length)
      .split("\t")
      .filter((field) => field.startsWith("version:")) ?? [];
  requireValue(
    signedVersions.length === 1 && signedVersions[0] === `version:${selected}`,
    "fixkey-signed-version-mismatch",
  );
  mkdirSync(output, { recursive: true });
  for (const [source, name] of [
    [installer, spec.installer],
    [payload, spec.payload],
    [signature, `${spec.payload}.sig`],
  ])
    copyFileSync(source, path.join(output, name));
  writeFileSync(
    path.join(output, `${platform}.json`),
    JSON.stringify(
      {
        schemaVersion: 1,
        platform,
        version: selected,
        revision,
        keyHash: hash(publicKey.trim()),
        files: [
          ...new Set([spec.installer, spec.payload, `${spec.payload}.sig`]),
        ]
          .sort()
          .map((name) => ({
            name,
            sha256: hash(readFileSync(path.join(output, name))),
          })),
      },
      null,
      2,
    ) + "\n",
  );
}
export function metadata(
  directory,
  selected,
  revision,
  publicKey,
  date = new Date(),
) {
  version(selected);
  requireValue(/^[a-f0-9]{40}$/u.test(revision), "fixkey-revision-invalid");
  requireValue(publicKey?.trim(), "fixkey-updater-key-missing");
  const targets = {};
  const checksums = [];
  for (const [platform, spec] of Object.entries(platforms)) {
    const record = JSON.parse(
      readFileSync(path.join(directory, `${platform}.json`), "utf8"),
    );
    requireValue(
      record.schemaVersion === 1 &&
        record.platform === platform &&
        record.version === selected &&
        record.revision === revision &&
        record.keyHash === hash(publicKey.trim()),
      "fixkey-artifact-identity-mismatch",
    );
    const expected = [
      ...new Set([spec.installer, spec.payload, `${spec.payload}.sig`]),
    ].sort();
    requireValue(
      JSON.stringify(record.files.map((file) => file.name)) ===
        JSON.stringify(expected),
      "fixkey-artifact-inventory-mismatch",
    );
    for (const file of record.files) {
      requireValue(
        hash(readFileSync(path.join(directory, file.name))) === file.sha256,
        "fixkey-artifact-checksum-mismatch",
      );
      checksums.push(`${file.sha256}  ${file.name}`);
    }
    targets[spec.target] = {
      signature: readFileSync(
        path.join(directory, `${spec.payload}.sig`),
        "utf8",
      ).trim(),
      url: `https://github.com/delinoio/oss/releases/download/fixkey-v${selected}/${spec.payload}`,
    };
  }
  const manifest = {
    version: selected,
    notes:
      "Signed FixKey desktop update. Settings are preserved. See the release for native acceptance evidence and manual recovery packages.",
    pub_date: date.toISOString(),
    platforms: targets,
  };
  writeFileSync(
    path.join(directory, "fixkey-latest.json"),
    JSON.stringify(manifest, null, 2) + "\n",
  );
  checksums.push(
    `${hash(readFileSync(path.join(directory, "fixkey-latest.json")))}  fixkey-latest.json`,
  );
  writeFileSync(
    path.join(directory, "SHA256SUMS"),
    checksums.sort().join("\n") + "\n",
  );
  return manifest;
}
if (
  process.argv[1] &&
  path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  const [command, ...args] = process.argv.slice(2);
  try {
    if (command === "configure")
      configure(
        ...args,
        process.env.FIXKEY_UPDATER_PUBLIC_KEY,
        process.env.FIXKEY_WINDOWS_THUMBPRINT,
      );
    else if (command === "source") validateSource(root, args[0]);
    else if (command === "stage")
      stage(...args, process.env.FIXKEY_UPDATER_PUBLIC_KEY);
    else if (command === "metadata")
      metadata(...args, process.env.FIXKEY_UPDATER_PUBLIC_KEY);
    else throw new Error("fixkey-release-command-invalid");
    console.log(
      JSON.stringify({
        component: "fixkey.release",
        command,
        result: "passed",
      }),
    );
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
