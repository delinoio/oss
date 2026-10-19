// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import {
  mkdtempSync,
  mkdirSync,
  writeFileSync,
  readFileSync,
  rmSync,
} from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";
import yaml from "js-yaml";
import {
  configure,
  metadata,
  stage,
  platforms,
  version,
  validateSource,
} from "./fixkey.mjs";
const revision = "a".repeat(40),
  key = "fixture-public-key";
function fixture(t) {
  const dir = mkdtempSync(path.join(tmpdir(), "fixkey-release-"));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  return dir;
}
function bundles(t) {
  const dir = fixture(t),
    out = path.join(dir, "assets");
  for (const [platform, spec] of Object.entries(platforms)) {
    const bundle = path.join(dir, platform);
    mkdirSync(bundle);
    for (const suffix of new Set([spec.package, spec.bundle]))
      writeFileSync(
        path.join(bundle, `FixKey_0.0.1${suffix}`),
        `fixture-${platform}-${suffix}`,
      );
    writeFileSync(
      path.join(bundle, `FixKey_0.0.1${spec.bundle}.sig`),
      Buffer.from(
        "untrusted comment: test fixture, not a signed payload\nfixture\ntrusted comment: timestamp:0\tfile:fixture\tversion:0.0.1\nfixture",
      ).toString("base64"),
    );
    stage(bundle, out, platform, "0.0.1", revision, key);
  }
  return out;
}
test("updater configuration requires signature-bound versions and exact Windows signing identity", (t) => {
  const dir = fixture(t),
    file = path.join(dir, "config.json");
  configure(file, "macos-arm64", key);
  assert.deepEqual(JSON.parse(readFileSync(file, "utf8")).plugins.updater, {
    pubkey: key,
    requireSignedVersion: true,
    allowDowngrades: false,
  });
  assert.throws(
    () => configure(file, "windows-x64", key, ""),
    /certificate-invalid/u,
  );
  configure(file, "windows-x64", key, "a".repeat(40));
  assert.equal(
    JSON.parse(readFileSync(file, "utf8")).bundle.windows.certificateThumbprint,
    "a".repeat(40),
  );
  const config = JSON.parse(
    readFileSync(
      new URL("../../apps/fixkey/src-tauri/tauri.conf.json", import.meta.url),
      "utf8",
    ),
  );
  assert.equal(config.plugins.updater.requireSignedVersion, true);
  assert.equal(config.plugins.updater.allowDowngrades, false);
});
test("metadata binds every platform to the exact source, key and payload hashes", (t) => {
  const out = bundles(t);
  const result = metadata(out, "0.0.1", revision, key);
  assert.deepEqual(Object.keys(result.platforms).sort(), [
    "darwin-aarch64",
    "linux-x86_64",
    "windows-x86_64",
  ]);
  assert.match(
    result.platforms["darwin-aarch64"].url,
    /\/fixkey-v0\.0\.1\/FixKey-macos-arm64.app.tar.gz$/u,
  );
  assert.match(
    readFileSync(path.join(out, "SHA256SUMS"), "utf8"),
    /fixkey-latest.json/u,
  );
  assert.throws(
    () => metadata(out, "0.0.1", "b".repeat(40), key),
    /identity-mismatch/u,
  );
  assert.throws(
    () => metadata(out, "0.0.1", revision, "wrong-key"),
    /identity-mismatch/u,
  );
  writeFileSync(path.join(out, platforms["windows-x64"].payload), "tampered");
  assert.throws(
    () => metadata(out, "0.0.1", revision, key),
    /checksum-mismatch/u,
  );
});
test("missing platforms, signatures and ambiguous bundles fail closed", (t) => {
  const dir = fixture(t);
  assert.throws(() => metadata(dir, "0.0.1", revision, key));
  writeFileSync(path.join(dir, "a.AppImage"), "fixture");
  assert.throws(
    () =>
      stage(
        dir,
        path.join(dir, "out"),
        "linux-x11-x64",
        "0.0.1",
        revision,
        key,
      ),
    /signed-payload-missing/u,
  );
  writeFileSync(path.join(dir, "b.AppImage"), "fixture");
  assert.throws(
    () =>
      stage(
        dir,
        path.join(dir, "out"),
        "linux-x11-x64",
        "0.0.1",
        revision,
        key,
      ),
    /ambiguous/u,
  );
  for (const invalid of ["1.0.0-beta.1", "01.0.0", "1.0.0;touch injected"])
    assert.throws(() => version(invalid));
});
test("source versions stay synchronized", () =>
  validateSource(path.resolve(import.meta.dirname, "../.."), "0.0.1"));
test("release signing and publication stay behind explicit main and native-acceptance gates", () => {
  const workflow = yaml.load(
    readFileSync(
      new URL("../../.github/workflows/release-fixkey.yml", import.meta.url),
      "utf8",
    ),
  );
  assert.deepEqual(Object.keys(workflow.on), ["workflow_dispatch"]);
  assert.match(workflow.jobs.prepare.if, /refs\/heads\/main/u);
  assert.equal(workflow.jobs.build.environment, "fixkey-signing");
  assert.equal(workflow.jobs.publish.environment, "fixkey-publish");
  assert.equal(workflow.jobs.publish.permissions.contents, "write");
  assert.match(
    workflow.jobs.prepare.steps.map((step) => step.run ?? "").join("\n"),
    /FIXKEY_NATIVE_ACCEPTANCE_REVISION/u,
  );
  assert.equal(
    workflow.jobs.build.strategy.matrix.include.find(
      (row) => row.platform === "macos-arm64",
    ).os,
    "macos-14",
  );
});
