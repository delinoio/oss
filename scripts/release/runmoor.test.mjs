import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdtempSync, readFileSync, writeFileSync, readdirSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";
import { readVersion, Project, Bump, bumpVersion } from "./project.mjs";
import { archive, archiveNames, assetManifest, checkPublication, checksums, githubRequest, inspectArchive, publishDraft, releasePlan, verify } from "./runmoor.mjs";

const sourceVersion = readVersion(Project.Runmoor);
const nextVersion = bumpVersion(sourceVersion, Bump.Patch);
const sourceTag = `runmoor@v${sourceVersion}`;
const revision = "1".repeat(40);
const entries = [
  { name: "runmoor", data: Buffer.from("test executable fixture"), mode: 0o755 },
  { name: "README.md", data: Buffer.from("stable release"), mode: 0o644 },
  { name: "LICENSE", data: Buffer.from("license"), mode: 0o644 },
];
test("Runmoor publication binds source version, revision, ref and stable release status", () => {
  const plan = releasePlan({ version: sourceVersion, revision, ref: "refs/heads/topic" });
  assert.equal(plan.mode, "dry-run"); assert.equal(plan.prerelease, false);
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

test("Release asset manifests bind exact names, sizes and digests", () => {
  const directory = mkdtempSync(path.join(tmpdir(), "runmoor-asset-manifest-test-"));
  try {
    const bytes = archive(entries);
    for (const name of archiveNames) writeFileSync(path.join(directory, name), bytes);
    writeFileSync(path.join(directory, "SHA256SUMS"), "checksums\n");
    const manifest = assetManifest(directory);
    assert.deepEqual(manifest, [...archiveNames, "SHA256SUMS"].map((name) => {
      const data = readFileSync(path.join(directory, name));
      return { name, size: data.length, digest: `sha256:${createHash("sha256").update(data).digest("hex")}` };
    }));
    for (const name of [...archiveNames, "SHA256SUMS"]) writeFileSync(path.join(directory, `${name}.sigstore.json`), "signature\n");
    assert.deepEqual(assetManifest(directory, true).map(({ name }) => name), [...archiveNames, "SHA256SUMS", ...archiveNames.map((name) => `${name}.sigstore.json`), "SHA256SUMS.sigstore.json"]);
    writeFileSync(path.join(directory, "unexpected"), "stale\n");
    assert.throws(() => assetManifest(directory), /Unexpected files/u);
    assert.throws(() => assetManifest(directory, true), /Unexpected files/u);
  } finally { rmSync(directory, { recursive: true, force: true }); }
});


const publicationPlan = () => releasePlan({ version: sourceVersion, revision, ref: "refs/heads/main", mode: "publish" });
const expectedAssets = [...archiveNames, "SHA256SUMS"].flatMap((name, index) => [
  { name, size: index + 1, digest: `sha256:${String(index + 1).repeat(64)}` },
  { name: `${name}.sigstore.json`, size: index + 11, digest: `sha256:${String(index + 5).repeat(64)}` },
]);
const draftFixture = (fields = {}) => ({ id: 42, tag_name: sourceTag, draft: true, prerelease: false, target_commitish: revision,
  assets: expectedAssets.map((asset) => ({ ...asset, state: "uploaded" })), ...fields });
const prefix = "/repos/delinoio/oss";
function remoteFixture({ draft = draftFixture(), pages, overrides = {} } = {}) {
  const calls = [];
  const request = async (route, options) => {
    calls.push({ route, options });
    if (route in overrides) return typeof overrides[route] === "function" ? overrides[route](options) : overrides[route];
    if (route === `${prefix}/git/ref/tags/${encodeURIComponent(sourceTag)}`) return { status: 200, body: { object: { type: "commit", sha: revision } } };
    // This endpoint deliberately cannot return a draft, matching GitHub's API.
    if (route === `${prefix}/releases/tags/${encodeURIComponent(sourceTag)}`) return { status: 404, body: {} };
    const page = route.match(/\/releases\?per_page=100&page=(\d+)$/u);
    if (page) return { status: 200, body: pages ? pages[Number(page[1]) - 1] ?? [] : draft ? [draft] : [] };
    if (route === `${prefix}/releases/42`) {
      if (options?.method === "PATCH") return { status: 200, body: { ...draft, draft: false, prerelease: false, body: options.body.body } };
      return { status: draft ? 200 : 404, body: draft ?? {} };
    }
    throw new Error(`Unexpected fixture route: ${route}`);
  };
  return { calls, request };
}

test("Publication discovers drafts through the list and revalidates their numeric ID", async () => {
  const { request, calls } = remoteFixture();
  const release = await checkPublication(publicationPlan(), request, expectedAssets);
  assert.equal(release.id, 42);
  assert.deepEqual(calls.map(({ route }) => route), [
    `${prefix}/git/ref/tags/${encodeURIComponent(sourceTag)}`,
    `${prefix}/releases?per_page=100&page=1`, `${prefix}/releases/42`,
  ]);
  const missing = remoteFixture({ draft: null });
  assert.equal(await checkPublication(publicationPlan(), missing.request), null);
  // A missing pinned draft must never fall back to discovery or authorize recreation.
  await assert.rejects(checkPublication(publicationPlan(), missing.request, undefined, 42), { code: "DRAFT_LOOKUP_FAILED" });
});

test("Publication scans later pages and rejects ambiguous same-tag releases", async () => {
  const unrelated = Array.from({ length: 100 }, (_, id) => draftFixture({ id: id + 100, tag_name: `other@v${id}` }));
  const remote = remoteFixture({ pages: [unrelated, [draftFixture()]] });
  assert.equal((await checkPublication(publicationPlan(), remote.request)).id, 42);
  assert.ok(remote.calls.some(({ route }) => route.endsWith("page=2")));
  const duplicate = remoteFixture({ pages: [[draftFixture(), ...unrelated.slice(1)], [draftFixture({ id: 43 })]] });
  await assert.rejects(checkPublication(publicationPlan(), duplicate.request), { code: "DUPLICATE_RELEASE" });
  const laterFailure = remoteFixture({ pages: [unrelated], overrides: { [`${prefix}/releases?per_page=100&page=2`]: { status: 500, body: {} } } });
  await assert.rejects(checkPublication(publicationPlan(), laterFailure.request), { code: "RELEASE_LIST_FAILED" });
});

test("Publication fails closed on remote failures, malformed lists and conflicting tags", async () => {
  for (const status of [401, 403, 404, 429, 500]) {
    const remote = remoteFixture({ overrides: { [`${prefix}/releases?per_page=100&page=1`]: { status, body: {} } } });
    await assert.rejects(checkPublication(publicationPlan(), remote.request), { code: "RELEASE_LIST_FAILED" });
  }
  for (const body of [{}, [null], [{}], [draftFixture({ id: "42" })]]) {
    const malformed = remoteFixture({ overrides: { [`${prefix}/releases?per_page=100&page=1`]: { status: 200, body } } });
    await assert.rejects(checkPublication(publicationPlan(), malformed.request), { code: "RELEASE_LIST_FAILED" });
  }
  for (const response of [
    { status: 403, body: {} },
    { status: 200, body: { object: { type: "commit", sha: "2".repeat(40) } } },
  ]) {
    const remote = remoteFixture({ overrides: { [`${prefix}/git/ref/tags/${encodeURIComponent(sourceTag)}`]: response } });
    await assert.rejects(checkPublication(publicationPlan(), remote.request));
    assert.equal(remote.calls.length, 1);
  }
  const remote = remoteFixture();
  await assert.rejects(checkPublication({ ...publicationPlan(), mode: "dry-run" }, remote.request), /dry-run/u);
  assert.equal(remote.calls.length, 0);
});

test("Draft lookup rejects changed identity, source, channel and published state", async () => {
  for (const fields of [
    { id: 43 }, { id: undefined }, { id: "42" }, { draft: false },
    { tag_name: "another@v1.0.0" }, { prerelease: true }, { target_commitish: "2".repeat(40) },
  ]) {
    const remote = remoteFixture({ overrides: { [`${prefix}/releases/42`]: { status: 200, body: draftFixture(fields) } } });
    await assert.rejects(checkPublication(publicationPlan(), remote.request, undefined, 42));
    assert.ok(remote.calls.every(({ route }) => !route.includes("/releases?")));
  }
  const published = remoteFixture({ draft: draftFixture({ draft: false }) });
  await assert.rejects(checkPublication(publicationPlan(), published.request), { code: "ALREADY_PUBLIC" });
  for (const id of [0, -1, NaN, "42"]) {
    await assert.rejects(checkPublication(publicationPlan(), remoteFixture().request, undefined, id), { code: "INVALID_RELEASE_ID" });
  }
});

test("Final draft verification rejects missing, duplicate, altered and unfinished assets", async () => {
  const assets = draftFixture().assets;
  for (const changed of [
    assets.slice(0, -1), [...assets.slice(1), assets[1]], [...assets, { name: "stale" }],
    assets.map((asset, i) => i === 0 ? { ...asset, size: asset.size + 1 } : asset),
    assets.map((asset, i) => i === 0 ? { ...asset, digest: `sha256:${"f".repeat(64)}` } : asset),
    assets.map((asset, i) => i === 0 ? { ...asset, state: "starter" } : asset),
  ]) {
    const remote = remoteFixture({ draft: draftFixture({ assets: changed }) });
    await assert.rejects(checkPublication(publicationPlan(), remote.request, expectedAssets, 42), { code: "ASSET_MISMATCH" });
  }
});

test("Publication patches only the verified release ID after final revalidation", async () => {
  const remote = remoteFixture();
  const result = await publishDraft(publicationPlan(), remote.request, { releaseId: 42, expectedAssets, notes: "Canonical notes" });
  assert.equal(result.draft, false);
  assert.deepEqual(remote.calls.at(-1), { route: `${prefix}/releases/42`, options: {
    method: "PATCH", body: { draft: false, prerelease: false, body: "Canonical notes" },
  } });
  assert.deepEqual(remote.calls.slice(0, -1).map(({ route }) => route), [
    `${prefix}/git/ref/tags/${encodeURIComponent(sourceTag)}`, `${prefix}/releases/42`,
  ]);
  for (const fields of [{ draft: false }, { id: 43 }, { assets: [] }, { target_commitish: "2".repeat(40) }]) {
    const changed = remoteFixture({ draft: draftFixture(fields) });
    await assert.rejects(publishDraft(publicationPlan(), changed.request, { releaseId: 42, expectedAssets, notes: "notes" }));
    assert.ok(changed.calls.every(({ options }) => !options));
  }
  for (const manifest of [undefined, [], expectedAssets.slice(1)]) {
    const missing = remoteFixture();
    await assert.rejects(publishDraft(publicationPlan(), missing.request, { releaseId: 42, expectedAssets: manifest, notes: "notes" }));
    assert.equal(missing.calls.length, 0);
  }
  const missingId = remoteFixture();
  await assert.rejects(publishDraft(publicationPlan(), missingId.request, { expectedAssets, notes: "notes" }), { code: "INVALID_RELEASE_ID" });
  assert.equal(missingId.calls.length, 0);
});

test("An uncertain publication outcome is reported without retrying the write", async () => {
  for (const response of [
    { status: 500, body: {} }, { status: 200, body: draftFixture() },
    { status: 200, body: draftFixture({ id: 43, draft: false }) },
  ]) {
    const remote = remoteFixture({ overrides: { [`${prefix}/releases/42`]: (options) => options?.method === "PATCH" ? response : { status: 200, body: draftFixture() } } });
    await assert.rejects(publishDraft(publicationPlan(), remote.request, { releaseId: 42, expectedAssets, notes: "notes" }), { code: "PUBLICATION_UNCONFIRMED" });
    assert.equal(remote.calls.filter(({ options }) => options?.method === "PATCH").length, 1);
  }
});

test("GitHub requests preserve status and redact transport and response parse failures", async () => {
  const request = githubRequest("test-token", async (url, options) => {
    assert.equal(url, `https://api.github.com${prefix}/releases/42`);
    assert.equal(options.redirect, "error");
    assert.equal(options.headers.Authorization, "Bearer test-token");
    assert.equal(options.method, "PATCH");
    assert.deepEqual(JSON.parse(options.body), { draft: false });
    return { status: 403, ok: false, json: () => { throw new Error("must not parse errors"); } };
  });
  assert.deepEqual(await request(`${prefix}/releases/42`, { method: "PATCH", body: { draft: false } }), { status: 403, body: {} });
  for (const fetchImpl of [
    async () => { throw new Error("secret upstream transport detail"); },
    async () => ({ status: 200, ok: true, json: () => { throw new Error("secret response"); } }),
  ]) await assert.rejects(githubRequest("test-token", fetchImpl)(`${prefix}/releases/42`), (error) => error.code === "REQUEST_FAILED" && !/secret|test-token/u.test(error.message));
});
