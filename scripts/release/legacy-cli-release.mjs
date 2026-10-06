import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { lstatSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { Project, sourceMetadata } from "./project.mjs";
import { verifyBundle } from "./linux-packages/release-input.mjs";

const repository = "delinoio/oss";
const prefix = `/repos/${repository}`;
const root = fileURLToPath(new URL("../..", import.meta.url));
const projects = [Project.Binpm, Project.CargoMono, Project.Nodeup, Project.WithWatch, Project.Derun];
const sha256 = (bytes) => createHash("sha256").update(bytes).digest("hex");
const ensure = (condition, code) => { if (!condition) throw new Error(code); };
const log = (stage, plan, releaseId) => console.error(JSON.stringify({ event: "legacy-cli-release", stage, project: plan.project, tag: plan.tag, revision: plan.revision, releaseId }));

export function artifactNames(project) {
  ensure(projects.includes(project), "INVALID_PROJECT");
  const targets = ["darwin-amd64", "darwin-arm64", "linux-amd64", "linux-arm64", "windows-amd64", ...(project === Project.Derun ? [] : ["windows-arm64"])];
  const names = targets.map((target) => `${project}-${target}.${target.startsWith("windows") ? "zip" : "tar.gz"}`);
  if (![Project.CargoMono, Project.Derun].includes(project)) names.push(...targets.map((target) => `${project}-${target}${target.startsWith("windows") ? ".exe" : ""}`));
  // cargo-mono's legacy build also retains one unqualified Unix binary. Keep
  // that existing public inventory until a separate distribution change removes it.
  if (project === Project.CargoMono) names.push("cargo-mono");
  return names.sort();
}

export function stage(project, directory) {
  const names = artifactNames(project);
  ensure(JSON.stringify(readdirSync(directory).sort()) === JSON.stringify(names), "UNSIGNED_INVENTORY_MISMATCH");
  const files = new Map(names.map((name) => {
    const file = path.join(directory, name);
    ensure(lstatSync(file).isFile(), "INVALID_LOCAL_ASSET");
    const bytes = readFileSync(file);
    ensure(bytes.length > 0, "EMPTY_LOCAL_ASSET");
    return [name, bytes];
  }));
  // Match generate-checksums.sh's sorted GNU text manifest without signing yet.
  files.set("SHA256SUMS", Buffer.from([...files].map(([name, bytes]) => `${sha256(bytes)}  ${name}\n`).join("")));
  writeFileSync(path.join(directory, "SHA256SUMS"), files.get("SHA256SUMS"));
  return files;
}

export async function checkTag(plan, api) {
  let object = (await api("GET", `${prefix}/git/ref/tags/${encodeURIComponent(plan.tag)}`))?.object;
  for (let depth = 0; object?.type === "tag" && depth < 4; depth++) {
    ensure(/^[a-f0-9]{40}$/u.test(object.sha ?? ""), "INVALID_TAG_OBJECT");
    object = (await api("GET", `${prefix}/git/tags/${object.sha}`))?.object;
  }
  ensure(object?.type === "commit" && object.sha === plan.revision, "TAG_SOURCE_MISMATCH");
}

function checkIdentity(plan, release, id) {
  ensure(Number.isSafeInteger(release?.id) && release.id > 0 && (id === undefined || release.id === id), "RELEASE_ID_MISMATCH");
  ensure(release.tag_name === plan.tag && release.prerelease === false && typeof release.draft === "boolean", "RELEASE_IDENTITY_MISMATCH");
  // Public target_commitish may be a branch name; the resolved tag is its source
  // authority. Partial drafts require an explicit commit before we add assets.
  ensure(!release.draft || release.target_commitish === plan.revision, "DRAFT_SOURCE_MISMATCH");
}

export async function findRelease(plan, api) {
  const matches = [];
  // The tag endpoint cannot discover drafts. Enumerate every page and pin the
  // numeric ID, rejecting duplicate same-tag records before signing or writing.
  for (let page = 1; page <= 1000; page++) {
    const releases = await api("GET", `${prefix}/releases?per_page=100&page=${page}`);
    ensure(Array.isArray(releases) && releases.length <= 100 && releases.every((release) => Number.isSafeInteger(release?.id) && release.id > 0 && typeof release.tag_name === "string"), "RELEASE_LIST_INVALID");
    matches.push(...releases.filter((release) => release.tag_name === plan.tag));
    ensure(matches.length <= 1, "DUPLICATE_RELEASE");
    if (releases.length < 100) {
      if (!matches.length) return null;
      const release = await api("GET", `${prefix}/releases/${matches[0].id}`);
      checkIdentity(plan, release, matches[0].id);
      return release;
    }
  }
  throw new Error("RELEASE_LIST_LIMIT");
}

async function inspect(plan, release, files, { download, verify }, complete) {
  checkIdentity(plan, release);
  const expected = [...files.keys()].flatMap((name) => [name, `${name}.sigstore.json`]);
  ensure(Array.isArray(release.assets), "ASSET_INVENTORY_INVALID");
  const assets = new Map();
  const ids = new Set();
  for (const asset of release.assets) {
    ensure(expected.includes(asset.name) && !assets.has(asset.name) && Number.isSafeInteger(asset.id) && asset.id > 0 && !ids.has(asset.id) && asset.state === "uploaded" && Number.isSafeInteger(asset.size) && asset.size > 0, "ASSET_INVENTORY_INVALID");
    assets.set(asset.name, asset); ids.add(asset.id);
  }
  ensure(!complete || assets.size === expected.length, "INCOMPLETE_RELEASE");
  const existing = new Map();
  for (const [name, asset] of assets) {
    const bytes = await download(asset);
    ensure(Buffer.isBuffer(bytes) && bytes.length === asset.size, "ASSET_SIZE_MISMATCH");
    ensure(asset.digest == null || asset.digest === `sha256:${sha256(bytes)}`, "ASSET_DIGEST_MISMATCH");
    existing.set(name, bytes);
  }
  for (const [name, bytes] of files) {
    ensure(!existing.has(name) || existing.get(name).equals(bytes), "ASSET_BYTES_MISMATCH");
    if (existing.has(`${name}.sigstore.json`)) await verify(name, bytes, existing.get(`${name}.sigstore.json`), plan);
  }
  return existing;
}

export async function publish(plan, files, dependencies) {
  const { api, upload, sign, verify, report = log } = dependencies;
  ensure(projects.includes(plan.project) && /^[a-f0-9]{40}$/u.test(plan.revision ?? "") && plan.tag === `${plan.project}@v${plan.version}`, "INVALID_PLAN");
  const names = [...artifactNames(plan.project), "SHA256SUMS"].sort();
  ensure(JSON.stringify([...files.keys()].sort()) === JSON.stringify(names), "CANDIDATE_INVENTORY_MISMATCH");
  await checkTag(plan, api);
  let release = await findRelease(plan, api);
  const existing = release ? await inspect(plan, release, files, dependencies, !release.draft) : new Map();
  if (release?.draft === false) {
    report("public-reused", plan, release.id);
    return { releaseId: release.id, reused: true };
  }

  // Sign only missing bundles, after the entire remote inventory passes. Keep
  // nondeterministic verified bundles from previous attempts unchanged.
  const candidate = new Map(files);
  for (const [name, bytes] of files) {
    const bundleName = `${name}.sigstore.json`;
    const bundle = existing.get(bundleName) ?? await sign(name, bytes, plan);
    ensure(Buffer.isBuffer(bundle) && bundle.length > 0, "INVALID_SIGNATURE_BUNDLE");
    await verify(name, bytes, bundle, plan);
    candidate.set(bundleName, bundle);
  }
  await checkTag(plan, api);
  const current = await findRelease(plan, api);
  ensure((current?.id ?? null) === (release?.id ?? null), "RELEASE_ID_MISMATCH");
  if (current) {
    ensure(current.draft === true, "RELEASE_STATE_CHANGED");
    const checked = await inspect(plan, current, files, dependencies, false);
    ensure(checked.size === existing.size && [...checked].every(([name, bytes]) => existing.get(name)?.equals(bytes)), "RELEASE_STATE_CHANGED");
  } else {
    release = await api("POST", `${prefix}/releases`, { tag_name: plan.tag, target_commitish: plan.revision, name: plan.tag, draft: true, prerelease: false, generate_release_notes: true });
    checkIdentity(plan, release);
    ensure(release.draft === true && Array.isArray(release.assets) && release.assets.length === 0, "NEW_DRAFT_INVALID");
    report("draft-created", plan, release.id);
  }
  for (const [name, bytes] of candidate) if (!existing.has(name)) await upload(release.id, name, bytes);
  await checkTag(plan, api);
  const ready = await findRelease(plan, api);
  checkIdentity(plan, ready, release.id);
  ensure(ready.draft === true, "RELEASE_STATE_CHANGED");
  const readback = await inspect(plan, ready, files, dependencies, true);
  ensure([...candidate].every(([name, bytes]) => readback.get(name)?.equals(bytes)), "SIGNED_READBACK_MISMATCH");
  report("signed-draft-verified", plan, release.id);
  // Unknown write outcomes stop this attempt. A later run reconciles by ID and
  // inventory instead of automatically repeating publication or uploads.
  const published = await api("PATCH", `${prefix}/releases/${release.id}`, { draft: false });
  checkIdentity(plan, published, release.id);
  ensure(published.draft === false, "PUBLICATION_UNCONFIRMED");
  await checkTag(plan, api);
  const confirmed = await findRelease(plan, api);
  checkIdentity(plan, confirmed, release.id);
  ensure(confirmed.draft === false, "PUBLICATION_UNCONFIRMED");
  await inspect(plan, confirmed, files, dependencies, true);
  report("published", plan, release.id);
  return { releaseId: release.id, reused: false };
}

export function releasePlan(env = process.env, read = (file) => readFileSync(path.join(root, file), "utf8")) {
  ensure(projects.includes(env.RELEASE_PROJECT) && /^[a-f0-9]{40}$/u.test(env.GITHUB_SHA ?? ""), "INVALID_RELEASE_CONTEXT");
  const metadata = sourceMetadata({ project: env.RELEASE_PROJECT, event: env.GITHUB_EVENT_NAME, ref: env.GITHUB_REF, requestedVersion: env.RELEASE_VERSION, requestedDryRun: env.DRY_RUN }, read);
  ensure(metadata.tag === env.RELEASE_TAG && metadata.dry_run === env.DRY_RUN, "RELEASE_METADATA_MISMATCH");
  if (metadata.dry_run === "false") ensure(env.GITHUB_ACTIONS === "true" && env.GITHUB_REPOSITORY === repository && env.GH_TOKEN, "PUBLICATION_CONTEXT_REQUIRED");
  return { project: env.RELEASE_PROJECT, version: metadata.version, tag: metadata.tag, revision: env.GITHUB_SHA, dryRun: metadata.dry_run === "true" };
}

export function productionDependencies(directory, env = process.env) {
  const request = async (url, options = {}) => {
    const response = await fetch(url, { ...options, headers: { Authorization: `Bearer ${env.GH_TOKEN}`, Accept: "application/vnd.github+json", "X-GitHub-Api-Version": "2022-11-28", ...options.headers }, signal: AbortSignal.timeout(60000), redirect: "error" });
    ensure(response.ok, `GITHUB_REQUEST_FAILED_${response.status}`);
    return response.json();
  };
  return {
    api: (method, endpoint, body) => request(`https://api.github.com${endpoint}`, { method, body: body === undefined ? undefined : JSON.stringify(body), headers: { "Content-Type": "application/json" } }),
    // gh handles authenticated asset redirects without forwarding a bearer
    // token to an arbitrary browser_download_url supplied by remote metadata.
    download: (asset) => execFileSync("gh", ["api", `repos/${repository}/releases/assets/${asset.id}`, "-H", "Accept: application/octet-stream"], { env, maxBuffer: 300 * 1024 * 1024, stdio: ["ignore", "pipe", "pipe"] }),
    upload: (id, name, bytes) => request(`https://uploads.github.com${prefix}/releases/${id}/assets?name=${encodeURIComponent(name)}`, { method: "POST", body: bytes, headers: { "Content-Type": "application/octet-stream" } }),
    sign: (name) => {
      ensure(env.ACTIONS_ID_TOKEN_REQUEST_TOKEN && env.ACTIONS_ID_TOKEN_REQUEST_URL, "SIGNING_CONTEXT_REQUIRED");
      const file = path.join(directory, name);
      execFileSync("cosign", ["sign-blob", "--yes", "--bundle", `${file}.sigstore.json`, file], { env, stdio: "inherit" });
      return readFileSync(`${file}.sigstore.json`);
    },
    verify: (name, bytes, bundle, plan) => {
      const temp = mkdtempSync(path.join(tmpdir(), "legacy-cli-verify-"));
      try {
        const file = path.join(temp, name);
        writeFileSync(file, bytes); writeFileSync(`${file}.sigstore.json`, bundle);
        verifyBundle(file, `${file}.sigstore.json`, plan);
      } finally { rmSync(temp, { recursive: true, force: true }); }
    },
  };
}

export async function main(args = process.argv.slice(2), env = process.env) {
  ensure(args.length === 2 && args[0] === "--artifacts-dir", "INVALID_ARGUMENTS");
  const plan = releasePlan(env);
  ensure(execFileSync("git", ["rev-parse", "HEAD"], { encoding: "utf8" }).trim() === plan.revision, "CHECKOUT_SOURCE_MISMATCH");
  const directory = path.resolve(args[1]);
  const files = stage(plan.project, directory);
  if (plan.dryRun) { log("unsigned-dry-run", plan); return; }
  return publish(plan, files, productionDependencies(directory, env));
}
if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) main().catch((error) => {
  console.error(JSON.stringify({ event: "legacy-cli-release-failed", code: error.message })); process.exitCode = 1;
});
