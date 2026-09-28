import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { archiveNames, assetManifest, githubRequest, verify } from "./runmoor.mjs";

const root = fileURLToPath(new URL("../..", import.meta.url));
const api = "/repos/delinoio/oss";
const macArchive = "runmoor-darwin-arm64.tar.gz";
const unsignedNames = [...archiveNames, "SHA256SUMS"];
const assetNames = [...unsignedNames, ...unsignedNames.map((name) => `${name}.sigstore.json`)].sort();
const digest = (bytes) => `sha256:${createHash("sha256").update(bytes).digest("hex")}`;
function ensure(condition, code) { if (!condition) throw Object.assign(new Error(code), { code }); }
function log(plan, stage, releaseId = null, code = null) {
  console.error(JSON.stringify({ event: "runmoor.homebrew", stage, tag: plan.tag, release_id: releaseId, code }));
}

export function homebrewPlan({ version, revision }) {
  ensure(/^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/u.test(version ?? ""), "INVALID_VERSION");
  ensure(/^[a-f0-9]{40}$/u.test(revision ?? "") && !/^0+$/u.test(revision), "INVALID_REVISION");
  return { version, revision, tag: `runmoor@v${version}` };
}

export function publicationContext(plan, env = process.env) {
  const sourceAllowed = env.GITHUB_REF === "refs/heads/main" || (env.GITHUB_REF === `refs/tags/${plan.tag}` && env.GITHUB_SHA === plan.revision);
  ensure(env.GITHUB_ACTIONS === "true" && env.GITHUB_REPOSITORY === "delinoio/oss" && sourceAllowed, "UNAUTHORIZED_PUBLICATION");
}

export async function publishedRelease(plan, request) {
  const get = async (endpoint) => {
    const response = await request(`${api}${endpoint}`);
    ensure(response.status === 200, "RELEASE_LOOKUP_FAILED");
    return response.body;
  };
  let object = (await get(`/git/ref/tags/${encodeURIComponent(plan.tag)}`)).object;
  for (let depth = 0; object?.type === "tag" && depth < 4; depth++) {
    ensure(/^[a-f0-9]{40}$/u.test(object.sha ?? ""), "INVALID_TAG_OBJECT");
    object = (await get(`/git/tags/${object.sha}`)).object;
  }
  ensure(object?.type === "commit" && object.sha === plan.revision, "TAG_REVISION_MISMATCH");
  const source = await get(`/contents/cmds/runmoor/internal/runmoor/types.go?ref=${plan.revision}`);
  ensure(source.encoding === "base64" && typeof source.content === "string", "INVALID_SOURCE_RESPONSE");
  ensure(Buffer.from(source.content, "base64").toString().match(/^const Version = "([^"]+)"$/mu)?.[1] === plan.version, "SOURCE_VERSION_MISMATCH");
  // This distribution consumes only public releases; the published-by-tag API is intentional.
  const release = await get(`/releases/tags/${encodeURIComponent(plan.tag)}`);
  ensure(Number.isSafeInteger(release.id) && release.id > 0 && release.tag_name === plan.tag && release.target_commitish === plan.revision && release.draft === false && release.prerelease === false, "RELEASE_IDENTITY_MISMATCH");
  ensure(Array.isArray(release.assets) && JSON.stringify(release.assets.map((asset) => asset.name).sort()) === JSON.stringify(assetNames), "RELEASE_ASSET_INVENTORY");
  ensure(release.assets.every((asset) => asset.state === "uploaded" && Number.isSafeInteger(asset.size) && asset.size > 0 && asset.size <= 100 * 1024 * 1024 && /^sha256:[a-f0-9]{64}$/u.test(asset.digest ?? "")), "RELEASE_ASSET_METADATA");
  log(plan, "public-release-verified", release.id);
  return release;
}

export function verifySignature(file, bundle, plan) {
  const escape = (value) => value.replace(/[.*+?^${}()|[\]\\]/gu, "\\$&");
  const workflow = "https://github.com/delinoio/oss/.github/workflows/release-runmoor.yml";
  // Both existing release entrypoints are allowed, but the certificate must bind
  // the same repository and exact source commit as the published tag.
  execFileSync("cosign", ["verify-blob", "--bundle", bundle,
    "--certificate-identity-regexp", `^${escape(workflow)}@(?:refs/tags/${escape(plan.tag)}|refs/heads/main)$`,
    "--certificate-oidc-issuer", "https://token.actions.githubusercontent.com",
    "--certificate-github-workflow-sha", plan.revision,
    "--certificate-github-workflow-repository", "delinoio/oss", file], { stdio: "inherit" });
}

export function renderHomebrew(plan, directory, publish = false) {
  const sha = createHash("sha256").update(readFileSync(path.join(directory, macArchive))).digest("hex");
  const args = [path.join(root, "scripts/release/update-homebrew.sh"), "--project", "runmoor", "--version", plan.version,
    "--darwin-arm64-url", `https://github.com/delinoio/oss/releases/download/${plan.tag}/${macArchive}`, "--darwin-arm64-sha256", sha];
  if (!publish) args.push("--dry-run");
  return execFileSync("bash", args, { cwd: root, encoding: "utf8", stdio: ["ignore", "pipe", "inherit"] });
}

export async function prepareHomebrew(plan, directory, {
  request = githubRequest(process.env.GH_TOKEN),
  download = (url) => execFileSync("curl", ["--fail", "--location", "--silent", "--show-error", "--max-time", "120", "--proto", "=https", "--proto-redir", "=https", url], { maxBuffer: 100 * 1024 * 1024 }),
  verifyBlob = verifySignature,
} = {}) {
  const release = await publishedRelease(plan, request);
  for (const asset of release.assets) {
    const bytes = await download(`https://github.com/delinoio/oss/releases/download/${encodeURIComponent(plan.tag)}/${asset.name}`);
    ensure(bytes.length === asset.size && digest(bytes) === asset.digest, "ASSET_DIGEST_MISMATCH");
    writeFileSync(path.join(directory, asset.name), bytes, { flag: "wx" });
  }
  assetManifest(directory, true);
  verify(directory, true, {
    identity: `https://github.com/delinoio/oss/.github/workflows/release-runmoor.yml@refs/tags/${plan.tag}`,
    verifyBlob: (file, bundle) => verifyBlob(file, bundle, plan),
  });
  log(plan, "signed-assets-verified", release.id);
  return renderHomebrew(plan, directory);
}

export async function main(args, env = process.env) {
  const [command, ...rest] = args;
  ensure(["render", "prepare", "publish"].includes(command), "INVALID_COMMAND");
  const flags = {};
  for (let i = 0; i < rest.length; i += 2) {
    const key = rest[i]?.replace(/^--/u, "");
    ensure(rest[i]?.startsWith("--") && ["version", "revision", "output", "archives"].includes(key) && rest[i + 1] && !(key in flags), "INVALID_OPTIONS");
    flags[key] = rest[i + 1];
  }
  const plan = homebrewPlan(flags);
  ensure(flags.output, "OUTPUT_REQUIRED");
  if (command === "render") {
    ensure(flags.archives, "ARCHIVES_REQUIRED");
    verify(flags.archives);
    writeFileSync(flags.output, renderHomebrew(plan, flags.archives));
    return;
  }
  if (command === "publish") {
    publicationContext(plan, env);
    ensure(env.HOMEBREW_TAP_GH_TOKEN, "TAP_TOKEN_REQUIRED");
  }
  const directory = mkdtempSync(path.join(tmpdir(), "runmoor-homebrew-"));
  try {
    const formula = await prepareHomebrew(plan, directory);
    if (command === "prepare") writeFileSync(flags.output, formula);
    else {
      // Native installation tested this exact formula before tap credentials were
      // created. Re-download and verify before writing; never publish changed bytes.
      ensure(readFileSync(flags.output, "utf8") === formula, "VALIDATED_FORMULA_CHANGED");
      process.stdout.write(renderHomebrew(plan, directory, true));
      log(plan, "tap-published");
    }
  } finally { rmSync(directory, { recursive: true, force: true }); }
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main(process.argv.slice(2)).catch((error) => {
    console.error(JSON.stringify({ event: "runmoor.homebrew", stage: "failed", code: error.code ?? "HOMEBREW_FAILED" }));
    process.exitCode = 1;
  });
}
