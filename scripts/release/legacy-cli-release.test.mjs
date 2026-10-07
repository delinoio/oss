import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";
import { artifactNames, findRelease, main, productionDependencies, publish, releasePlan, stage } from "./legacy-cli-release.mjs";

const revision = "a".repeat(40);
const hash = (bytes) => createHash("sha256").update(bytes).digest("hex");
const planFor = (project) => ({ project, version: "1.2.3", tag: `${project}@v1.2.3`, revision });
const signature = (bytes, plan) => Buffer.from(JSON.stringify({ sha256: hash(bytes), revision: plan.revision, tag: plan.tag }));

function temporary(t) {
  const directory = mkdtempSync(path.join(tmpdir(), "legacy-release-"));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  return directory;
}
function fixture(t, project = "binpm", state = "public") {
  const plan = planFor(project);
  const directory = temporary(t);
  for (const name of artifactNames(project)) writeFileSync(path.join(directory, name), `artifact ${name}\n`);
  const files = stage(project, directory);
  const calls = [];
  const bytes = new Map();
  let nextId = 100;
  const f = { plan, files, calls, bytes, object: { type: "commit", sha: revision }, release: null, directory };
  const add = (name, value) => {
    const id = nextId++;
    bytes.set(id, value);
    f.release.assets.push({ id, name, size: value.length, digest: `sha256:${hash(value)}`, state: "uploaded" });
  };
  f.add = add;
  if (state !== "absent") {
    f.release = { id: 42, tag_name: plan.tag, target_commitish: state === "public" ? "main" : revision, draft: state !== "public", prerelease: false, immutable: state === "public", assets: [] };
    for (const [name, value] of files) {
      add(name, value); add(`${name}.sigstore.json`, signature(value, plan));
    }
  }
  f.dependencies = {
    api: async (method, endpoint, body) => {
      calls.push({ method, endpoint, body });
      if (method === "GET") {
        if (endpoint.includes("/git/ref/")) return { object: f.object };
        if (endpoint.includes("/git/tags/")) return { object: { type: "commit", sha: revision } };
        if (endpoint.includes("?per_page=")) return f.release ? [structuredClone(f.release)] : [];
        assert.equal(endpoint, "/repos/delinoio/oss/releases/42");
        return structuredClone(f.release);
      }
      if (method === "POST") {
        assert.equal(endpoint, "/repos/delinoio/oss/releases");
        assert.equal(body.draft, true);
        assert.equal(body.target_commitish, revision);
        assert.equal(body.generate_release_notes, true);
        f.release = { id: 42, ...body, assets: [] };
      } else {
        assert.equal(method, "PATCH");
        assert.equal(f.release.draft, true);
        assert.deepEqual(body, { draft: false });
        f.release.draft = false; f.release.immutable = true;
      }
      return structuredClone(f.release);
    },
    download: async (asset) => { calls.push({ method: "DOWNLOAD", name: asset.name }); return bytes.get(asset.id); },
    upload: async (id, name, value) => {
      calls.push({ method: "UPLOAD", name });
      assert.equal(id, 42); assert.equal(f.release.draft, true);
      assert.ok(!f.release.assets.some((asset) => asset.name === name), "existing assets must never be uploaded again");
      add(name, f.corruptUpload === name ? Buffer.from("corrupted upload") : value);
    },
    sign: async (name, value, plan) => { calls.push({ method: "SIGN", name }); return signature(value, plan); },
    verify: async (name, value, bundle, plan) => {
      calls.push({ method: "VERIFY", name });
      assert.deepEqual(JSON.parse(bundle), JSON.parse(signature(value, plan)), "signature must bind exact bytes, tag and source");
    },
    report: (stage) => calls.push({ method: "REPORT", stage }),
  };
  return f;
}
const writes = (f) => f.calls.filter(({ method }) => ["POST", "PATCH", "UPLOAD", "SIGN", "DELETE"].includes(method));

for (const project of ["binpm", "cargo-mono", "nodeup", "with-watch", "derun"]) {
  test(`${project}: immutable release allows a failed-then-recovered tap with zero release/signing writes`, async (t) => {
    const f = fixture(t, project);
    let tapAttempts = 0;
    const publishAndTap = async () => {
      const result = await publish(f.plan, f.files, f.dependencies);
      assert.deepEqual(result, { releaseId: 42, reused: true });
      if (++tapAttempts === 1) throw new Error("tap failure");
    };
    await assert.rejects(publishAndTap(), /tap failure/u);
    await publishAndTap();
    assert.equal(tapAttempts, 2);
    assert.deepEqual(writes(f), []);
    assert.equal(f.calls.filter(({ method }) => method === "VERIFY").length, 2 * f.files.size);
  });
}

for (const [name, mutate] of [
  ["missing archive", (f) => f.release.assets.shift()],
  ["missing signature", (f) => f.release.assets.pop()],
  ["unexpected asset", (f) => f.add("extra", Buffer.from("unexpected"))],
  ["duplicate asset name", (f) => f.release.assets.push({ ...f.release.assets[0], id: 999 })],
  ["duplicate asset ID", (f) => { f.release.assets[1].id = f.release.assets[0].id; }],
  ["failed upload state", (f) => { f.release.assets[0].state = "starter"; }],
  ["wrong size", (f) => { f.release.assets[0].size++; }],
  ["wrong digest", (f) => { f.release.assets[0].digest = `sha256:${"b".repeat(64)}`; }],
  ["changed archive bytes", (f) => {
    const asset = f.release.assets[0]; const changed = Buffer.from("changed artifact");
    f.bytes.set(asset.id, changed); asset.size = changed.length; asset.digest = `sha256:${hash(changed)}`;
  }],
  ["changed checksum manifest", (f) => {
    const asset = f.release.assets.find(({ name }) => name === "SHA256SUMS");
    const changed = Buffer.from("wrong checksums"); f.bytes.set(asset.id, changed); asset.size = changed.length; asset.digest = null;
  }],
  ["wrong signature source", (f) => {
    const asset = f.release.assets[1]; const changed = signature(f.files.get(f.release.assets[0].name), { ...f.plan, revision: "b".repeat(40) });
    f.bytes.set(asset.id, changed); asset.size = changed.length; asset.digest = `sha256:${hash(changed)}`;
  }],
  ["prerelease", (f) => { f.release.prerelease = true; }],
  ["conflicting tag", (f) => { f.object.sha = "b".repeat(40); }],
]) test(`public ${name} blocks before signing, release writes or tap work`, async (t) => {
  const f = fixture(t); mutate(f);
  let tapAttempts = 0;
  await assert.rejects(async () => { await publish(f.plan, f.files, f.dependencies); tapAttempts++; });
  assert.equal(tapAttempts, 0); assert.deepEqual(writes(f), []);
});

test("fresh publication signs, uploads only into a draft and verifies readback before publication", async (t) => {
  const f = fixture(t, "derun", "absent");
  assert.deepEqual(await publish(f.plan, f.files, f.dependencies), { releaseId: 42, reused: false });
  assert.equal(f.calls.filter(({ method }) => method === "SIGN").length, f.files.size);
  assert.equal(f.calls.filter(({ method }) => method === "UPLOAD").length, 2 * f.files.size);
  const publishIndex = f.calls.findIndex(({ method }) => method === "PATCH");
  assert.ok(f.calls.slice(0, publishIndex).some(({ stage }) => stage === "signed-draft-verified"));
  assert.ok(f.calls.slice(publishIndex).some(({ method }) => method === "VERIFY"));
  const before = writes(f).length;
  await publish(f.plan, f.files, f.dependencies);
  assert.equal(writes(f).length, before);
});

test("owned partial drafts preserve existing bundles and upload/sign only missing assets", async (t) => {
  const f = fixture(t, "nodeup", "draft");
  const original = new Map(f.release.assets.map(({ name, id }) => [name, f.bytes.get(id)]));
  const missing = [f.release.assets[0].name, f.release.assets[3].name];
  f.release.assets = f.release.assets.filter(({ name }) => !missing.includes(name));
  await publish(f.plan, f.files, f.dependencies);
  assert.deepEqual(f.calls.filter(({ method }) => method === "UPLOAD").map(({ name }) => name).sort(), missing.sort());
  assert.equal(f.calls.filter(({ method }) => method === "SIGN").length, 1);
  for (const asset of f.release.assets) assert.deepEqual(f.bytes.get(asset.id), original.get(asset.name));
});

test("complete signed drafts need no signing or uploads", async (t) => {
  const f = fixture(t, "with-watch", "draft");
  await publish(f.plan, f.files, f.dependencies);
  assert.deepEqual(writes(f).map(({ method }) => method), ["PATCH"]);
});

test("unowned or conflicting partial drafts block before any write", async (t) => {
  for (const mutate of [
    (f) => { f.release.target_commitish = "main"; },
    (f) => { f.release.assets.pop(); f.bytes.set(f.release.assets[0].id, Buffer.alloc(f.release.assets[0].size)); },
    (f) => { f.release.assets[1].digest = "invalid"; },
  ]) {
    const f = fixture(t, "binpm", "draft"); mutate(f);
    await assert.rejects(publish(f.plan, f.files, f.dependencies)); assert.deepEqual(writes(f), []);
  }
});

test("bad signature generation and corrupt readback never publish a draft", async (t) => {
  const badSignature = fixture(t, "derun", "absent");
  badSignature.dependencies.sign = () => Buffer.from("invalid signature");
  await assert.rejects(publish(badSignature.plan, badSignature.files, badSignature.dependencies));
  assert.equal(badSignature.release, null);
  const f = fixture(t, "derun", "absent"); f.corruptUpload = f.files.keys().next().value;
  await assert.rejects(publish(f.plan, f.files, f.dependencies), /ASSET_BYTES_MISMATCH/u);
  assert.equal(f.release.draft, true); assert.ok(!f.calls.some(({ method }) => method === "PATCH"));
});

test("an unconfirmed publication stops without automatic mutation retry; next attempt reuses public state", async (t) => {
  const f = fixture(t, "binpm", "draft"); const api = f.dependencies.api;
  f.dependencies.api = async (...args) => {
    const response = await api(...args);
    if (args[0] === "PATCH") throw new Error("connection lost after publication");
    return response;
  };
  await assert.rejects(publish(f.plan, f.files, f.dependencies), /connection lost/u);
  assert.equal(f.calls.filter(({ method }) => method === "PATCH").length, 1);
  f.dependencies.api = api;
  assert.equal((await publish(f.plan, f.files, f.dependencies)).reused, true);
  assert.equal(f.calls.filter(({ method }) => method === "PATCH").length, 1);
});

test("draft discovery follows every list page, rejects duplicates and reads the pinned ID", async () => {
  const plan = planFor("binpm");
  const unrelated = Array.from({ length: 100 }, (_, index) => ({ id: index + 100, tag_name: `other-${index}` }));
  const target = { id: 42, tag_name: plan.tag, draft: true, prerelease: false, target_commitish: revision, assets: [] };
  const endpoints = [];
  const api = async (_, endpoint) => {
    endpoints.push(endpoint);
    if (endpoint.endsWith("page=1")) return unrelated;
    if (endpoint.endsWith("page=2")) return [target];
    return target;
  };
  assert.equal((await findRelease(plan, api)).id, 42);
  assert.equal(endpoints.at(-1), "/repos/delinoio/oss/releases/42");
  await assert.rejects(findRelease(plan, async () => [target, target]), /DUPLICATE_RELEASE/u);
  await assert.rejects(findRelease(plan, async () => null), /RELEASE_LIST_INVALID/u);
  await assert.rejects(findRelease(plan, async (_, endpoint) => endpoint.includes("page=") ? [target] : { ...target, id: 43 }), /RELEASE_ID_MISMATCH/u);
});

test("a competing same-tag release before publication blocks the PATCH", async (t) => {
  const f = fixture(t, "derun", "draft"); const api = f.dependencies.api; let lists = 0;
  f.dependencies.api = async (...args) => {
    const result = await api(...args);
    if (args[1].includes("?per_page=") && ++lists === 3) return [f.release, { ...f.release, id: 43 }];
    return result;
  };
  await assert.rejects(publish(f.plan, f.files, f.dependencies), /DUPLICATE_RELEASE/u);
  assert.deepEqual(writes(f), []);
});

test("annotated tags resolve to the exact source", async (t) => {
  const f = fixture(t); f.object = { type: "tag", sha: "b".repeat(40) };
  assert.equal((await publish(f.plan, f.files, f.dependencies)).reused, true);
});

test("staging matches the legacy sorted checksum bytes and rejects extra or missing inputs", (t) => {
  const f = fixture(t, "cargo-mono");
  const names = artifactNames("cargo-mono");
  assert.ok(names.includes("cargo-mono")); assert.equal(names.length, 7);
  const [executable, ...args] = process.platform === "darwin" ? ["shasum", "-a", "256"] : ["sha256sum"];
  assert.equal(f.files.get("SHA256SUMS").toString(), execFileSync(executable, [...args, "--", ...names], { cwd: f.directory, encoding: "utf8" }));
  assert.throws(() => stage("cargo-mono", f.directory), /UNSIGNED_INVENTORY_MISMATCH/u);
  rmSync(path.join(f.directory, "SHA256SUMS")); rmSync(path.join(f.directory, names[0]));
  assert.throws(() => stage("cargo-mono", f.directory), /UNSIGNED_INVENTORY_MISMATCH/u);
  assert.throws(() => artifactNames("runmoor"), /INVALID_PROJECT/u);
});

test("release context preserves exact source/tag, main dispatch and dry-run boundaries", () => {
  const read = (file) => file === "Cargo.lock" ? '[[package]]\nname = "binpm"\nversion = "1.2.3"\n' : '[package]\nname = "binpm"\nversion = "1.2.3"\n';
  const env = { RELEASE_PROJECT: "binpm", RELEASE_TAG: "binpm@v1.2.3", RELEASE_VERSION: "1.2.3", GITHUB_SHA: revision, GITHUB_EVENT_NAME: "push", GITHUB_REF: "refs/tags/binpm@v1.2.3", DRY_RUN: "false", GITHUB_ACTIONS: "true", GITHUB_REPOSITORY: "delinoio/oss", GH_TOKEN: "fixture" };
  assert.equal(releasePlan(env, read).dryRun, false);
  assert.equal(releasePlan({ ...env, GITHUB_EVENT_NAME: "workflow_dispatch", GITHUB_REF: "refs/heads/main" }, read).dryRun, false);
  assert.equal(releasePlan({ ...env, GITHUB_EVENT_NAME: "workflow_dispatch", GITHUB_REF: "refs/heads/fixture", DRY_RUN: "true", GH_TOKEN: "" }, read).dryRun, true);
  for (const override of [{ RELEASE_TAG: "wrong" }, { GITHUB_SHA: "bad" }, { GITHUB_REPOSITORY: "fork/oss" }, { GITHUB_REF: "refs/heads/other" }, { DRY_RUN: "true" }, { RELEASE_PROJECT: "runmoor" }]) assert.throws(() => releasePlan({ ...env, ...override }, read));
});

test("real CLI dry run emits unsigned checksums without GitHub or OIDC credentials", async (t) => {
  const project = "derun";
  const directory = temporary(t);
  for (const name of artifactNames(project)) writeFileSync(path.join(directory, name), "dry-run artifact");
  const version = readFileSync(new URL("../../cmds/derun/internal/version/version.go", import.meta.url), "utf8").match(/const Version = "([^"]+)"/u)[1];
  const sha = execFileSync("git", ["rev-parse", "HEAD"], { encoding: "utf8" }).trim();
  await main(["--artifacts-dir", directory], { RELEASE_PROJECT: project, RELEASE_VERSION: version, RELEASE_TAG: `derun@v${version}`, DRY_RUN: "true", GITHUB_SHA: sha, GITHUB_EVENT_NAME: "workflow_dispatch", GITHUB_REF: "refs/heads/fixture" });
  assert.match(readFileSync(path.join(directory, "SHA256SUMS"), "utf8"), /derun-windows-amd64\.zip\n$/u);
});

test("production verifier requests exact workflow, issuer, source SHA and repository claims", (t) => {
  const directory = temporary(t); const bin = path.join(directory, "bin"); mkdirSync(bin);
  const captured = path.join(directory, "arguments.json");
  writeFileSync(path.join(bin, "cosign"), `#!${process.execPath}\nrequire('node:fs').writeFileSync(${JSON.stringify(captured)}, JSON.stringify(process.argv.slice(2)));\n`, { mode: 0o755 });
  const previous = process.env.PATH; process.env.PATH = `${bin}:${previous}`;
  try {
    productionDependencies(directory).verify("SHA256SUMS", Buffer.from("manifest"), Buffer.from("bundle"), planFor("binpm"));
  } finally { process.env.PATH = previous; }
  const args = JSON.parse(readFileSync(captured, "utf8"));
  assert.equal(args[args.indexOf("--certificate-github-workflow-sha") + 1], revision);
  assert.equal(args[args.indexOf("--certificate-github-workflow-repository") + 1], "delinoio/oss");
  assert.equal(args[args.indexOf("--certificate-oidc-issuer") + 1], "https://token.actions.githubusercontent.com");
  const regexp = args[args.indexOf("--certificate-identity-regexp") + 1];
  assert.equal(regexp, "^https://github\\.com/delinoio/oss/\\.github/workflows/release-binpm\\.yml@(?:refs/tags/binpm@v1\\.2\\.3|refs/heads/main)$");
});
