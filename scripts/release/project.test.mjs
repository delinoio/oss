import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdtempSync, mkdirSync, readFileSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { Bump, Project, Kind, bumpVersion, readVersion, versionChanges, sourceMetadata, git, prepareRelease, validateCommit, preflightVersion, pushReleaseTag, tagRevision, waitForWorkflow } from "./project.mjs";

const root = fileURLToPath(new URL("../..", import.meta.url));
const files = ["Cargo.lock", ...["binpm", "cargo-mono", "nodeup", "with-watch"].map((name) => `crates/${name}/Cargo.toml`), "cmds/derun/internal/version/version.go", "cmds/runmoor/internal/runmoor/types.go"];
const sources = Object.fromEntries(files.map((file) => [file, readFileSync(path.join(root, file), "utf8")]));
const read = (file) => sources[file];
const bot = { name: "delino-release-bot[bot]", email: "123+delino-release-bot[bot]@users.noreply.github.com" };
const revision = "1".repeat(40);
const identity = { project: Project.Binpm, revision, tag: "binpm@v1.2.3" };
const absent = async () => ({ status: 404 });

for (const project of Object.values(Project)) for (const bump of Object.values(Bump)) {
  test(`${project} ${bump} changes only the selected source and matching workspace lock entry`, () => {
    const plan = versionChanges(project, bump, read);
    assert.equal(plan.previous_version, readVersion(project, read));
    assert.equal(plan.version, bumpVersion(plan.previous_version, bump));
    const updated = { ...sources, ...plan.changes };
    for (const candidate of Object.values(Project)) assert.equal(readVersion(candidate, (file) => updated[file]), candidate === project ? plan.version : readVersion(candidate, read));
    assert.equal(Object.keys(plan.changes).length, plan.kind === Kind.Rust ? 2 : 1);
    if (plan.kind === Kind.Rust) {
      const before = sources["Cargo.lock"].split("[[package]]");
      const after = updated["Cargo.lock"].split("[[package]]");
      assert.equal(before.length, after.length);
      assert.equal(before.filter((section, i) => section !== after[i]).length, 1);
    }
  });
}

test("SemVer resets lower components, remains exact, and rejects overflow and arbitrary selectors", () => {
  assert.equal(bumpVersion("2.9.8", Bump.Patch), "2.9.9");
  assert.equal(bumpVersion("2.9.8", Bump.Minor), "2.10.0");
  assert.equal(bumpVersion("2.9.8", Bump.Major), "3.0.0");
  for (const value of ["v1.2.3", "01.2.3", "1.2", "1.2.3-rc.1", "1.2.3+build", "1.2.3\n", "1.2.$(touch injected)", "1.2.18446744073709551615"]) assert.throws(() => bumpVersion(value, Bump.Patch));
  for (const project of ["rustia", "devhud", "__proto__", "../../x"]) assert.throws(() => versionChanges(project, Bump.Patch, read));
  assert.throws(() => versionChanges(Project.Binpm, "pre", read));
});

test("Version planning rejects manifest/lock drift, duplicate or external entries before writes", () => {
  const entry = `name = "binpm"\nversion = "${readVersion(Project.Binpm, read)}"`;
  for (const lock of [
    sources["Cargo.lock"].replace(entry, 'name = "binpm"\nversion = "99.0.0"'),
    sources["Cargo.lock"] + `\n[[package]]\n${entry}\n`,
    sources["Cargo.lock"].replace(entry, entry + '\nsource = "registry+example"'),
    sources["Cargo.lock"].replace(entry, 'name = "another"\nversion = "0.1.0"'),
  ]) assert.throws(() => versionChanges(Project.Binpm, Bump.Patch, (file) => file === "Cargo.lock" ? lock : read(file)));
  assert.throws(() => versionChanges(Project.Binpm, Bump.Patch, (file) => file === "crates/binpm/Cargo.toml" ? '[package]\nname = "binpm"\nversion.workspace = true\n' : read(file)));
});

test("Downstream manual and tag metadata must agree with source before builds", () => {
  for (const project of Object.values(Project)) {
    const version = readVersion(project, read);
    const tag = `${project}@v${version}`;
    const input = { project, event: "workflow_dispatch", ref: "refs/heads/main", requestedVersion: version, requestedDryRun: "false" };
    assert.deepEqual(sourceMetadata(input, read), { version, tag, dry_run: "false" });
    assert.deepEqual(sourceMetadata({ ...input, event: "push", ref: `refs/tags/${tag}` }, read), { version, tag, dry_run: "false" });
    assert.equal(sourceMetadata({ ...input, ref: "refs/heads/topic", requestedDryRun: "true" }, read).dry_run, "true");
    for (const change of [{ requestedVersion: bumpVersion(version, Bump.Patch) }, { event: "pull_request" }, { ref: "refs/heads/topic" }, { requestedDryRun: "perhaps" }, { event: "push", ref: "refs/heads/main" }, { event: "push", ref: `refs/tags/other@v${version}` }]) assert.throws(() => sourceMetadata({ ...input, ...change }, read));
  }
});

function fixture(t) {
  const directory = mkdtempSync(path.join(tmpdir(), "selected-release-"));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const remote = path.join(directory, "remote.git");
  const checkout = path.join(directory, "checkout");
  const emptyTemplate = path.join(directory, "empty-template");
  mkdirSync(emptyTemplate);
  execFileSync("git", ["init", "--bare", "--initial-branch=main", `--template=${emptyTemplate}`, remote], { stdio: "pipe" });
  execFileSync("git", ["clone", "--template", emptyTemplate, remote, checkout], { stdio: "pipe" });
  git(checkout, ["config", "user.name", "Fixture"]);
  git(checkout, ["config", "user.email", "fixture@example.invalid"]);
  for (const [file, contents] of Object.entries(sources)) {
    mkdirSync(path.dirname(path.join(checkout, file)), { recursive: true });
    writeFileSync(path.join(checkout, file), contents);
  }
  git(checkout, ["add", "."]);
  git(checkout, ["commit", "-m", "fixture baseline"]);
  git(checkout, ["push", "origin", "HEAD:main"]);
  return { directory: checkout, remote, initial: git(checkout, ["rev-parse", "HEAD"]) };
}

for (const project of Object.values(Project)) test(`${project} commit journals and resumes the same run after main advances`, async (t) => {
  const fixtureState = fixture(t);
  const options = { directory: fixtureState.directory, project, bump: Bump.Minor, runId: "123", ...bot };
  const first = await prepareRelease(options);
  assert.equal(first.resumed, false);
  assert.equal(git(fixtureState.remote, ["rev-parse", "refs/heads/main"]), first.revision);
  assert.equal(git(fixtureState.directory, ["status", "--porcelain"]), "");
  writeFileSync(path.join(fixtureState.directory, "unrelated.txt"), "unrelated\n");
  git(fixtureState.directory, ["add", "unrelated.txt"]);
  git(fixtureState.directory, ["commit", "-m", "unrelated work"]);
  git(fixtureState.directory, ["push", "origin", "HEAD:main"]);
  const second = await prepareRelease(options);
  assert.equal(second.resumed, true);
  assert.equal(second.revision, first.revision);
  assert.equal(second.version, first.version);
  assert.throws(() => validateCommit(fixtureState.directory, first.revision, project, Bump.Patch, "123"), /journal/u);
  assert.throws(() => validateCommit(fixtureState.directory, first.revision, project, Bump.Minor, "456"), /journal/u);
});

test("A concurrent main push fails without rewriting remote history", async (t) => {
  const state = fixture(t);
  const competitor = path.join(path.dirname(state.remote), "competitor");
  execFileSync("git", ["clone", state.remote, competitor], { stdio: "pipe" });
  git(competitor, ["config", "user.name", "Fixture"]);
  git(competitor, ["config", "user.email", "fixture@example.invalid"]);
  let competingRevision;
  await assert.rejects(prepareRelease({ directory: state.directory, project: Project.Derun, bump: Bump.Patch, runId: "999", ...bot,
    preflight: async () => {
      writeFileSync(path.join(competitor, "other.txt"), "competing commit\n");
      git(competitor, ["add", "other.txt"]);
      git(competitor, ["commit", "-m", "concurrent push"]);
      git(competitor, ["push", "origin", "HEAD:main"]);
      competingRevision = git(competitor, ["rev-parse", "HEAD"]);
    },
  }), /Git -c failed/u);
  assert.equal(git(state.remote, ["rev-parse", "refs/heads/main"]), competingRevision);
});

test("Recovery rejects extra paths and hidden non-version changes", async (t) => {
  const state = fixture(t);
  const options = { directory: state.directory, project: Project.Derun, bump: Bump.Patch, runId: "1", ...bot };
  const first = await prepareRelease(options);
  const file = "cmds/derun/internal/version/version.go";
  writeFileSync(path.join(state.directory, file), readFileSync(path.join(state.directory, file), "utf8") + "\n// unexpected mutation\n");
  git(state.directory, ["add", file]);
  git(state.directory, ["commit", "--amend", "--no-edit"]);
  assert.throws(() => validateCommit(state.directory, git(state.directory, ["rev-parse", "HEAD"]), options.project, options.bump, "1"), /version-only/u);
  git(state.directory, ["checkout", "--detach", first.revision]);
  writeFileSync(path.join(state.directory, "other"), "other");
  git(state.directory, ["add", "other"]);
  git(state.directory, ["commit", "--amend", "--no-edit"]);
  assert.throws(() => validateCommit(state.directory, git(state.directory, ["rev-parse", "HEAD"]), options.project, options.bump, "1"), /unexpected paths/u);
});

test("Preflight rejects existing releases, tags and uncertain API results", async () => {
  await preflightVersion(identity, absent);
  for (const status of [200, 403, 500]) await assert.rejects(preflightVersion(identity, async (route) => route.includes("/git/") ? { status: 404 } : { status }));
  await assert.rejects(preflightVersion(identity, async () => ({ status: 200, body: { object: { type: "commit", sha: revision } } })), /already exists/u);
  await assert.rejects(tagRevision(identity.tag, async () => ({ status: 403 })), /ownership/u);
  assert.equal(await tagRevision(identity.tag, async (route) => ({ status: 200, body: { object: { type: route.includes("/git/tags/") ? "commit" : "tag", sha: revision } } })), revision);
});

test("Registry-success/tag-push interruption recovers only the same tag and commit", async (t) => {
  const state = fixture(t);
  const plan = await prepareRelease({ directory: state.directory, project: Project.Binpm, bump: Bump.Patch, runId: "22", ...bot });
  const request = async () => {
    const sha = git(state.remote, ["for-each-ref", "--format=%(objectname)", `refs/tags/${plan.tag}`]);
    return sha ? { status: 200, body: { object: { type: "commit", sha } } } : { status: 404 };
  };
  assert.equal((await pushReleaseTag({ directory: state.directory, identity: plan, request })).reused, false);
  assert.equal((await pushReleaseTag({ directory: state.directory, identity: plan, request })).reused, true);
  await assert.rejects(pushReleaseTag({ directory: state.directory, identity: { ...plan, revision: state.initial }, request }), /different commit/u);
  assert.equal(git(state.remote, ["tag", "--list"]), plan.tag);
});

function run(overrides = {}) {
  return { id: 5, run_attempt: 1, head_sha: revision, head_branch: "main", event: "push", path: ".github/workflows/CI.yml", status: "completed", conclusion: "success", ...overrides };
}
function ciRequest(runs = [run()], jobs = [{ name: "CI Result", status: "completed", conclusion: "success", head_sha: revision }]) {
  return async (route) => ({ status: 200, body: route.includes("/jobs?") ? { jobs } : { workflow_runs: runs } });
}

test("CI wait binds workflow, event, branch, SHA and aggregate result", async () => {
  assert.deepEqual(await waitForWorkflow({ identity, stage: "ci", request: ciRequest() }), { ci_url: "https://github.com/delinoio/oss/actions/runs/5" });
  for (const conclusion of ["failure", "cancelled", "skipped", "neutral", "timed_out", null]) {
    await assert.rejects(waitForWorkflow({ identity, stage: "ci", request: ciRequest([run({ conclusion })]) }), /did not succeed/u);
  }
  for (const jobs of [[], [{ name: "CI Result", conclusion: "success", status: "completed", head_sha: "2".repeat(40) }], [{ name: "CI Result", conclusion: "skipped", status: "completed", head_sha: revision }]]) {
    await assert.rejects(waitForWorkflow({ identity, stage: "ci", request: ciRequest([run()], jobs) }), /CI Result/u);
  }
  for (const wrong of [{ head_sha: "2".repeat(40) }, { head_branch: "topic" }, { event: "workflow_dispatch" }, { path: ".github/workflows/other.yml" }]) {
    let clock = 0;
    await assert.rejects(waitForWorkflow({ identity, stage: "ci", request: ciRequest([run(wrong)]), now: () => clock, delay: async () => { clock += 600001; } }), /did not start/u);
  }
});

test("Waits poll pending runs, prefer the newest matching run and have deadlines", async () => {
  let calls = 0;
  let clock = 0;
  const states = [];
  const request = async (route) => route.includes("/jobs?") ? ciRequest()(route) : { status: 200, body: { workflow_runs: [run({ id: 1, conclusion: "failure" }), run({ status: ++calls > 1 ? "completed" : "in_progress" })] } };
  await waitForWorkflow({ identity, stage: "ci", request, now: () => clock, delay: async () => { clock += 30000; }, report: (state) => states.push(state) });
  assert.equal(states.length, 2);
  clock = 0;
  await assert.rejects(waitForWorkflow({ identity, stage: "ci", request: ciRequest([run({ status: "in_progress" })]), now: () => clock, delay: async () => { clock += 30000; }, timeoutMs: 60000 }), /timed out/u);
  await assert.rejects(waitForWorkflow({ identity, stage: "ci", request: async () => ({ status: 403 }) }), /inspect/u);
});

test("Downstream success requires the correct tag and populated public stable release", async () => {
  for (const project of [Project.Binpm, Project.Runmoor]) {
    const plan = { ...identity, project, tag: `${project}@v1.2.3` };
    const request = async (route) => {
      if (route.includes("/actions/")) return { status: 200, body: { workflow_runs: [run({ head_branch: plan.tag, path: `.github/workflows/release-${project}.yml` })] } };
      if (route.includes("/git/")) return { status: 200, body: { object: { type: "commit", sha: revision } } };
      return { status: 200, body: { tag_name: plan.tag, draft: false, prerelease: false, assets: [{ id: 1 }] } };
    };
    const result = await waitForWorkflow({ identity: plan, stage: "release", request });
    assert.ok(result.release_url.endsWith(encodeURIComponent(plan.tag)));
    await assert.rejects(waitForWorkflow({ identity: plan, stage: "release", request: (route) => route.includes("/releases/") ? absent() : request(route) }), /public release/u);
    await assert.rejects(waitForWorkflow({ identity: plan, stage: "release", request: async (route) => {
      const response = await request(route);
      if (route.includes("/releases/")) response.body.prerelease = !response.body.prerelease;
      return response;
    } }), /channel/u);
  }
});
