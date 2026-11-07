import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import {
  readFileSync,
  readdirSync,
  writeFileSync,
  mkdirSync,
  lstatSync,
} from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { parseArgs } from "node:util";
import platforms from "../src/platforms.cjs";
import { archive, entries } from "./archive.mjs";

export const packageRoot = fileURLToPath(new URL("../", import.meta.url));
export const root = path.resolve(packageRoot, "../..");
export const sha256 = (bytes) =>
  createHash("sha256").update(bytes).digest("hex");
export const version = JSON.parse(
  readFileSync(path.join(packageRoot, "package.json")),
).version;
const source = (file) => readFileSync(path.join(root, file));
const revision = () =>
  execFileSync("git", ["rev-parse", "HEAD"], {
    cwd: root,
    encoding: "utf8",
  }).trim();
export function identity(sourceRevision, fixture = false) {
  assert(
    /^[0-9a-f]{40}$/.test(sourceRevision),
    "Expected exact source revision",
  );
  assert(
    /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/.test(version),
    "Invalid source version",
  );
  return { version, sourceRevision, fixture, tag: `ccow@v${version}` };
}
// The Rust build script embeds one static marker in each native executable:
// `ccow-build-info:{"version":"<v>","revision":"<sha>","target":"<triple>","dirty":false}`.
// Cross-built binaries cannot run on the packaging host, so verify the bytes.
export const BUILD_INFO_MARKER = "ccow-build-info:";
export function buildInfo(bytes) {
  const marker = Buffer.from(BUILD_INFO_MARKER);
  const found = new Set();
  for (
    let index = bytes.indexOf(marker);
    index >= 0;
    index = bytes.indexOf(marker, index + 1)
  ) {
    const start = index + marker.length;
    const end = bytes.indexOf(0x7d, start);
    assert(
      bytes[start] === 0x7b && end > start && end - start < 1024,
      "Malformed embedded build identity",
    );
    found.add(bytes.subarray(start, end + 1).toString("utf8"));
  }
  assert.equal(found.size, 1, "Expected exactly one embedded build identity");
  const info = JSON.parse([...found][0]);
  assert.deepEqual(
    Object.keys(info).sort(),
    ["dirty", "revision", "target", "version"],
    "Unexpected embedded build identity fields",
  );
  return info;
}
const json = (object) => Buffer.from(JSON.stringify(object, null, 2) + "\n");
const regular = (bytes) => ({ bytes });

// Output must be new. Never recursively delete caller-selected paths or overwrite
// an existing artifact; failed builds remain available for diagnosis.
export function packageRelease({
  binaries,
  output,
  sourceRevision = revision(),
  fixture = false,
}) {
  const id = identity(sourceRevision, fixture);
  assert.equal(typeof fixture, "boolean");
  assert.deepEqual(
    Object.keys(binaries).sort(),
    platforms.targets.map((t) => t.suffix).sort(),
    "Supply exactly the supported targets",
  );
  if (!fixture) {
    assert.equal(
      sourceRevision,
      revision(),
      "Package sources must match the binary revision",
    );
    assert.equal(
      execFileSync(
        "git",
        ["status", "--porcelain", "--untracked-files=normal"],
        { cwd: root, encoding: "utf8" },
      ).trim(),
      "",
      "Release packaging requires a clean checkout",
    );
  }
  const binaryBytes = new Map(
    platforms.targets.map((target) => {
      const file = binaries[target.suffix];
      assert(
        lstatSync(file).isFile(),
        "Binary must be a regular file, not a symlink",
      );
      const bytes = readFileSync(file);
      assert(bytes.length > 0, "Empty binary");
      if (!fixture)
        assert.deepEqual(
          buildInfo(bytes),
          {
            version,
            revision: sourceRevision,
            target: target.rust,
            dirty: false,
          },
          "Native binary build identity mismatch or dirty build",
        );
      if (
        !fixture &&
        target.os === process.platform &&
        target.cpu === process.arch
      ) {
        assert.equal(
          execFileSync(path.resolve(file), ["--version"], {
            encoding: "utf8",
            timeout: 10000,
            stdio: "pipe",
          }).trim(),
          `ccow ${version}`,
          "Native executable version mismatch",
        );
      }
      return [target.suffix, bytes];
    }),
  );
  mkdirSync(output, { recursive: false });
  const artifacts = [];
  const licenses = {
    LICENSE: regular(source("LICENSE")),
    NOTICE: regular(source("NOTICE")),
  };
  const emit = (name, files, kind, target) => {
    const bytes = archive(files);
    writeFileSync(path.join(output, name), bytes, { flag: "wx" });
    artifacts.push({
      name,
      kind,
      ...(target ? { target: target.suffix } : {}),
      sha256: sha256(bytes),
    });
  };
  for (const target of platforms.targets) {
    const bytes = binaryBytes.get(target.suffix);
    emit(
      `ccow-${version}-${target.suffix}.tar.gz`,
      {
        ...licenses,
        [target.binary]: { bytes, mode: 0o755 },
        "release.json": regular(
          json({ ...id, target: target.suffix, sha256: sha256(bytes) }),
        ),
      },
      "native",
      target,
    );
  }
  const release = { ...id, artifacts };
  writeFileSync(path.join(output, "release.json"), json(release), {
    flag: "wx",
  });
  writeFileSync(
    path.join(output, "SHA256SUMS"),
    artifacts.map((a) => `${a.sha256}  ${a.name}\n`).join(""),
    { flag: "wx" },
  );
  verifyRelease(output, id);
  return release;
}

// The candidate directory holds exactly the versioned native archives plus
// release.json and SHA256SUMS. Any other file, including a package tarball from
// the retired npm distribution, is rejected.
export function verifyRelease(directory, expected) {
  const release = JSON.parse(
    readFileSync(path.join(directory, "release.json")),
  );
  for (const key of ["version", "sourceRevision", "tag", "fixture"])
    assert.equal(release[key], expected[key], `Release ${key} mismatch`);
  assert.deepEqual(
    Object.keys(release).sort(),
    ["artifacts", "fixture", "sourceRevision", "tag", "version"],
    "Unexpected release metadata fields",
  );
  assert.equal(release.artifacts.length, platforms.targets.length);
  const expectedNames = new Set(
    platforms.targets.map((t) => `ccow-${version}-${t.suffix}.tar.gz`),
  );
  assert.deepEqual(
    readdirSync(directory).sort(),
    [...expectedNames, "SHA256SUMS", "release.json"].sort(),
    "Unexpected or missing candidate file",
  );
  for (const artifact of release.artifacts) {
    assert(
      expectedNames.delete(artifact.name),
      "Unexpected or duplicate artifact",
    );
    assert.equal(artifact.kind, "native", "Unknown artifact kind");
    const target = platforms.targets.find((t) => t.suffix === artifact.target);
    assert(target, "Native target missing");
    assert.equal(
      artifact.name,
      `ccow-${version}-${target.suffix}.tar.gz`,
      "Artifact identity mismatch",
    );
    const bytes = readFileSync(path.join(directory, artifact.name));
    assert.equal(sha256(bytes), artifact.sha256, "Artifact checksum mismatch");
    const files = entries(bytes);
    for (const file of ["LICENSE", "NOTICE"])
      assert.deepEqual(
        files.get(file)?.bytes,
        source(file),
        "License mismatch",
      );
    const meta = JSON.parse(files.get("release.json").bytes);
    assert.deepEqual(meta, {
      ...expected,
      target: target.suffix,
      sha256: sha256(files.get(target.binary).bytes),
    });
    assert.equal(files.get(target.binary).mode, 0o755);
    assert.equal(files.size, 4);
  }
  assert.equal(expectedNames.size, 0);
  assert.equal(
    readFileSync(path.join(directory, "SHA256SUMS"), "utf8"),
    release.artifacts.map((a) => `${a.sha256}  ${a.name}\n`).join(""),
    "Checksum list mismatch",
  );
  return release;
}

if (
  process.argv[1] &&
  path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  const { values } = parseArgs({
    options: {
      binaries: { type: "string" },
      output: { type: "string" },
      revision: { type: "string" },
      fixture: { type: "boolean", default: false },
    },
  });
  assert(
    values.binaries && values.output,
    "Usage: --binaries <directory> --output <new-directory> [--revision <sha>] [--fixture]",
  );
  const result = packageRelease({
    binaries: Object.fromEntries(
      platforms.targets.map((t) => [
        t.suffix,
        path.resolve(values.binaries, t.suffix, t.binary),
      ]),
    ),
    output: path.resolve(values.output),
    sourceRevision: values.revision ?? revision(),
    fixture: values.fixture,
  });
  console.log(JSON.stringify(result));
}
