import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";
import { archive, archiveNames, assetManifest, checksums } from "./runmoor.mjs";
import { homebrewPlan, main, prepareHomebrew, publicationContext, publishedRelease } from "./runmoor-homebrew.mjs";

const root = new URL("../../", import.meta.url);
const revision = "a".repeat(40);
const plan = homebrewPlan({ version: "0.1.3", revision });
function temporary(t) {
  const directory = mkdtempSync(path.join(tmpdir(), "runmoor-homebrew-test-"));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  return directory;
}
function fixture(t) {
  const directory = temporary(t);
  for (const name of archiveNames) writeFileSync(path.join(directory, name), archive([
    { name: "runmoor", data: Buffer.from("executable fixture"), mode: 0o755 },
    { name: "README.md", data: Buffer.from("Runmoor"), mode: 0o644 },
    { name: "LICENSE", data: Buffer.from("Apache-2.0"), mode: 0o644 },
  ]));
  checksums(directory);
  for (const name of [...archiveNames, "SHA256SUMS"]) writeFileSync(path.join(directory, `${name}.sigstore.json`), JSON.stringify({ mediaType: "fixture", verificationMaterial: {}, messageSignature: {} }));
  const release = { id: 42, tag_name: plan.tag, target_commitish: revision, draft: false, prerelease: false, assets: assetManifest(directory, true).map((asset) => ({ ...asset, state: "uploaded" })) };
  const responses = new Map([
    [`/git/ref/tags/${encodeURIComponent(plan.tag)}`, { object: { type: "commit", sha: revision } }],
    [`/contents/cmds/runmoor/internal/runmoor/types.go?ref=${revision}`, { encoding: "base64", content: Buffer.from('const Version = "0.1.3"').toString("base64") }],
    [`/releases/tags/${encodeURIComponent(plan.tag)}`, release],
  ]);
  const request = async (endpoint) => {
    const body = responses.get(endpoint.replace("/repos/delinoio/oss", ""));
    return { status: body ? 200 : 404, body };
  };
  const download = async (url) => {
    assert.ok(url.startsWith(`https://github.com/delinoio/oss/releases/download/${encodeURIComponent(plan.tag)}/`));
    return readFileSync(path.join(directory, url.split("/").at(-1)));
  };
  return { directory, release, responses, request, download };
}
function renderArgs(version = "0.1.3") {
  return ["scripts/release/update-homebrew.sh", "--project", "runmoor", "--version", version,
    "--darwin-arm64-url", `https://github.com/delinoio/oss/releases/download/runmoor@v${version}/runmoor-darwin-arm64.tar.gz`,
    "--darwin-arm64-sha256", "a".repeat(64)];
}

test("Homebrew verifies all public assets and signatures before rendering a macOS-only formula", async (t) => {
  const f = fixture(t); const verified = [];
  const formula = await prepareHomebrew(plan, temporary(t), { ...f, verifyBlob: (file, bundle, identity) => {
    verified.push(path.basename(file));
    assert.equal(bundle, `${file}.sigstore.json`);
    assert.deepEqual(identity, plan);
  } });
  assert.deepEqual(verified.sort(), [...archiveNames, "SHA256SUMS"].sort());
  assert.match(formula, /depends_on arch: :arm64/u);
  assert.match(formula, /depends_on macos: :sonoma/u);
  assert.match(formula, /license "Apache-2.0"/u);
  assert.match(formula, /prefix\.install "LICENSE"/u);
  assert.match(formula, /\(prefix\/"LICENSE"\)\.read/u);
  assert.match(formula, /runmoor version/u);
  assert.doesNotMatch(formula, /on_linux|darwin-amd64|service do|__VERSION__/u);
});

test("Homebrew rejects wrong tags, revisions, source versions, drafts and incomplete metadata", async (t) => {
  for (const mutation of [
    (f) => { f.release.tag_name = "runmoor@v0.1.2"; },
    (f) => { f.release.target_commitish = "b".repeat(40); },
    (f) => { f.release.draft = true; },
    (f) => { f.release.prerelease = true; },
    (f) => { f.release.assets.pop(); },
    (f) => { f.release.assets[1] = f.release.assets[0]; },
    (f) => { f.release.assets[0].digest = null; },
    (f) => { f.release.assets[0].state = "new"; },
    (f) => { f.responses.get(`/git/ref/tags/${encodeURIComponent(plan.tag)}`).object.sha = "b".repeat(40); },
    (f) => { f.responses.get(`/contents/cmds/runmoor/internal/runmoor/types.go?ref=${revision}`).content = Buffer.from('const Version = "0.1.2"').toString("base64"); },
  ]) {
    const f = fixture(t); mutation(f);
    await assert.rejects(publishedRelease(plan, f.request));
  }
  for (const status of [403, 404, 500]) await assert.rejects(publishedRelease(plan, async () => ({ status })), /RELEASE_LOOKUP_FAILED/u);
});

test("Homebrew resolves annotated tags and rejects changed downloads or invalid signatures", async (t) => {
  const f = fixture(t);
  f.responses.set(`/git/ref/tags/${encodeURIComponent(plan.tag)}`, { object: { type: "tag", sha: "b".repeat(40) } });
  f.responses.set(`/git/tags/${"b".repeat(40)}`, { object: { type: "commit", sha: revision } });
  assert.equal((await publishedRelease(plan, f.request)).id, 42);
  await assert.rejects(prepareHomebrew(plan, temporary(t), { ...f, download: async () => Buffer.from("tampered"), verifyBlob: () => assert.fail("must not verify changed bytes") }), /ASSET_DIGEST_MISMATCH/u);
  await assert.rejects(prepareHomebrew(plan, temporary(t), { ...f, verifyBlob: () => { throw new Error("signature mismatch"); } }), /signature mismatch/u);
});

test("Homebrew rendering rejects malformed inputs and unsupported platform URLs", () => {
  for (const [flag, value] of [["--version", "0.1.3-beta"], ["--darwin-arm64-url", "https://example.invalid/runmoor-darwin-arm64.tar.gz"], ["--darwin-arm64-sha256", "not-a-sha"]]) {
    const args = renderArgs(); args[args.indexOf(flag) + 1] = value;
    assert.notEqual(spawnSync("bash", [...args, "--dry-run"], { cwd: root }).status, 0);
  }
  assert.notEqual(spawnSync("bash", [...renderArgs(), "--linux-amd64-url", "https://example.invalid/linux", "--dry-run"], { cwd: root }).status, 0);
  assert.throws(() => homebrewPlan({ version: "0.1.3", revision: "0".repeat(40) }), /INVALID_REVISION/u);
});

test("Homebrew publication refuses local or unrelated branch contexts before network access", async () => {
  const args = ["publish", "--version", plan.version, "--revision", revision, "--output", "unused"];
  await assert.rejects(main(args, {}), /UNAUTHORIZED_PUBLICATION/u);
  await assert.rejects(main(args, { GITHUB_ACTIONS: "true", GITHUB_REPOSITORY: "delinoio/oss", GITHUB_REF: "refs/heads/feature", HOMEBREW_TAP_GH_TOKEN: "fixture" }), /UNAUTHORIZED_PUBLICATION/u);
  const tagged = { GITHUB_ACTIONS: "true", GITHUB_REPOSITORY: "delinoio/oss", GITHUB_REF: `refs/tags/${plan.tag}`, GITHUB_SHA: revision };
  publicationContext(plan, tagged);
  assert.throws(() => publicationContext(plan, { ...tagged, GITHUB_SHA: "b".repeat(40) }), /UNAUTHORIZED_PUBLICATION/u);
});

test("Homebrew tap retries are identical and cannot downgrade or replace a published version", (t) => {
  const directory = temporary(t);
  const remote = path.join(directory, "tap.git"); const checkout = path.join(directory, "tap");
  const git = (args, cwd = directory) => execFileSync("git", args, { cwd, encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] });
  git(["init", "--bare", "--initial-branch=main", remote]);
  git(["clone", remote, checkout]);
  git(["config", "user.name", "Fixture"], checkout); git(["config", "user.email", "fixture@example.invalid"], checkout);
  const formula = execFileSync("bash", [...renderArgs(), "--dry-run"], { cwd: root, encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] });
  mkdirSync(path.join(checkout, "Formula")); writeFileSync(path.join(checkout, "Formula/runmoor.rb"), formula);
  git(["add", "Formula/runmoor.rb"], checkout); git(["commit", "-m", "Runmoor fixture"], checkout); git(["push", "origin", "HEAD:main"], checkout);
  const before = git(["rev-parse", "main"], remote);
  const env = { ...process.env, GH_TOKEN: "fixture", HOMEBREW_TAP_GH_TOKEN: "fixture", GIT_CONFIG_COUNT: "1", GIT_CONFIG_KEY_0: `url.file://${remote}.insteadOf`, GIT_CONFIG_VALUE_0: "https://github.com/fixture/runmoor-tap.git" };
  const run = (args) => spawnSync("bash", [...args, "--tap-repo", "fixture/runmoor-tap"], { cwd: root, env, encoding: "utf8" });
  const same = run(renderArgs()); assert.equal(same.status, 0, same.stderr);
  const older = run(renderArgs("0.1.2")); assert.notEqual(older.status, 0); assert.match(older.stderr, /refusing runmoor Homebrew downgrade/u);
  const args = renderArgs(); args[args.indexOf("--darwin-arm64-sha256") + 1] = "b".repeat(64);
  const changed = run(args); assert.notEqual(changed.status, 0); assert.match(changed.stderr, /conflicting runmoor formula bytes/u);
  assert.equal(git(["rev-parse", "main"], remote), before);
});
