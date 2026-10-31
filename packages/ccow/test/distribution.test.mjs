import assert from "node:assert/strict";
import { test } from "node:test";
import {
  mkdtempSync,
  writeFileSync,
  readFileSync,
  mkdirSync,
  rmSync,
  symlinkSync,
  realpathSync,
} from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { execFileSync, spawnSync } from "node:child_process";
import { EventEmitter } from "node:events";
import { gzipSync, gunzipSync } from "node:zlib";
import platforms from "../src/platforms.cjs";
import launcher from "../src/launcher.cjs";
import { archive, entries } from "../scripts/archive.mjs";
import {
  packageRelease,
  verifyRelease,
  identity,
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
test("exact support matrix and unsupported platforms", () => {
  assert.deepEqual(
    platforms.targets.map((t) => t.suffix),
    ["darwin-arm64", "linux-x64", "windows-x64"],
  );
  assert.equal(platforms.selectTarget("darwin", "x64"), undefined);
  assert.throws(() => launcher.resolveBinary("unused", null), /Unsupported/);
});
test("fixture dry-run is labeled and private", () => {
  const result = dryRun();
  assert.equal(result.fixture, true);
  assert.equal(result.artifacts.length, 7);
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
test(
  "npm-installed fixture preserves arguments, cwd, binary streams, exit and signals",
  { skip: process.platform === "win32" },
  (t) => {
    const options = setup(t);
    packageRelease(options);
    const target = platforms.selectTarget();
    assert(target, "Test host is outside the supported matrix");
    const consumer = path.join(options.root, "consumer");
    mkdirSync(consumer);
    execFileSync(
      "npm",
      [
        "install",
        "--offline",
        "--ignore-scripts",
        "--no-audit",
        "--no-fund",
        "--package-lock=false",
        path.join(options.output, "delino-ccow-0.1.0.tgz"),
        path.join(options.output, `delino-ccow-${target.suffix}-0.1.0.tgz`),
      ],
      { cwd: consumer, stdio: "pipe" },
    );
    const bin = path.join(consumer, "node_modules/@delino/ccow/bin/ccow.cjs");
    const result = spawnSync(
      process.execPath,
      [bin, "space argument", "한글", "$(literal)", ""],
      { cwd: consumer, input: Buffer.from([0, 255, 10]) },
    );
    assert.equal(result.status, 37);
    assert.deepEqual(
      result.stdout,
      Buffer.concat([
        Buffer.from(
          `${realpathSync(consumer)}\nspace argument\n한글\n$(literal)\n\n`,
        ),
        Buffer.from([0, 255, 10]),
      ]),
    );
    assert.equal(result.stderr.toString(), "fixture stderr\n");
    assert.equal(
      spawnSync(process.execPath, [bin, "signal"], { cwd: consumer }).signal,
      "SIGTERM",
    );
    const main = path.join(consumer, "node_modules/@delino/ccow/package.json");
    const native = path.join(
      consumer,
      `node_modules/${target.name}/package.json`,
    );
    const meta = JSON.parse(readFileSync(native));
    writeFileSync(native, JSON.stringify({ ...meta, version: "0.0.0" }));
    assert.throws(() => launcher.resolveBinary(main), /identity/);
    writeFileSync(native, JSON.stringify(meta));
    writeFileSync(
      path.join(path.dirname(native), "bin", target.binary),
      "tampered",
    );
    assert.throws(() => launcher.resolveBinary(main), /checksum/);
  },
);
test("Windows console events are not translated into forced termination", async () => {
  const parent = Object.assign(new EventEmitter(), {
    versions: { node: "24.0.0" },
    platform: "win32",
  });
  const child = new EventEmitter();
  const promise = launcher.launch("native.exe", ["a b"], {
    parent,
    spawnChild(binary, args, options) {
      assert.equal(binary, "native.exe");
      assert.deepEqual(args, ["a b"]);
      assert.equal(options.stdio, "inherit");
      assert.equal(options.shell, false);
      return child;
    },
  });
  parent.emit("SIGINT");
  parent.emit("SIGBREAK");
  child.emit("exit", 37, null);
  assert.deepEqual(await promise, { code: 37, signal: null });
  assert.equal(parent.listenerCount("SIGINT"), 0);
});
test("Node baseline and process replacement use exact argv and environment", async () => {
  await assert.rejects(
    launcher.launch("native", [], { parent: { versions: { node: "22.0.0" } } }),
    /24/,
  );
  const env = { SAMPLE: "value" };
  await assert.rejects(
    launcher.launch("native", ["a b"], {
      parent: {
        versions: { node: "24.0.0" },
        platform: "linux",
        env,
        execve(binary, args, environment) {
          assert.equal(binary, "native");
          assert.deepEqual(args, ["native", "a b"]);
          assert.equal(environment, env);
          throw new Error("fixture exec");
        },
      },
    }),
    /fixture exec/,
  );
});
