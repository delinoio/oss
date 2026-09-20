import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, writeFileSync, readdirSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";
import { readVersion, Project, Bump, bumpVersion } from "./project.mjs";
import { archive, archiveNames, checkPublication, checksums, inspectArchive, releasePlan, verify } from "./runmoor.mjs";

const sourceVersion = readVersion(Project.Runmoor);
const nextVersion = bumpVersion(sourceVersion, Bump.Patch);
const sourceTag = `runmoor@v${sourceVersion}`;
const revision = "1".repeat(40);
const entries = [
  { name: "runmoor", data: Buffer.from("test executable fixture"), mode: 0o755 },
  { name: "README.md", data: Buffer.from("preview"), mode: 0o644 },
  { name: "LICENSE", data: Buffer.from("license"), mode: 0o644 },
];
test("Runmoor publication binds source version, revision, ref and preview status", () => {
  const plan = releasePlan({ version: sourceVersion, revision, ref: "refs/heads/topic" });
  assert.equal(plan.mode, "dry-run"); assert.equal(plan.prerelease, true);
  assert.equal(plan.tag, sourceTag); assert.equal(plan.signatures.length, 4);
  for (const fields of [
    { version: `v${sourceVersion}` }, { version: nextVersion }, { revision: "HEAD" },
    { ref: "refs/tags/another@v0.1.0" }, { mode: "publish", ref: "refs/heads/topic" },
    { mode: "fake-sign" },
  ]) assert.throws(() => releasePlan({ version: sourceVersion, revision, ref: "refs/heads/main", ...fields }));
  for (const ref of ["refs/heads/main", `refs/tags/${sourceTag}`]) {
    assert.equal(releasePlan({ version: sourceVersion, revision, ref, mode: "publish" }).mode, "publish");
  }
});
test("Runmoor archives are deterministic with exact files, permissions and checksums", () => {
  const bytes = archive(entries);
  assert.deepEqual(bytes, archive(entries));
  assert.deepEqual(inspectArchive(bytes), ["runmoor", "README.md", "LICENSE"]);
  assert.throws(() => archive([{ ...entries[0], name: "../runmoor" }]));
  assert.throws(() => inspectArchive(archive([{ ...entries[0], mode: 0o644 }, ...entries.slice(1)])));
  assert.throws(() => inspectArchive(archive(entries.slice(1))));
  const dir = mkdtempSync(path.join(tmpdir(), "runmoor-release-test-"));
  try {
    for (const name of archiveNames) writeFileSync(path.join(dir, name), bytes);
    const sums = checksums(dir); verify(dir);
    assert.equal(sums.split("\n").filter(Boolean).length, 3);
    assert.deepEqual(readdirSync(dir).sort(), [...archiveNames, "SHA256SUMS"].sort());
    writeFileSync(path.join(dir, "SHA256SUMS"), sums.replace(/[0-9a-f]/u, "x"));
    assert.throws(() => verify(dir), /Checksum/u);
    writeFileSync(path.join(dir, "SHA256SUMS"), sums);
    writeFileSync(path.join(dir, "fake.sigstore.json"), "{}");
    assert.throws(() => verify(dir), /inventory/u);
  } finally { rmSync(dir, { recursive: true, force: true }); }
});
test("Signed verification invokes an injected verifier with exact artifact and identity binding", () => {
  const directory = mkdtempSync(path.join(tmpdir(), "runmoor-signature-fixture-"));
  const identity = `https://github.com/delinoio/oss/.github/workflows/release-runmoor.yml@refs/tags/${sourceTag}`;
  try {
    for (const name of archiveNames) writeFileSync(path.join(directory, name), archive(entries));
    checksums(directory);
    // Deliberately non-cryptographic fixtures remain in this temporary test directory only.
    for (const name of [...archiveNames, "SHA256SUMS"]) {
      writeFileSync(path.join(directory, `${name}.sigstore.json`), JSON.stringify({ mediaType: "test-only", verificationMaterial: {}, messageSignature: {} }));
    }
    const verified = [];
    const verifyBlob = (artifact, bundle, publisher) => {
      assert.equal(publisher, identity);
      assert.equal(bundle, `${artifact}.sigstore.json`);
      assert.ok(readFileSync(artifact).length > 0);
      verified.push(path.basename(artifact));
    };
    verify(directory, true, { identity, verifyBlob });
    assert.deepEqual(verified.sort(), [...archiveNames, "SHA256SUMS"].sort());
    assert.throws(() => verify(directory, true, { identity: "untrusted", verifyBlob }), /identity/u);
    assert.throws(() => verify(directory, true, { identity, verifyBlob: () => { throw new Error("signature mismatch"); } }), /signature mismatch/u);
    writeFileSync(path.join(directory, "SHA256SUMS"), "corrupt");
    let invoked = false;
    assert.throws(() => verify(directory, true, { identity, verifyBlob: () => { invoked = true; } }), /Checksum/u);
    assert.equal(invoked, false);
  } finally { rmSync(directory, { recursive: true, force: true }); }
});


test("Publication refuses conflicting tags, existing releases and uncertain API results", async () => {
  const plan = releasePlan({ version: sourceVersion, revision, ref: "refs/heads/main", mode: "publish" });
  await checkPublication(plan, async () => ({ status: 404, body: {} }));
  await checkPublication(plan, async (route) => ({ status: route.includes("/git/") ? 200 : 404, body: { object: { type: "commit", sha: revision } } }));
  for (const result of [
    { status: 403, body: {} },
    { status: 200, body: { object: { type: "commit", sha: "2".repeat(40) } } },
  ]) await assert.rejects(checkPublication(plan, async () => result));
  await assert.rejects(checkPublication(plan, async (route) => route.includes("/git/") ? { status: 404, body: {} } : { status: 200, body: {} }), /already exists/u);
  let invoked = false;
  await assert.rejects(checkPublication({ ...plan, mode: "dry-run" }, async () => { invoked = true; return {}; }), /dry-run/u);
  assert.equal(invoked, false);
});
