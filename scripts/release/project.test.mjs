import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdtempSync, mkdirSync, readFileSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { Bump, Project, Kind, bumpVersion, readVersion, versionChanges, sourceMetadata, git, prepareRelease, validateCommit, preflightVersion, pushReleaseTag, tagRevision, requiresCargoPublish, reactForgeVersionPublished } from "./project.mjs";

const root = fileURLToPath(new URL("../..", import.meta.url));
const achFiles = [
  "cmds/async-commit-hook/internal/core/model.go", "apps/async-commit-hook/package.json",
  "packages/async-commit-hook-api-client/package.json", "packaging/async-commit-hook/release-metadata.json",
];
const files = ["Cargo.lock", "packages/clibox/package.json", "packages/pnport/package.json", "packages/react-forge/package.json", ...["binpm", "cargo-mono", "nodeup", "with-watch", "clibox", "pnport", "pnport-core", "pnport-preload"].map((name) => `crates/${name}/Cargo.toml`), "cmds/derun/internal/version/version.go", "cmds/runmoor/internal/runmoor/types.go", ...achFiles];
const sources = Object.fromEntries(files.map((file) => [file, readFileSync(path.join(root, file), "utf8")]));
const read = (file) => sources[file];
const readReactForgeRecovery = (file) => file === "packages/react-forge/package.json"
  ? read(file).replace(/("version": ")[^"]+/u, (_, prefix) => `${prefix}0.1.0`)
  : read(file);
const bot = { name: "delino-release-bot[bot]", email: "123+delino-release-bot[bot]@users.noreply.github.com" };
const revision = "1".repeat(40);
const identity = { project: Project.Binpm, revision, tag: "binpm@v1.2.3" };
const absent = async () => ({ status: 404 });

for (const project of Object.values(Project)) for (const bump of Object.values(Bump)) {
  test(`${project} ${bump} changes only the selected version sources`, () => {
    if (project === Project.Pnport && bump !== Bump.Minor) {
      assert.throws(() => versionChanges(project, bump, read), /first public release requires a minor bump/u);
      return;
    }
    const plan = versionChanges(project, bump, read);
    assert.equal(plan.previous_version, readVersion(project, read));
    assert.equal(plan.version, bumpVersion(plan.previous_version, bump));
    const updated = { ...sources, ...plan.changes };
    for (const candidate of Object.values(Project)) assert.equal(readVersion(candidate, (file) => updated[file]), candidate === project ? plan.version : readVersion(candidate, read));
    assert.equal(Object.keys(plan.changes).length, project === Project.Pnport ? 5 : project === Project.AsyncCommitHook ? 4 : project === Project.Clibox ? 3 : plan.kind === Kind.Rust ? 2 : 1);
    if (project === Project.AsyncCommitHook) {
      assert.equal(plan.kind, Kind.Go);
      assert.equal(plan.tag, `async-commit-hook@v${plan.version}`);
      assert.deepEqual(Object.keys(plan.changes).sort(), [...achFiles].sort());
      for (const file of achFiles) assert.equal(plan.changes[file], read(file).replace(plan.previous_version, plan.version));
    }
    if (plan.kind === Kind.Rust) {
      const before = sources["Cargo.lock"].split("[[package]]");
      const after = updated["Cargo.lock"].split("[[package]]");
      assert.equal(before.length, after.length);
      assert.equal(before.filter((section, i) => section !== after[i]).length, project === Project.Pnport ? 3 : 1);
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

test("clibox releases synchronize Cargo and npm and reject npm drift before version writes", () => {
  const plan = versionChanges(Project.Clibox, Bump.Minor, read);
  assert.deepEqual(Object.keys(plan.changes).sort(), ["Cargo.lock", "crates/clibox/Cargo.toml", "packages/clibox/package.json"]);
  assert.equal(JSON.parse(plan.changes["packages/clibox/package.json"]).version, plan.version);
  const drift = (file) => file === "packages/clibox/package.json" ? read(file).replace(`"version": "${plan.previous_version}"`, '"version": "99.0.0"') : read(file);
  assert.throws(() => versionChanges(Project.Clibox, Bump.Patch, drift), /versions disagree/u);
  assert.throws(() => sourceMetadata({ project: Project.Clibox, event: "push", ref: `refs/tags/clibox@v${plan.previous_version}` }, drift), /versions disagree/u);
});

test("pnport first minor bump produces 0.1.0 with CLI, preload, npm, and lockstep versions", () => {
  const plan = versionChanges(Project.Pnport, Bump.Minor, read);
  assert.equal(plan.previous_version, "0.0.0");
  assert.equal(plan.version, "0.1.0");
  assert.deepEqual(Object.keys(plan.changes).sort(), ["Cargo.lock", "crates/pnport/Cargo.toml", "crates/pnport-core/Cargo.toml", "crates/pnport-preload/Cargo.toml", "packages/pnport/package.json"].sort());
  assert.equal(JSON.parse(plan.changes["packages/pnport/package.json"]).version, "0.1.0");
  assert.equal((plan.changes["Cargo.lock"].match(/name = "pnport(?:-core|-preload)?"\nversion = "0\.1\.0"/gu) ?? []).length, 3);
  assert.equal(requiresCargoPublish(Project.Pnport), false);
  for (const bump of [Bump.Patch, Bump.Major]) {
    assert.throws(() => versionChanges(Project.Pnport, bump, read), /first public release requires a minor bump/u);
  }
  for (const driftFile of ["packages/pnport/package.json", "crates/pnport-core/Cargo.toml", "crates/pnport-preload/Cargo.toml"]) {
    const drift = (file) => file === driftFile ? read(file).replace("0.0.0", "9.9.9") : read(file);
    assert.throws(() => versionChanges(Project.Pnport, Bump.Minor, drift), /versions disagree/u);
  }
});

test("React Forge patch after the failed 0.1.0 tag updates only its private source manifest", () => {
  const plan = versionChanges(Project.ReactForge, Bump.Patch, readReactForgeRecovery);
  assert.equal(plan.previous_version, "0.1.0");
  assert.equal(plan.version, "0.1.1");
  assert.deepEqual(Object.keys(plan.changes), ["packages/react-forge/package.json"]);
  assert.equal(JSON.parse(plan.changes["packages/react-forge/package.json"]).version, "0.1.1");
  assert.equal(requiresCargoPublish(Project.ReactForge), false);
  assert.throws(() => readVersion(Project.ReactForge, (file) => file === "packages/react-forge/package.json" ? readReactForgeRecovery(file).replace('"name": "@delino/react-forge"', '"name": "foreign"') : readReactForgeRecovery(file)));
});

for (const file of achFiles) test(`async-commit-hook rejects drift and missing, duplicate or malformed versions in ${file}`, () => {
  const current = readVersion(Project.AsyncCommitHook, read);
  const declaration = read(file).split("\n").find((line) => line.includes(current));
  assert.ok(declaration);
  for (const source of [
    read(file).replace(current, "99.0.0"),
    read(file).replace(declaration + "\n", ""),
    read(file).replace(declaration, `${declaration}\n${declaration}`),
    read(file).replace(current, "01.2.3"),
  ]) {
    const changed = (candidate) => candidate === file ? source : read(candidate);
    assert.throws(() => readVersion(Project.AsyncCommitHook, changed));
    assert.throws(() => versionChanges(Project.AsyncCommitHook, Bump.Patch, changed));
  }
});

test("async-commit-hook rejects foreign identities and ambiguously formatted JSON version keys", () => {
  for (const file of achFiles.filter((file) => file.endsWith(".json"))) {
    const source = read(file);
    const declaration = source.split("\n").find((line) => line.includes('"version":'));
    for (const changed of [
      source.replace(/"(?:name|tag_prefix)": "[^"]+"/u, '"name": "another-project"'),
      source.replace(declaration, `${declaration}\n "version" : "99.0.0",`),
    ]) assert.throws(() => versionChanges(Project.AsyncCommitHook, Bump.Patch, (candidate) => candidate === file ? changed : read(candidate)));
  }
});

test("Downstream manual and tag metadata must agree with source before builds", () => {
  for (const project of Object.values(Project).filter((project) => project !== Project.AsyncCommitHook)) {
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

test("async-commit-hook version drift fails before preflight or version writes", async (t) => {
  const state = fixture(t);
  const file = "apps/async-commit-hook/package.json";
  writeFileSync(path.join(state.directory, file), read(file).replace(readVersion(Project.AsyncCommitHook, read), "99.0.0"));
  git(state.directory, ["add", file]);
  git(state.directory, ["commit", "-m", "fixture version drift"]);
  git(state.directory, ["push", "origin", "HEAD:main"]);
  const before = git(state.directory, ["rev-parse", "HEAD"]);
  let checked = false;
  await assert.rejects(prepareRelease({ directory: state.directory, project: Project.AsyncCommitHook, bump: Bump.Patch, runId: "31", ...bot,
    preflight: async () => { checked = true; },
  }), /versions disagree/u);
  assert.equal(checked, false);
  assert.equal(git(state.directory, ["status", "--porcelain"]), "");
  assert.equal(git(state.remote, ["rev-parse", "refs/heads/main"]), before);
  assert.equal(git(state.remote, ["tag", "--list"]), "");
});

for (const project of Object.values(Project)) test(`${project} commit journals and resumes the same run after main advances`, async (t) => {
  const fixtureState = fixture(t);
  const bump = project === Project.ReactForge ? Bump.Patch : Bump.Minor;
  const options = { directory: fixtureState.directory, project, bump, runId: "123", ...bot };
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
  assert.throws(() => validateCommit(fixtureState.directory, first.revision, project, bump === Bump.Patch ? Bump.Minor : Bump.Patch, "123"), project === Project.Pnport ? /first public release requires a minor bump/u : /journal/u);
  assert.throws(() => validateCommit(fixtureState.directory, first.revision, project, bump, "456"), /journal/u);
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

for (const project of [Project.Derun, Project.AsyncCommitHook]) test(`${project} recovery rejects extra paths and hidden non-version changes`, async (t) => {
  const state = fixture(t);
  const options = { directory: state.directory, project, bump: Bump.Patch, runId: "1", ...bot };
  const first = await prepareRelease(options);
  const file = project === Project.AsyncCommitHook ? "apps/async-commit-hook/package.json" : "cmds/derun/internal/version/version.go";
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

test("React Forge requires patch recovery while the current version is unpublished", async () => {
  for (const bump of [Bump.Minor, Bump.Major]) {
    const plan = versionChanges(Project.ReactForge, bump, readReactForgeRecovery);
    await assert.rejects(preflightVersion(plan, absent, async () => false), /requires the next patch version/u);
    await preflightVersion(plan, absent, async () => true);
  }
  await preflightVersion(versionChanges(Project.ReactForge, Bump.Patch, readReactForgeRecovery), absent, async () => { throw new Error("Patch must not read npm"); });
  const later = (file) => file === "packages/react-forge/package.json" ? readReactForgeRecovery(file).replace(/("version": ")0\.1\.0/u, (_, prefix) => `${prefix}0.1.1`) : readReactForgeRecovery(file);
  await assert.rejects(preflightVersion(versionChanges(Project.ReactForge, Bump.Minor, later), absent, async () => false), /requires the next patch version/u);
});

test("React Forge npm publication lookup fails closed on uncertain responses", async () => {
  const version = "0.1.1";
  const metadata = { name: "@delino/react-forge", version, dist: { integrity: "sha512-fixture" } };
  const lookup = (status, body = metadata) => reactForgeVersionPublished(version, async (url, options) => {
    assert.equal(url, "https://registry.npmjs.org/%40delino%2Freact-forge/0.1.1");
    assert.equal(options.redirect, "error");
    return { status, ok: status === 200, json: async () => body };
  });
  assert.equal(await lookup(404), false);
  assert.equal(await lookup(200), true);
  await assert.rejects(lookup(503), /HTTP 503/u);
  await assert.rejects(lookup(200, { ...metadata, version: "0.1.2" }), /identity mismatch/u);
  await assert.rejects(reactForgeVersionPublished(version, async () => { throw new Error("transport"); }), /lookup failed/u);
});

for (const project of [Project.Binpm, Project.Pnport, Project.AsyncCommitHook]) test(`${project} tag-push interruption recovers only the same tag and commit`, async (t) => {
  const state = fixture(t);
  const plan = await prepareRelease({ directory: state.directory, project, bump: project === Project.Pnport ? Bump.Minor : Bump.Patch, runId: "22", ...bot });
  const request = async () => {
    const sha = git(state.remote, ["for-each-ref", "--format=%(objectname)", `refs/tags/${plan.tag}`]);
    return sha ? { status: 200, body: { object: { type: "commit", sha } } } : { status: 404 };
  };
  assert.equal((await pushReleaseTag({ directory: state.directory, identity: plan, request })).reused, false);
  assert.equal((await pushReleaseTag({ directory: state.directory, identity: plan, request })).reused, true);
  await assert.rejects(pushReleaseTag({ directory: state.directory, identity: { ...plan, revision: state.initial }, request }), /different commit/u);
  assert.equal(git(state.remote, ["tag", "--list"]), plan.tag);
});
