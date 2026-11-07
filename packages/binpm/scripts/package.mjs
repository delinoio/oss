// SPDX-License-Identifier: Apache-2.0
import { createHash } from "node:crypto";
import {
  copyFileSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  readdirSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { execFileSync } from "node:child_process";
import { tmpdir } from "node:os";
import path from "node:path";
import { parseArgs } from "node:util";
import {
  LAUNCHER_NAME,
  ensure,
  event,
  homepage,
  isMain,
  nativeName,
  npm,
  packageRoot,
  placeholderVersion,
  repository,
  root,
  targets,
} from "./common.mjs";

const placeholderReadme = (name) => `# ${name}

This version reserves the \`${name}\` package name on npm. It contains no executable and no code.

The npm launcher for binpm is not published yet. Until it is, install binpm with the methods in the [installation guide](${homepage}/installation).
`;

// Source-controlled manifests for the placeholder packages. The launcher is
// listed last so native packages publish first, as the other repository npm
// distributions do.
export function placeholderManifests(version = placeholderVersion) {
  ensure(
    version === placeholderVersion,
    `Only the ${placeholderVersion} reservation placeholder can be packaged`,
  );
  const common = {
    version,
    license: "Apache-2.0",
    repository: { type: "git", url: repository },
    homepage,
    files: ["README.md", "LICENSE"],
  };
  return [
    ...targets.map((target) => ({
      name: nativeName(target),
      ...common,
      description: `Reserved platform package name for binpm on ${target.suffix}`,
      os: [target.os],
      cpu: [target.cpu],
      ...(target.libc ? { libc: [target.libc] } : {}),
    })),
    {
      name: LAUNCHER_NAME,
      ...common,
      description: "Reserved package name for the binpm npm launcher",
    },
  ];
}

export const sha512 = (bytes) =>
  `sha512-${createHash("sha512").update(bytes).digest("base64")}`;

export function packPlaceholders(
  outputDirectory,
  version = placeholderVersion,
) {
  const manifests = placeholderManifests(version);
  const staging = mkdtempSync(path.join(tmpdir(), "binpm-npm-"));
  try {
    mkdirSync(outputDirectory, { recursive: true });
    const artifacts = manifests.map((manifest) => {
      const directory = path.join(staging, manifest.name.replace(/\W+/gu, "-"));
      mkdirSync(directory);
      writeFileSync(
        path.join(directory, "package.json"),
        `${JSON.stringify(manifest, null, 2)}\n`,
      );
      writeFileSync(
        path.join(directory, "README.md"),
        placeholderReadme(manifest.name),
      );
      copyFileSync(path.join(root, "LICENSE"), path.join(directory, "LICENSE"));
      const [packed] = JSON.parse(
        npm(
          [
            "pack",
            "--json",
            "--ignore-scripts",
            "--pack-destination",
            path.resolve(outputDirectory),
          ],
          { cwd: directory },
        ),
      );
      const bytes = readFileSync(path.join(outputDirectory, packed.filename));
      return {
        name: manifest.name,
        version: manifest.version,
        filename: packed.filename,
        integrity: sha512(bytes),
      };
    });
    writeFileSync(
      path.join(outputDirectory, "artifacts.json"),
      `${JSON.stringify(artifacts, null, 2)}\n`,
    );
    for (const artifact of artifacts) event("package", artifact);
    return artifacts;
  } finally {
    rmSync(staging, { recursive: true, force: true });
  }
}

// Read-only: confirms the directory holds exactly the expected tarballs with the
// recorded integrity and manifests. It never repairs or rewrites an artifact.
export function verifyPlaceholders(directory, version = placeholderVersion) {
  const expected = placeholderManifests(version);
  const artifacts = JSON.parse(
    readFileSync(path.join(directory, "artifacts.json"), "utf8"),
  );
  ensure(
    artifacts.length === expected.length &&
      artifacts.every(
        (artifact, index) => artifact.name === expected[index].name,
      ),
    "Artifact list does not match the expected package set",
  );
  const tarballs = readdirSync(directory).filter((file) =>
    file.endsWith(".tgz"),
  );
  ensure(
    tarballs.length === artifacts.length &&
      artifacts.every(({ filename }) => tarballs.includes(filename)),
    "Unexpected or missing tarballs",
  );
  for (const [index, artifact] of artifacts.entries()) {
    const file = path.join(directory, artifact.filename);
    ensure(
      sha512(readFileSync(file)) === artifact.integrity,
      `Integrity mismatch: ${artifact.name}`,
    );
    const members = execFileSync("tar", ["-tzf", file], { encoding: "utf8" })
      .split(/\r?\n/u)
      .filter(Boolean)
      .sort();
    ensure(
      JSON.stringify(members) ===
        JSON.stringify([
          "package/LICENSE",
          "package/README.md",
          "package/package.json",
        ]),
      `Unexpected members in ${artifact.name}`,
    );
    const manifest = JSON.parse(
      execFileSync("tar", ["-xOzf", file, "package/package.json"], {
        encoding: "utf8",
      }),
    );
    ensure(
      JSON.stringify(manifest) === JSON.stringify(expected[index]),
      `Unexpected manifest in ${artifact.name}`,
    );
  }
  return artifacts;
}

export function main() {
  const { values } = parseArgs({
    options: {
      output: {
        type: "string",
        default: path.join(packageRoot, "dist/tarballs"),
      },
      version: { type: "string", default: placeholderVersion },
    },
  });
  packPlaceholders(values.output, values.version);
  verifyPlaceholders(values.output, values.version);
}

if (isMain(import.meta.url))
  try {
    main();
  } catch (error) {
    console.error(
      JSON.stringify({
        event: "binpm_npm_package_failed",
        message: error.message,
      }),
    );
    process.exitCode = 1;
  }
