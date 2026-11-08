// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import test from "node:test";
import { archive } from "../../packages/ccow/scripts/archive.mjs";
import {
  identity,
  linuxArchiveName,
  sha256,
  validateRelease,
} from "./linux-packages/model.mjs";
import { extractCacheCowExecutable } from "./linux-packages/release-input.mjs";

const plan = identity({
  project: "ccow",
  version: "0.1.0",
  revision: "a".repeat(40),
});
const terms = {
  LICENSE: Buffer.from("license fixture"),
  NOTICE: Buffer.from("notice fixture"),
};
function fixture(arch, changes = {}) {
  const target = arch === "amd64" ? "x64" : "arm64";
  const info = {
    version: plan.version,
    revision: plan.revision,
    target:
      arch === "amd64"
        ? "x86_64-unknown-linux-gnu"
        : "aarch64-unknown-linux-gnu",
    dirty: false,
    ...changes.info,
  };
  const binary = Buffer.from(
    `ELF fixture\0ccow-build-info:${JSON.stringify(info)}\0`,
  );
  const metadata = {
    version: plan.version,
    sourceRevision: plan.revision,
    fixture: false,
    tag: plan.tag,
    target: `linux-${target}`,
    sha256: sha256(binary),
    ...changes.metadata,
  };
  const files = {
    LICENSE: { bytes: terms.LICENSE },
    NOTICE: { bytes: terms.NOTICE },
    ccow: { bytes: binary, mode: 0o755 },
    "release.json": { bytes: Buffer.from(JSON.stringify(metadata)) },
    ...changes.files,
  };
  return { bytes: archive(files), binary };
}
test("CacheCow Linux packages reuse both exact source-bound archive executables", () => {
  for (const arch of ["amd64", "arm64"]) {
    const f = fixture(arch);
    assert.deepEqual(
      extractCacheCowExecutable(f.bytes, plan, arch, terms),
      f.binary,
    );
    assert.throws(() =>
      extractCacheCowExecutable(
        f.bytes,
        plan,
        arch === "amd64" ? "arm64" : "amd64",
        terms,
      ),
    );
    for (const changes of [
      { metadata: { fixture: true } },
      { metadata: { sourceRevision: "b".repeat(40) } },
      { metadata: { version: "0.2.0" } },
      { metadata: { sha256: "0".repeat(64) } },
      { info: { dirty: true } },
      { info: { revision: "b".repeat(40) } },
      { files: { NOTICE: { bytes: Buffer.from("wrong") } } },
      { files: { extra: { bytes: Buffer.from("unrelated") } } },
    ])
      assert.throws(() =>
        extractCacheCowExecutable(
          fixture(arch, changes).bytes,
          plan,
          arch,
          terms,
        ),
      );
  }
});
test("CacheCow releases require versioned Linux names and both Sigstore bundles", () => {
  const names = [
    "SHA256SUMS",
    "SHA256SUMS.sigstore.json",
    ...["amd64", "arm64"].flatMap((arch) => [
      linuxArchiveName(plan, arch),
      `${linuxArchiveName(plan, arch)}.sigstore.json`,
    ]),
  ];
  const release = {
    tag_name: plan.tag,
    draft: false,
    prerelease: false,
    assets: names.map((name) => ({ name, size: 1 })),
  };
  assert.deepEqual(validateRelease(release, plan, plan.revision), names);
  assert.throws(() =>
    validateRelease(
      { ...release, assets: release.assets.slice(1) },
      plan,
      plan.revision,
    ),
  );
  assert.throws(() => validateRelease(release, plan, "b".repeat(40)));
});
