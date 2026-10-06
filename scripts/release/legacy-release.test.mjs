import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { existsSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { createLegacyReleaseTag, Project, git, legacyReleaseMetadata, readVersion } from "./project.mjs";

const root = fileURLToPath(new URL("../..", import.meta.url));
const read = (file) => readFileSync(path.join(root, file), "utf8");
const projects = [Project.Binpm, Project.CargoMono, Project.Nodeup, Project.WithWatch, Project.Derun];
const build = "b".repeat(40);
const historical = "a".repeat(40);
const commit = (sha) => ({ status: 200, body: { object: { type: "commit", sha } } });
const input = (project, extra = {}) => ({ project, revision: build, event: "workflow_dispatch", ref: "refs/heads/main", requestedVersion: readVersion(project, read), requestedDryRun: "false", ...extra });

for (const project of projects) test(`${project} permits only absent or matching remote tags for manual and tag publication`, async () => {
  const source = input(project);
  const tag = `${project}@v${source.requestedVersion}`;
  for (const event of [source, { ...source, event: "push", ref: `refs/tags/${tag}` }]) {
    for (const result of [{ tag: { status: 404 }, published: { status: 404 }, releases: { status: 200, body: [] } }, { tag: commit(build) }]) {
      const routes = [];
      const plan = await legacyReleaseMetadata(event, read, async (route) => {
        routes.push(route);
        if (route.includes("/releases?")) return result.releases;
        if (route.includes("/releases/tags/")) return result.published;
        return result.tag;
      });
      const tagRoute = `/repos/delinoio/oss/git/ref/tags/${encodeURIComponent(tag)}`;
      const publishedRoute = `/repos/delinoio/oss/releases/tags/${encodeURIComponent(tag)}`;
      const listRoute = "/repos/delinoio/oss/releases?per_page=100&page=1";
      assert.deepEqual(routes, result.published ? [tagRoute, publishedRoute, listRoute] : [tagRoute]);
      assert.deepEqual(plan, { version: source.requestedVersion, tag, dry_run: "false", revision: build, target_commitish: build });
    }
    await assert.rejects(legacyReleaseMetadata(event, read, async () => commit(historical)), /different commit/u);
  }
});

test("orphaned GitHub releases block publication when their tag ref is absent", async () => {
  const tag = `${Project.Binpm}@v${readVersion(Project.Binpm, read)}`;
  const routes = [];
  await assert.rejects(legacyReleaseMetadata(input(Project.Binpm), read, async (route) => {
    routes.push(route);
    if (route.includes("/git/ref/")) return { status: 404 };
    return { status: 200, body: { id: 123, tag_name: tag } };
  }), /no verified tag target/u);
  assert.deepEqual(routes, [`/repos/delinoio/oss/git/ref/tags/${encodeURIComponent(tag)}`, `/repos/delinoio/oss/releases/tags/${encodeURIComponent(tag)}`]);
});

test("orphaned draft releases block publication when the published-release lookup is absent", async () => {
  const tag = `${Project.Binpm}@v${readVersion(Project.Binpm, read)}`;
  const routes = [];
  await assert.rejects(legacyReleaseMetadata(input(Project.Binpm), read, async (route) => {
    routes.push(route);
    if (route.includes("/git/ref/")) return { status: 404 };
    if (route.includes("/releases/tags/")) return { status: 404 };
    return { status: 200, body: [{ id: 123, tag_name: tag, draft: true }] };
  }), /no verified tag target/u);
  assert.deepEqual(routes, [`/repos/delinoio/oss/git/ref/tags/${encodeURIComponent(tag)}`, `/repos/delinoio/oss/releases/tags/${encodeURIComponent(tag)}`, "/repos/delinoio/oss/releases?per_page=100&page=1"]);
});

test("legacy publication creates an absent tag atomically and verifies the commit again", async () => {
  const tag = `${Project.Binpm}@v${readVersion(Project.Binpm, read)}`;
  const calls = [];
  const result = await createLegacyReleaseTag({ tag, revision: build, request: async (route, options) => {
    calls.push({ route, options });
    if (options?.method === "POST") return { status: 201, body: {} };
    return calls.length === 1 ? { status: 404 } : commit(build);
  } });
  assert.deepEqual(result, { tag, revision: build, target_commitish: build });
  assert.deepEqual(calls, [
    { route: `/repos/delinoio/oss/git/ref/tags/${encodeURIComponent(tag)}`, options: undefined },
    { route: "/repos/delinoio/oss/git/refs", options: { method: "POST", body: { ref: `refs/tags/${tag}`, sha: build } } },
    { route: `/repos/delinoio/oss/git/ref/tags/${encodeURIComponent(tag)}`, options: undefined },
  ]);
  await assert.rejects(createLegacyReleaseTag({ tag, revision: build, request: async (route, options) => options?.method === "POST" ? { status: 422 } : { status: 404 } }), /atomic creation/u);
});

test("historical tags and uncertain lookups stop all signing, publication and tap side effects", async () => {
  for (const request of [
    async () => commit(historical),
    async () => ({ status: 403 }),
    async () => ({ status: 500 }),
    async () => ({ status: 200, body: {} }),
    async () => { throw new Error("lookup unavailable"); },
  ]) {
    const calls = { signing: 0, release: 0, tap: 0 };
    await assert.rejects((async () => {
      await legacyReleaseMetadata(input(Project.CargoMono), read, request);
      calls.signing++;
      calls.release++;
      calls.tap++;
    })());
    assert.deepEqual(calls, { signing: 0, release: 0, tap: 0 });
  }
});

test("bounded annotated chains resolve commits and reject malformed, cyclic and unsupported targets", async () => {
  function chain(depth, final = { type: "commit", sha: build }, status = 200) {
    let calls = 0;
    return async () => ({ status: calls++ === 0 ? 200 : status, body: { object: calls <= depth ? { type: "tag", sha: calls.toString(16).padStart(40, "0") } : final } });
  }
  for (const depth of [1, 4]) {
    const plan = await legacyReleaseMetadata(input(Project.Nodeup), read, chain(depth));
    assert.equal(plan.target_commitish, build);
  }
  for (const request of [
    chain(5), chain(1, { type: "commit", sha: historical }),
    chain(1, { type: "tree", sha: build }), chain(1, { type: "blob", sha: build }),
    chain(1, { type: "commit", sha: "short" }), chain(1, null),
    chain(1, undefined, 404), chain(1, undefined, 500),
    async () => ({ status: 200, body: { object: { type: "tag", sha: "invalid" } } }),
    async () => ({ status: 200, body: { object: { type: "tag", sha: historical } } }),
  ]) await assert.rejects(legacyReleaseMetadata(input(Project.Nodeup), read, request));
});

test("absent-tag creation uses the build commit after mocked main advances", async () => {
  let main = build;
  const responses = [{ status: 404 }, { status: 404 }, { status: 200, body: [] }];
  const plan = await legacyReleaseMetadata(input(Project.Derun), read, async () => responses.shift() ?? { status: 404 });
  main = historical;
  const releaseRequest = { tag_name: plan.tag, target_commitish: plan.target_commitish };
  const createdTagCommit = releaseRequest.target_commitish ?? main;
  assert.equal(createdTagCommit, build);
  assert.notEqual(createdTagCommit, main);
  // A tag introduced during the build must be checked again before writes.
  await assert.rejects(legacyReleaseMetadata(input(Project.Derun), read, async () => commit(main)), /different commit/u);
});

test("development-ref dry runs validate source without remote lookup or write authority", async () => {
  for (const project of projects) {
    const plan = await legacyReleaseMetadata(input(project, { requestedDryRun: "true", ref: "refs/heads/development" }), read, async () => { throw new Error("Dry run must not access GitHub"); });
    assert.equal(plan.dry_run, "true");
    assert.equal(plan.revision, build);
  }
  for (const extra of [{ revision: "main" }, { revision: "b".repeat(39) }, { requestedVersion: "99.0.0" }, { ref: "refs/heads/development" }, { project: Project.Runmoor }]) {
    let lookedUp = false;
    await assert.rejects(legacyReleaseMetadata(input(Project.Binpm, extra), read, async () => { lookedUp = true; return { status: 404 }; }));
    assert.equal(lookedUp, false);
  }
});

test("actual workflow command pins the checkout and emits no outputs after a rejected lookup", (t) => {
  const directory = mkdtempSync(path.join(tmpdir(), "legacy-release-command-"));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const revision = git(root, ["rev-parse", "HEAD"]);
  let attempt = 0;
  const run = (result, extra = {}) => {
    const output = path.join(directory, `output-${attempt++}`);
    const responses = Array.isArray(result) ? result : [result];
    const preload = `const responses = ${JSON.stringify(responses)}; globalThis.fetch = async () => { const response = responses.shift() ?? responses.at(-1) ?? { status: 404 }; return { status: response.status, ok: response.status >= 200 && response.status < 300, json: async () => (response.body ?? null) }; };`;
    const command = spawnSync(process.execPath, ["--import", `data:text/javascript,${encodeURIComponent(preload)}`, "scripts/release/project.mjs", "legacy-source"], {
      cwd: root, encoding: "utf8",
      env: { PATH: process.env.PATH, GH_TOKEN: "fixture-only", GITHUB_OUTPUT: output, GITHUB_REPOSITORY: "delinoio/oss", GITHUB_SHA: revision, GITHUB_EVENT_NAME: "workflow_dispatch", GITHUB_REF: "refs/heads/main", RELEASE_PROJECT: Project.CargoMono, REQUESTED_VERSION: readVersion(Project.CargoMono, read), REQUESTED_DRY_RUN: "false", ...extra },
    });
    return { ...command, outputs: existsSync(output) ? readFileSync(output, "utf8") : "" };
  };
  const accepted = run([{ status: 404 }, { status: 404 }, { status: 200, body: [] }]);
  assert.equal(accepted.status, 0, accepted.stderr);
  assert.ok(accepted.outputs.includes(`target_commitish=${revision}\n`));
  assert.ok(accepted.outputs.includes(`revision=${revision}\n`));
  assert.match(accepted.stderr, /"outcome":"verified"/u);
  const orphanedTag = `cargo-mono@v${readVersion(Project.CargoMono, read)}`;
  for (const rejected of [run(commit(historical)), run({ status: 500 }), run([{ status: 404 }, { status: 404 }, { status: 200, body: [{ id: 123, tag_name: orphanedTag, draft: true }] }]), run({ status: 404 }, { GITHUB_SHA: historical }), run({ status: 404 }, { GITHUB_REPOSITORY: "untrusted/oss" })]) {
    assert.equal(rejected.status, 1);
    assert.equal(rejected.outputs, "");
    assert.doesNotMatch(rejected.stderr, /fixture-only/u);
  }
  const dryRun = run({ status: 500 }, { REQUESTED_DRY_RUN: "true", GITHUB_REF: "refs/heads/development", GH_TOKEN: "" });
  assert.equal(dryRun.status, 0, dryRun.stderr);
});
