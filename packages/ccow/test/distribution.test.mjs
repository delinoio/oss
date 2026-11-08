import assert from "node:assert/strict";
import { test } from "node:test";
import {
  mkdtempSync,
  writeFileSync,
  readFileSync,
  readdirSync,
  rmSync,
  symlinkSync,
} from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { gzipSync, gunzipSync } from "node:zlib";
import platforms from "../src/platforms.cjs";
import { archive, entries } from "../scripts/archive.mjs";
import {
  packageRelease,
  verifyRelease,
  identity,
  buildInfo,
  sha256,
} from "../scripts/package.mjs";
import { dryRun } from "../scripts/release-dry-run.mjs";
const revision = "a".repeat(40);
const fixture = Buffer.from(`#!/bin/sh
# TEST FIXTURE ONLY: does not implement CacheCow.
if [ "$1" = "signal" ]; then kill -TERM $$; fi
printf '%s\\n' "$PWD" "$@"
printf 'fixture stderr\\n' >&2
cat
exit 37
`);
function setup(t) {
  const root = mkdtempSync(path.join(tmpdir(), "ccow test 한글 "));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const binaries = Object.fromEntries(
    platforms.targets.map((target) => {
      const binary = path.join(root, target.suffix);
      writeFileSync(binary, fixture, { mode: 0o755 });
      return [target.suffix, binary];
    }),
  );
  return {
    root,
    binaries,
    output: path.join(root, "artifacts"),
    sourceRevision: revision,
    fixture: true,
  };
}
test("exact native support matrix", () => {
  assert.deepEqual(
    platforms.targets.map((t) => t.suffix),
    ["darwin-arm64", "linux-x64", "linux-arm64", "windows-x64"],
  );
  assert.deepEqual(
    platforms.targets.map((t) => t.binary),
    ["ccow", "ccow", "ccow", "ccow.exe"],
  );
  assert(platforms.targets.every((t) => t.name === undefined));
});
test("native build identity is read from the embedded marker", () => {
  const info = {
    version: "0.1.0",
    revision,
    target: "x86_64-unknown-linux-gnu",
    dirty: false,
  };
  const marker = `ccow-build-info:${JSON.stringify(info)}`;
  const binary = Buffer.concat([
    Buffer.from("\x7fELF\0prefix\0"),
    Buffer.from(marker),
    Buffer.from("\0middle\0"),
    Buffer.from(marker),
    Buffer.from("\0suffix"),
  ]);
  assert.deepEqual(buildInfo(binary), info);
  assert.throws(() => buildInfo(Buffer.from("no identity")), /exactly one/);
  assert.throws(
    () =>
      buildInfo(
        Buffer.concat([
          Buffer.from(marker),
          Buffer.from("\0"),
          Buffer.from(marker.replace(revision, "b".repeat(40))),
        ]),
      ),
    /exactly one/,
  );
  assert.throws(
    () => buildInfo(Buffer.from('ccow-build-info:{"version":"0.1.0"}')),
    /fields/,
  );
  assert.throws(
    () => buildInfo(Buffer.from("ccow-build-info:x}")),
    /Malformed/,
  );
});
test("fixture dry-run is labeled and produces only native archives", () => {
  const result = dryRun();
  assert.equal(result.fixture, true);
  assert.equal(result.artifacts.length, 4);
  assert(result.artifacts.every((a) => a.kind === "native"));
});
test("candidates hold only native archives, SHA256SUMS and release.json", (t) => {
  const options = setup(t);
  const release = packageRelease(options);
  assert.deepEqual(
    readdirSync(options.output).sort(),
    [
      ...platforms.targets.map((x) => `ccow-0.1.0-${x.suffix}.tar.gz`),
      "SHA256SUMS",
      "release.json",
    ].sort(),
  );
  assert.deepEqual(Object.keys(release).sort(), [
    "artifacts",
    "fixture",
    "sourceRevision",
    "tag",
    "version",
  ]);
  assert.equal(
    readFileSync(path.join(options.output, "SHA256SUMS"), "utf8").split("\n")
      .length,
    5,
  );
  for (const artifact of release.artifacts) {
    const target = platforms.targets.find((x) => x.suffix === artifact.target);
    assert.deepEqual(
      [
        ...entries(
          readFileSync(path.join(options.output, artifact.name)),
        ).keys(),
      ].sort(),
      ["LICENSE", "NOTICE", "release.json", target.binary].sort(),
    );
  }
});
test("verification rejects npm leftovers in the candidate", (t) => {
  const options = setup(t);
  const release = packageRelease(options);
  const expected = identity(revision, true);
  const tarball = path.join(options.output, "delino-ccow-0.1.0.tgz");
  writeFileSync(tarball, "retired npm package");
  assert.throws(() => verifyRelease(options.output, expected), /Unexpected/);
  rmSync(tarball);
  const metadata = path.join(options.output, "release.json");
  writeFileSync(
    metadata,
    JSON.stringify({
      ...release,
      artifacts: [
        ...release.artifacts,
        {
          name: "delino-ccow-0.1.0.tgz",
          kind: "npm",
          sha256: "0".repeat(64),
        },
      ],
    }),
  );
  assert.throws(() => verifyRelease(options.output, expected));
  writeFileSync(metadata, JSON.stringify({ ...release, artifacts: [] }));
  assert.throws(() => verifyRelease(options.output, expected));
});
test("archives verify exact release identity, permissions, checksums and licenses", (t) => {
  const options = setup(t);
  const release = packageRelease(options);
  assert.deepEqual(
    verifyRelease(options.output, identity(revision, true)),
    release,
  );
  assert.throws(
    () => verifyRelease(options.output, identity("b".repeat(40), true)),
    /revision|Revision/,
  );
  assert.throws(
    () => verifyRelease(options.output, identity(revision, false)),
    /fixture/,
  );
  assert.throws(() => packageRelease(options), /EEXIST/);
  const tarball = path.join(options.output, release.artifacts[0].name);
  writeFileSync(tarball, Buffer.from("corrupted"));
  assert.throws(
    () => verifyRelease(options.output, identity(revision, true)),
    /checksum/,
  );
});
test("reject unsafe inputs and false native claims", (t) => {
  const options = setup(t);
  assert.throws(
    () => packageRelease({ ...options, sourceRevision: "../escape" }),
    /revision/,
  );
  assert.throws(
    () =>
      packageRelease({
        ...options,
        binaries: { ...options.binaries, extra: "bad" },
      }),
    /targets/,
  );
  assert.throws(() => packageRelease({ ...options, fixture: false }));
  if (process.platform !== "win32") {
    const link = path.join(options.root, "link");
    symlinkSync(options.binaries["linux-x64"], link);
    assert.throws(
      () =>
        packageRelease({
          ...options,
          binaries: { ...options.binaries, "linux-x64": link },
        }),
      /regular file/,
    );
  }
});
test("archive parser rejects traversal, links, duplicates, and damaged endings", () => {
  const valid = archive({ good: { bytes: Buffer.from("ok") } });
  const mutate = (fn) => {
    const bytes = gunzipSync(valid);
    fn(bytes);
    bytes.fill(32, 148, 156);
    bytes.write(
      bytes
        .subarray(0, 512)
        .reduce((a, b) => a + b, 0)
        .toString(8)
        .padStart(6, "0") + "\0 ",
      148,
    );
    return gzipSync(bytes);
  };
  assert.throws(
    () =>
      entries(
        mutate((b) => {
          b.fill(0, 0, 100);
          b.write("../escape");
        }),
      ),
    /path/,
  );
  assert.throws(
    () =>
      entries(
        mutate((b) => {
          b[156] = 50;
        }),
      ),
    /entry/,
  );
  const raw = gunzipSync(valid);
  assert.throws(
    () => entries(gzipSync(Buffer.concat([raw.subarray(0, 1024), raw]))),
    /entry/,
  );
  assert.throws(() => entries(gzipSync(raw.subarray(0, 1024))), /ending/);
});
