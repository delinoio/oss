import { execFileSync } from "node:child_process";
import { appendFileSync, readFileSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

export const Project = Object.freeze({ Binpm: "binpm", CargoMono: "cargo-mono", Nodeup: "nodeup", WithWatch: "with-watch", Derun: "derun", Runmoor: "runmoor", Clibox: "clibox", Pnport: "pnport", AsyncCommitHook: "async-commit-hook" });
export const Bump = Object.freeze({ Patch: "patch", Minor: "minor", Major: "major" });
export const Kind = Object.freeze({ Rust: "rust", Go: "go" });
const repository = "delinoio/oss";
const botName = "delino-release-bot[bot]";
const root = fileURLToPath(new URL("../..", import.meta.url));
const versions = Object.freeze({
  clibox: { kind: Kind.Rust, file: "crates/clibox/Cargo.toml" },
  pnport: { kind: Kind.Rust, file: "crates/pnport/Cargo.toml" },
  binpm: { kind: Kind.Rust, file: "crates/binpm/Cargo.toml" },
  "cargo-mono": { kind: Kind.Rust, file: "crates/cargo-mono/Cargo.toml" },
  nodeup: { kind: Kind.Rust, file: "crates/nodeup/Cargo.toml" },
  "with-watch": { kind: Kind.Rust, file: "crates/with-watch/Cargo.toml" },
  derun: { kind: Kind.Go, file: "cmds/derun/internal/version/version.go" },
  runmoor: { kind: Kind.Go, file: "cmds/runmoor/internal/runmoor/types.go" },
  "async-commit-hook": { kind: Kind.Go, file: "cmds/async-commit-hook/internal/core/model.go" },
});
const asyncCommitHookVersions = Object.freeze([
  { file: "apps/async-commit-hook/package.json", name: "async-commit-hook" },
  { file: "packages/async-commit-hook-api-client/package.json", name: "@delinoio/async-commit-hook-api-client" },
  { file: "packaging/async-commit-hook/release-metadata.json" },
]);
const shaPattern = /^[a-f0-9]{40}$/u;
const semverPattern = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/u;

function requireValue(condition, message) {
  if (!condition) throw new Error(message);
}
function descriptor(project) {
  requireValue(Object.values(Project).includes(project), "Unknown release project");
  return versions[project];
}

// Rust source versioning is independent from registry distribution: clibox
// and pnport ship only through npm and native packages.
export function requiresCargoPublish(project) {
  return descriptor(project).kind === Kind.Rust && ![Project.Clibox, Project.Pnport].includes(project);
}

function replaceLockVersion(lock, project, previous, next) {
  const sections = [...lock.matchAll(/^\[\[package\]\]\n[\s\S]*?(?=^\[\[|$(?![\s\S]))/gmu)]
    .filter(([section]) => section.split("\n").includes(`name = "${project}"`));
  requireValue(sections.length === 1, "Missing or ambiguous Cargo.lock package");
  const section = sections[0][0];
  requireValue(!/^source = /mu.test(section), "Release lock entry must be a workspace package");
  const fields = [...section.matchAll(/^version = "([^"]+)"$/gmu)];
  requireValue(fields.length === 1 && fields[0][1] === previous, "Manifest and Cargo.lock versions disagree");
  requireValue(!lock.includes(` "${project} ${previous}`), "Version-qualified workspace dependents require an explicit release contract");
  return lock.replace(section, section.replace(/^version = "[^"]+"$/mu, `version = "${next}"`));
}
function versionParts(version) {
  requireValue(typeof version === "string" && version.length <= 62 && semverPattern.test(version), "An exact stable MAJOR.MINOR.PATCH version is required");
  const parts = version.split(".").map(BigInt);
  requireValue(parts.every((part) => part <= 18446744073709551615n), "Version component overflow");
  return parts;
}
export function bumpVersion(version, bump) {
  requireValue(Object.values(Bump).includes(bump), "Unknown version bump");
  const parts = versionParts(version);
  const index = { major: 0, minor: 1, patch: 2 }[bump];
  parts[index] += 1n;
  for (let i = index + 1; i < parts.length; i++) parts[i] = 0n;
  const next = parts.join(".");
  versionParts(next);
  return next;
}

// These CLI manifests have explicit versions and no workspace dependents. Match
// complete TOML sections, never a dependency's version or an external lock entry.
// Reject ambiguous/new layouts until their release contract is explicitly added.
function replaceVersion(source, project, kind, next) {
  const pattern = kind === Kind.Rust
    ? /(^\[package\]\s*\n)([\s\S]*?)(?=^\[|$(?![\s\S]))/gmu
    : /^const Version = "([^"]+)"$/gmu;
  const matches = [...source.matchAll(pattern)];
  requireValue(matches.length === 1, "Missing or ambiguous source version declaration");
  if (kind === Kind.Go) {
    const current = matches[0][1];
    versionParts(current);
    return { current, text: next ? source.replace(pattern, `const Version = "${next}"`) : source };
  }
  const section = matches[0][2];
  requireValue([...section.matchAll(/^name = "([^"]+)"$/gmu)].length === 1 && section.includes(`name = "${project}"\n`), "Manifest package identity mismatch");
  const fields = [...section.matchAll(/^version = "([^"]+)"$/gmu)];
  requireValue(fields.length === 1, "Missing or ambiguous manifest version");
  const current = fields[0][1];
  versionParts(current);
  return { current, text: next ? source.replace(pattern, (_, header, body) => header + body.replace(/^version = "[^"]+"$/mu, `version = "${next}"`)) : source };
}

// Preserve every byte outside these release fields, including installer trust
// identities. New or ambiguous layouts need an explicit versioning contract.
function asyncCommitHookVersionChanges(read, current, next = current) {
  return Object.fromEntries(asyncCommitHookVersions.map(({ file, name, pattern }) => {
    const source = read(file);
    if (!pattern) {
      const metadata = JSON.parse(source);
      requireValue(name ? metadata.name === name : metadata.tag_prefix === `${Project.AsyncCommitHook}@v` && metadata.executable === "ach", `Release identity mismatch: ${file}`);
      requireValue([...source.matchAll(/"version"\s*:/gu)].length === 1, `Missing or ambiguous version declaration: ${file}`);
      requireValue(metadata.version === current, `async-commit-hook source versions disagree: ${file}`);
      pattern = /^(  "version": ")([^"\r\n]+)(",)$/gmu;
    }
    const matches = [...source.matchAll(pattern)];
    requireValue(matches.length === 1, `Missing or ambiguous version declaration: ${file}`);
    requireValue(matches[0][2] === current, `async-commit-hook source versions disagree: ${file}`);
    return [file, source.replace(pattern, (_, prefix, version, suffix) => `${prefix}${next}${suffix}`)];
  }));
}

export function readVersion(project, read = (file) => readFileSync(path.join(root, file), "utf8")) {
  const { file, kind } = descriptor(project);
  const current = replaceVersion(read(file), project, kind).current;
  if (project === Project.Clibox) {
    const npm = JSON.parse(read("packages/clibox/package.json"));
    requireValue(npm.name === "@delino/clibox" && npm.version === current, "clibox Cargo/npm versions disagree");
  }
  if (project === Project.Pnport) {
    const npm = JSON.parse(read("packages/pnport/package.json"));
    requireValue(npm.name === "@delino/pnport" && npm.version === current, "pnport Cargo/npm versions disagree");
    for (const name of ["pnport-core", "pnport-preload"]) requireValue(replaceVersion(read(`crates/${name}/Cargo.toml`), name, Kind.Rust).current === current, "pnport CLI/core/preload versions disagree");
  }
  if (project === Project.AsyncCommitHook) asyncCommitHookVersionChanges(read, current);
  return current;
}

export function versionChanges(project, bump, read) {
  const { file, kind } = descriptor(project);
  const previous_version = readVersion(project, read);
  if (project === Project.Pnport && previous_version === "0.0.0") {
    requireValue(bump === Bump.Minor, "pnport first public release requires a minor bump to 0.1.0");
  }
  const version = bumpVersion(previous_version, bump);
  const changes = { [file]: replaceVersion(read(file), project, kind, version).text };
  if (kind === Kind.Rust) {
    changes["Cargo.lock"] = replaceLockVersion(read("Cargo.lock"), project, previous_version, version);
  }
  if (project === Project.Clibox) {
    const file = "packages/clibox/package.json";
    const source = read(file);
    requireValue([...source.matchAll(/^  "version": "[^"]+",$/gmu)].length === 1, "Missing or ambiguous npm source version");
    changes[file] = source.replace(/^  "version": "[^"]+",$/mu, `  "version": "${version}",`);
  }
  if (project === Project.Pnport) {
    for (const name of ["pnport-core", "pnport-preload"]) {
      const file = `crates/${name}/Cargo.toml`;
      changes[file] = replaceVersion(read(file), name, Kind.Rust, version).text;
      changes["Cargo.lock"] = replaceLockVersion(changes["Cargo.lock"], name, previous_version, version);
    }
    const npm = "packages/pnport/package.json";
    const source = read(npm);
    requireValue([...source.matchAll(/^  "version": "[^"]+",$/gmu)].length === 1, "Missing or ambiguous pnport npm source version");
    changes[npm] = source.replace(/^  "version": "[^"]+",$/mu, `  "version": "${version}",`);
  }
  if (project === Project.AsyncCommitHook) Object.assign(changes, asyncCommitHookVersionChanges(read, previous_version, version));
  return { project, bump, kind, previous_version, version, tag: `${project}@v${version}`, changes };
}

export function sourceMetadata({ project, event, ref, requestedVersion, requestedDryRun }, read) {
  descriptor(project);
  requireValue(["push", "workflow_dispatch"].includes(event), "Unsupported release event");
  const dry_run = event === "push" ? "false" : requestedDryRun;
  requireValue(["true", "false"].includes(dry_run), "Invalid release dry-run mode");
  const version = event === "push" ? ref?.replace(`refs/tags/${project}@v`, "") : requestedVersion;
  versionParts(version);
  requireValue(readVersion(project, read) === version, "Requested release version does not match source");
  const tag = `${project}@v${version}`;
  requireValue(event !== "push" || ref === `refs/tags/${tag}`, "Release push must be the exact project tag");
  requireValue(dry_run === "true" || ref === "refs/heads/main" || ref === `refs/tags/${tag}`, "Publication requires main or the exact version tag");
  if (descriptor(project).kind === Kind.Rust) replaceLockVersion(read("Cargo.lock"), project, version, version);
  return { version, tag, dry_run };
}

export function git(directory, args, options = {}) {
  try {
    const { trim = true, ...execOptions } = options;
    const result = execFileSync("git", args, { cwd: directory, encoding: "utf8", stdio: ["pipe", "pipe", "pipe"], ...execOptions });
    return trim ? result.trim() : result;
  } catch {
    // Git transport failures may include credential-helper output. Report only
    // the operation; authentication and remote refusal remain hard failures.
    throw new Error(`Git ${args[0]} failed; inspect repository access or retry after resolving the conflict`);
  }
}
const authArgs = ["-c", "credential.helper=", "-c", "credential.helper=!gh auth git-credential"];

function validateRun(project, bump, runId) {
  descriptor(project);
  requireValue(Object.values(Bump).includes(bump), "Unknown version bump");
  requireValue(/^[1-9]\d{0,19}$/u.test(runId ?? ""), "Invalid release run ID");
}
function releaseMessage(plan, runId) {
  return `chore(release): ${plan.tag}\n\nRelease-Run: ${repository}/${runId}\nRelease-Project: ${plan.project}\nRelease-Bump: ${plan.bump}\nRelease-Previous-Version: ${plan.previous_version}`;
}
export function validateCommit(directory, revision, project, bump, runId) {
  validateRun(project, bump, runId);
  requireValue(shaPattern.test(revision ?? ""), "Invalid release commit");
  const parents = git(directory, ["show", "-s", "--format=%P", revision]).split(" ");
  requireValue(parents.length === 1 && shaPattern.test(parents[0]), "Release commit must have one parent");
  const plan = versionChanges(project, bump, (file) => git(directory, ["show", `${parents[0]}:${file}`], { trim: false }));
  requireValue(git(directory, ["show", "-s", "--format=%B", revision]) === releaseMessage(plan, runId), "Release journal does not match this run");
  const changed = git(directory, ["diff-tree", "--no-commit-id", "--name-only", "--no-renames", "-r", revision]).split("\n").sort();
  requireValue(JSON.stringify(changed) === JSON.stringify(Object.keys(plan.changes).sort()), "Release commit contains unexpected paths");
  for (const [file, text] of Object.entries(plan.changes)) {
    requireValue(git(directory, ["show", `${revision}:${file}`], { trim: false }) === text, "Release commit is not the exact version-only change");
  }
  const { changes, ...identity } = plan;
  return { ...identity, revision };
}

export async function prepareRelease({ directory, project, bump, runId, name, email, preflight = async () => {} }) {
  validateRun(project, bump, runId);
  requireValue(name === botName && /^\d+\+delino-release-bot\[bot\]@users\.noreply\.github\.com$/u.test(email ?? ""), "Unexpected release bot commit identity");
  requireValue(git(directory, ["status", "--porcelain"]) === "", "Release checkout must be clean");
  git(directory, ["fetch", "--no-tags", "origin", "refs/heads/main:refs/remotes/origin/main"]);
  const candidates = git(directory, ["log", "origin/main", "--format=%H", "--fixed-strings", `--grep=Release-Run: ${repository}/${runId}`])
    .split("\n").filter(Boolean).filter((sha) => git(directory, ["show", "-s", "--format=%B", sha]).split("\n").includes(`Release-Run: ${repository}/${runId}`));
  requireValue(candidates.length <= 1, "Multiple commits claim this release run");
  if (candidates.length === 1) {
    const identity = validateCommit(directory, candidates[0], project, bump, runId);
    git(directory, ["checkout", "--detach", identity.revision]);
    return { ...identity, resumed: true };
  }
  git(directory, ["checkout", "--detach", "origin/main"]);
  const plan = versionChanges(project, bump, (file) => readFileSync(path.join(directory, file), "utf8"));
  await preflight(plan);
  for (const [file, text] of Object.entries(plan.changes)) writeFileSync(path.join(directory, file), text);
  git(directory, ["config", "user.name", name]);
  git(directory, ["config", "user.email", email]);
  git(directory, ["add", "--", ...Object.keys(plan.changes)]);
  git(directory, ["commit", "--file=-"], { input: releaseMessage(plan, runId) + "\n" });
  const revision = git(directory, ["rev-parse", "HEAD"]);
  const identity = validateCommit(directory, revision, project, bump, runId);
  git(directory, [...authArgs, "push", "origin", `${revision}:refs/heads/main`]);
  return { ...identity, resumed: false };
}

export async function githubRequest(route) {
  requireValue(route.startsWith(`/repos/${repository}/`) || route === `/users/${encodeURIComponent(botName)}`, "Unsupported GitHub API route");
  requireValue(Boolean(process.env.GH_TOKEN), "A scoped GitHub token is required");
  let response;
  try {
    response = await fetch(`https://api.github.com${route}`, {
      headers: { Authorization: `Bearer ${process.env.GH_TOKEN}`, Accept: "application/vnd.github+json", "X-GitHub-Api-Version": "2022-11-28" },
      redirect: "error", signal: AbortSignal.timeout(30000),
    });
  } catch { throw new Error("GitHub API transport failed"); }
  requireValue([200, 404].includes(response.status), `GitHub API failed with HTTP ${response.status}`);
  if (response.status === 404) return { status: 404, body: null };
  try { return { status: 200, body: await response.json() }; }
  catch { throw new Error("Invalid GitHub API response"); }
}

export async function tagRevision(tag, request) {
  const result = await request(`/repos/${repository}/git/ref/tags/${encodeURIComponent(tag)}`);
  if (result.status === 404) return null;
  requireValue(result.status === 200, "Cannot establish tag ownership");
  let object = result.body?.object;
  for (let depth = 0; object?.type === "tag" && depth < 4; depth++) {
    requireValue(shaPattern.test(object.sha ?? ""), "Invalid annotated tag");
    const tagObject = await request(`/repos/${repository}/git/tags/${object.sha}`);
    requireValue(tagObject.status === 200, "Cannot resolve annotated tag");
    object = tagObject.body?.object;
  }
  requireValue(object?.type === "commit" && shaPattern.test(object.sha ?? ""), "Invalid release tag target");
  return object.sha;
}

export async function preflightVersion(plan, request) {
  requireValue(await tagRevision(plan.tag, request) === null, "Next version tag already exists");
  const release = await request(`/repos/${repository}/releases/tags/${encodeURIComponent(plan.tag)}`);
  requireValue(release.status === 404, "Next version release already exists or cannot be checked");
}

export async function pushReleaseTag({ directory, identity, request }) {
  const existing = await tagRevision(identity.tag, request);
  requireValue(existing === null || existing === identity.revision, "Existing release tag belongs to a different commit");
  if (existing === null) git(directory, [...authArgs, "push", "origin", `${identity.revision}:refs/tags/${identity.tag}`]);
  requireValue(await tagRevision(identity.tag, request) === identity.revision, "Remote release tag verification failed");
  return { tag: identity.tag, revision: identity.revision, reused: existing !== null };
}

function output(values) {
  if (process.env.GITHUB_OUTPUT) for (const [key, value] of Object.entries(values)) {
    requireValue(/^[a-z_]+$/u.test(key) && !/[\r\n]/u.test(String(value)), "Unsafe workflow output");
    appendFileSync(process.env.GITHUB_OUTPUT, `${key}=${value}\n`);
  }
  console.log(JSON.stringify(values));
}
function log(value) { console.error(JSON.stringify({ component: "release.project", ...value })); }
function workflowContext() {
  requireValue(process.env.GITHUB_REPOSITORY === repository && process.env.GITHUB_REF === "refs/heads/main" && process.env.GITHUB_EVENT_NAME === "workflow_dispatch", "Release coordinator requires a manual main run in delinoio/oss");
  const project = process.env.RELEASE_PROJECT;
  const bump = process.env.RELEASE_BUMP;
  const runId = process.env.GITHUB_RUN_ID;
  validateRun(project, bump, runId);
  return { directory: root, project, bump, runId };
}

export async function main(command) {
  if (command === "source") {
    output(sourceMetadata({ project: process.env.RELEASE_PROJECT, event: process.env.GITHUB_EVENT_NAME, ref: process.env.GITHUB_REF, requestedVersion: process.env.REQUESTED_VERSION, requestedDryRun: process.env.REQUESTED_DRY_RUN }, (file) => readFileSync(path.join(root, file), "utf8")));
    return;
  }
  if (command === "plan") {
    const { changes, ...plan } = versionChanges(process.env.RELEASE_PROJECT, process.env.RELEASE_BUMP, (file) => readFileSync(path.join(root, file), "utf8"));
    output({ ...plan, files: Object.keys(changes).join(",") });
    return;
  }
  if (command === "bot-identity") {
    const result = await githubRequest(`/users/${encodeURIComponent(botName)}`);
    requireValue(result.status === 200 && result.body?.login === botName && result.body.type === "Bot" && Number.isSafeInteger(result.body.id), "Cannot resolve release bot identity");
    output({ name: botName, email: `${result.body.id}+${botName}@users.noreply.github.com` });
    return;
  }
  const context = workflowContext();
  log({ phase: command, project: context.project, run_id: context.runId, outcome: "started" });
  if (command === "prepare") {
    output(await prepareRelease({ ...context, name: process.env.RELEASE_BOT_NAME, email: process.env.RELEASE_BOT_EMAIL, preflight: (plan) => preflightVersion(plan, githubRequest) }));
    return;
  }
  const identity = validateCommit(root, process.env.RELEASE_REVISION, context.project, context.bump, context.runId);
  requireValue(git(root, ["rev-parse", "HEAD"]) === identity.revision, "Checkout is not the release commit");
  if (command === "validate") {
    git(root, ["merge-base", "--is-ancestor", identity.revision, "origin/main"]);
    const existing = await tagRevision(identity.tag, githubRequest);
    requireValue(existing === null || existing === identity.revision, "Existing release tag belongs to a different commit");
    output(identity);
    return;
  }
  if (command === "tag") { output(await pushReleaseTag({ directory: root, identity, request: githubRequest })); return; }
  throw new Error("Unknown release command");
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main(process.argv[2]).catch((error) => {
    log({ phase: process.argv[2], outcome: "failed", message: error.message });
    process.exitCode = 1;
  });
}
