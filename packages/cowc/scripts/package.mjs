import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { readFileSync, writeFileSync, mkdirSync, lstatSync } from "node:fs";
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
const launcherFiles = [
  "bin/cowc.cjs",
  "src/launcher.cjs",
  "src/platforms.cjs",
  "README.md",
];
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
  return { version, sourceRevision, fixture, tag: `cowcache-v${version}` };
}
function manifest(id, target, digest) {
  return {
    name: target?.name ?? "@delino/cowc",
    version: id.version,
    license: "Apache-2.0",
    gitHead: id.sourceRevision,
    description: id.fixture
      ? "TEST FIXTURE ONLY: not a CowCache release"
      : "CowCache native CLI",
    ...(id.fixture
      ? { private: true }
      : { publishConfig: { access: "public" } }),
    repository: {
      type: "git",
      url: "git+https://github.com/delinoio/oss.git",
      directory: "packages/cowc",
    },
    cowc: {
      fixture: id.fixture,
      ...(target ? { target: target.suffix, sha256: digest } : {}),
    },
    ...(target
      ? { os: [target.os], cpu: [target.cpu] }
      : {
          engines: { node: ">=24" },
          bin: { cowc: "bin/cowc.cjs" },
          optionalDependencies: Object.fromEntries(
            platforms.targets.map((t) => [t.name, id.version]),
          ),
        }),
  };
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
      if (!fixture) {
        const build = execFileSync("go", ["version", "-m", file], {
          encoding: "utf8",
          stdio: "pipe",
        });
        assert(
          build.includes(`GOOS=${target.goos}`) &&
            build.includes(`GOARCH=${target.goarch}`),
          "Go binary target mismatch",
        );
        assert(
          build.includes(`-X main.version=${version}`),
          "Go binary version linker identity mismatch",
        );
        assert(
          build.includes(`vcs.revision=${sourceRevision}`) &&
            build.includes("vcs.modified=false"),
          "Go binary source revision mismatch or dirty build",
        );
      }
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
          `cowc ${version}`,
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
    const meta = manifest(id, target, sha256(bytes));
    const files = {
      ...licenses,
      "package.json": regular(json(meta)),
      [`bin/${target.binary}`]: { bytes, mode: 0o755 },
    };
    emit(
      `delino-cowc-${target.suffix}-${version}.tgz`,
      Object.fromEntries(
        Object.entries(files).map(([key, value]) => [`package/${key}`, value]),
      ),
      "npm",
      target,
    );
    emit(
      `cowc-${version}-${target.suffix}.tar.gz`,
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
  const main = { ...licenses, "package.json": regular(json(manifest(id))) };
  for (const file of launcherFiles)
    main[file] = {
      bytes: readFileSync(path.join(packageRoot, file)),
      mode: file.startsWith("bin/") ? 0o755 : 0o644,
    };
  emit(
    `delino-cowc-${version}.tgz`,
    Object.fromEntries(
      Object.entries(main).map(([key, value]) => [`package/${key}`, value]),
    ),
    "npm",
  );
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

export function verifyRelease(directory, expected) {
  const release = JSON.parse(
    readFileSync(path.join(directory, "release.json")),
  );
  for (const key of ["version", "sourceRevision", "tag", "fixture"])
    assert.equal(release[key], expected[key], `Release ${key} mismatch`);
  assert.equal(release.artifacts.length, 7);
  const binaryDigests = new Map();
  const expectedNames = new Set([
    `delino-cowc-${version}.tgz`,
    ...platforms.targets.flatMap((t) => [
      `delino-cowc-${t.suffix}-${version}.tgz`,
      `cowc-${version}-${t.suffix}.tar.gz`,
    ]),
  ]);
  for (const artifact of release.artifacts) {
    assert(
      expectedNames.delete(artifact.name),
      "Unexpected or duplicate artifact",
    );
    const bytes = readFileSync(path.join(directory, artifact.name));
    assert.equal(sha256(bytes), artifact.sha256, "Artifact checksum mismatch");
    const files = entries(bytes);
    const target = platforms.targets.find((t) => t.suffix === artifact.target);
    const native = artifact.kind === "native";
    assert.equal(
      artifact.name,
      native
        ? `cowc-${version}-${target?.suffix}.tar.gz`
        : target
          ? `delino-cowc-${target.suffix}-${version}.tgz`
          : `delino-cowc-${version}.tgz`,
      "Artifact identity mismatch",
    );
    assert(native || artifact.kind === "npm", "Unknown artifact kind");
    assert(!native || target, "Native target missing");
    const prefix = native ? "" : "package/";
    if (target) {
      const digest = sha256(
        files.get(native ? target.binary : `package/bin/${target.binary}`)
          .bytes,
      );
      if (binaryDigests.has(target.suffix))
        assert.equal(
          digest,
          binaryDigests.get(target.suffix),
          "Native/npm executable mismatch",
        );
      binaryDigests.set(target.suffix, digest);
    }
    for (const file of ["LICENSE", "NOTICE"])
      assert.deepEqual(
        files.get(prefix + file)?.bytes,
        source(file),
        "License mismatch",
      );
    if (native) {
      const meta = JSON.parse(files.get("release.json").bytes);
      assert.deepEqual(meta, {
        ...expected,
        target: target.suffix,
        sha256: sha256(files.get(target.binary).bytes),
      });
      assert.equal(files.get(target.binary).mode, 0o755);
      assert.equal(files.size, 4);
    } else {
      const digest = target
        ? sha256(files.get(`package/bin/${target.binary}`).bytes)
        : undefined;
      assert.deepEqual(
        JSON.parse(files.get("package/package.json").bytes),
        manifest(expected, target, digest),
      );
      assert.equal(files.size, target ? 4 : 7);
      if (target)
        assert.equal(files.get(`package/bin/${target.binary}`).mode, 0o755);
      else
        for (const file of launcherFiles)
          assert.deepEqual(
            files.get(`package/${file}`)?.bytes,
            readFileSync(path.join(packageRoot, file)),
          );
    }
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
