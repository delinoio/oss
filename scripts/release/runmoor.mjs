import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { gzipSync, gunzipSync } from "node:zlib";

const root = fileURLToPath(new URL("../..", import.meta.url));
export const platforms = ["darwin-arm64", "linux-amd64", "linux-arm64"];
export const archiveNames = platforms.map((platform) => `runmoor-${platform}.tar.gz`);
const versionPattern = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/u;
const releaseAssetNames = [...archiveNames, "SHA256SUMS"];
const repositoryApi = "/repos/delinoio/oss";

function publicationError(code, message) {
  return Object.assign(new Error(message), { code });
}

export function releaseLog(plan, stage, releaseId = null, code = null) {
  console.error(JSON.stringify({ event: "runmoor.release", stage, tag: plan.tag, release_id: releaseId, code }));
}

export function githubRequest(token, fetchImpl = fetch) {
  return async (apiPath, { method = "GET", body } = {}) => {
    try {
      const response = await fetchImpl(`https://api.github.com${apiPath}`, {
        method, headers: { Authorization: `Bearer ${token}`, Accept: "application/vnd.github+json", "Content-Type": "application/json" },
        body: body === undefined ? undefined : JSON.stringify(body), redirect: "error", signal: AbortSignal.timeout(30000),
      });
      return { status: response.status, body: response.ok ? await response.json() : {} };
    } catch {
      // Never include upstream bodies or transport errors, which may contain credentials.
      throw publicationError("REQUEST_FAILED", "GitHub release request failed; inspect remote state before retrying");
    }
  };
}

export function releasePlan({ version, revision, ref, mode = "dry-run" }) {
  if (!versionPattern.test(version ?? "")) throw new Error("An exact MAJOR.MINOR.PATCH version is required");
  if (!/^[0-9a-f]{40}$/u.test(revision ?? "")) throw new Error("An exact lowercase 40-hex source revision is required");
  if (!["dry-run", "publish"].includes(mode)) throw new Error("Unknown release mode");
  const source = readFileSync(path.join(root, "cmds/runmoor/internal/runmoor/types.go"), "utf8");
  const sourceVersion = source.match(/const Version = "([^"]+)"/u)?.[1];
  if (sourceVersion !== version) throw new Error("Release version does not match Runmoor source");
  const tag = `runmoor@v${version}`;
  if (ref?.startsWith("refs/tags/") && ref !== `refs/tags/${tag}`) throw new Error("Tag and source version do not match");
  if (mode === "publish" && ref !== "refs/heads/main" && ref !== `refs/tags/${tag}`) throw new Error("Publication requires main or the exact version tag");
  return { schema_version: 1, project: "runmoor", version, revision, tag, mode, prerelease: false,
    platforms, archives: archiveNames, checksums: "SHA256SUMS",
    signatures: [...archiveNames, "SHA256SUMS"].map((name) => `${name}.sigstore.json`),
    verification_gaps: ["Live GitHub repository/organization and App/PAT compatibility", "Real Tart local execution", "Actual macOS host execution and unsigned Xcode builds"],
  };
}

function sameAssetManifest(actual, expected) {
  if (!Array.isArray(actual) || !Array.isArray(expected) || actual.length !== expected.length) return false;
  const normalize = (asset) => ({ name: asset?.name, size: asset?.size, digest: asset?.digest });
  const sort = (left, right) => String(left.name).localeCompare(String(right.name));
  return JSON.stringify(actual.map(normalize).sort(sort)) === JSON.stringify(expected.map(normalize).sort(sort));
}

async function checkTag(plan, request) {
  if (plan.mode !== "publish") throw new Error("Remote publication checks are unavailable in dry-run mode");
  const prefix = repositoryApi;
  const tag = await request(`${prefix}/git/ref/tags/${encodeURIComponent(plan.tag)}`);
  if (tag.status !== 404) {
    if (tag.status !== 200) throw new Error("Cannot establish remote tag ownership");
    let object = tag.body.object;
    for (let depth = 0; object?.type === "tag" && depth < 4; depth++) {
      if (!/^[0-9a-f]{40}$/u.test(object.sha ?? "")) throw new Error("Invalid annotated tag identity");
      const annotated = await request(`${prefix}/git/tags/${object.sha}`);
      if (annotated.status !== 200) throw new Error("Cannot resolve annotated release tag");
      object = annotated.body.object;
    }
    if (object?.type !== "commit" || object.sha !== plan.revision) throw new Error("Existing release tag belongs to a different source revision");
  }
}

function requireDraft(plan, release, releaseId, expectedAssets) {
  if (!Number.isSafeInteger(release?.id) || release.id <= 0 || release.id !== releaseId) throw publicationError("RELEASE_ID_CHANGED", "Release draft identity changed");
  if (release.draft !== true) throw publicationError("ALREADY_PUBLIC", "A public release already exists; immutable artifacts cannot be overwritten");
  if (release.tag_name !== plan.tag || release.prerelease !== false || release.target_commitish !== plan.revision) throw publicationError("DRAFT_SOURCE_MISMATCH", "Existing release draft is not bound to the requested stable tag and source revision");
  // Recovery first downloads and verifies existing signatures. Only the complete verified manifest may authorize publication.
  if (expectedAssets !== undefined && (!sameAssetManifest(release.assets, expectedAssets) || release.assets.some((asset) => asset.state !== "uploaded"))) throw publicationError("ASSET_MISMATCH", "Existing release draft assets do not match the verified release asset inventory");
  return release;
}

export async function checkPublication(plan, request, expectedAssets, releaseId) {
  try {
    await checkTag(plan, request);
    if (releaseId === undefined) {
      // GitHub's tag endpoint returns published releases only. Enumerate all pages
      // to discover drafts and reject ambiguous same-tag candidates before signing.
      const matches = [];
      for (let page = 1; ; page++) {
        const result = await request(`${repositoryApi}/releases?per_page=100&page=${page}`);
        if (result.status !== 200 || !Array.isArray(result.body) || result.body.length > 100 || result.body.some((release) => !Number.isSafeInteger(release?.id) || release.id <= 0 || typeof release.tag_name !== "string")) throw publicationError("RELEASE_LIST_FAILED", "Cannot establish whether a release already exists");
        matches.push(...result.body.filter((release) => release?.tag_name === plan.tag));
        if (matches.length > 1) throw publicationError("DUPLICATE_RELEASE", "Multiple releases use the requested tag");
        if (result.body.length < 100) break;
        // Bound a broken/repeating pagination response without treating it as absence.
        if (page === 1000) throw publicationError("RELEASE_LIST_LIMIT", "Release pagination limit reached; cannot establish release ownership");
      }
      if (matches.length === 0) {
        releaseLog(plan, "draft-absent");
        return null;
      }
      releaseId = matches[0].id;
      requireDraft(plan, matches[0], releaseId);
    }
    if (!Number.isSafeInteger(releaseId) || releaseId <= 0) throw publicationError("INVALID_RELEASE_ID", "A positive release ID is required");
    const result = await request(`${repositoryApi}/releases/${releaseId}`);
    if (result.status !== 200) throw publicationError("DRAFT_LOOKUP_FAILED", "Cannot retrieve the pinned release draft");
    const release = requireDraft(plan, result.body, releaseId, expectedAssets);
    releaseLog(plan, "draft-verified", releaseId);
    return release;
  } catch (error) {
    releaseLog(plan, "draft-check-failed", releaseId, error.code ?? "PUBLICATION_CHECK_FAILED");
    throw error;
  }
}

export async function publishDraft(plan, request, { releaseId, expectedAssets, notes }) {
  try {
    if (!Number.isSafeInteger(releaseId) || releaseId <= 0) throw publicationError("INVALID_RELEASE_ID", "A positive release ID is required");
    const names = [...releaseAssetNames, ...releaseAssetNames.map((name) => `${name}.sigstore.json`)].sort();
    if (!Array.isArray(expectedAssets) || JSON.stringify(expectedAssets.map((asset) => asset.name).sort()) !== JSON.stringify(names)) throw publicationError("ASSET_MISMATCH", "Publication requires the complete verified asset manifest");
    // The pinned ID alone cannot detect a second same-tag draft created since
    // preflight. Rediscover all candidates immediately before the irreversible write.
    const draft = await checkPublication(plan, request, expectedAssets);
    if (draft?.id !== releaseId) throw publicationError("RELEASE_ID_CHANGED", "The pinned draft is no longer the sole release for this tag");
    releaseLog(plan, "publishing", releaseId);
    const result = await request(`${repositoryApi}/releases/${releaseId}`, { method: "PATCH", body: { draft: false, prerelease: false, body: notes } });
    const release = result.body;
    if (result.status !== 200 || release?.id !== releaseId || release.tag_name !== plan.tag || release.target_commitish !== plan.revision || release.draft !== false || release.prerelease !== false || !sameAssetManifest(release.assets, expectedAssets)) throw publicationError("PUBLICATION_UNCONFIRMED", "Publication outcome is unconfirmed; inspect the pinned release before retrying");
    releaseLog(plan, "published", releaseId);
    return release;
  } catch (error) {
    releaseLog(plan, "publish-failed", releaseId, error.code ?? "PUBLICATION_FAILED");
    throw error;
  }
}

export function assetManifest(directory, signed = false) {
  const expectedNames = signed ? [...releaseAssetNames, ...releaseAssetNames.map((name) => `${name}.sigstore.json`)] : releaseAssetNames;
  const names = readdirSync(directory).sort();
  if (JSON.stringify(names) !== JSON.stringify([...expectedNames].sort())) throw new Error(`Unexpected files in ${signed ? "signed" : "unsigned"} artifact directory`);
  return expectedNames.map((name) => {
    const bytes = readFileSync(path.join(directory, name));
    return { name, size: bytes.length, digest: `sha256:${createHash("sha256").update(bytes).digest("hex")}` };
  });
}

// A small, fixed-name ustar writer avoids platform-specific GNU/BSD tar flags
// and produces identical metadata on macOS and Linux. Payloads are controlled
// release files, never user-supplied archive paths.
export function archive(entries) {
  const blocks = [];
  for (const { name, data, mode } of entries) {
    if (!["runmoor", "README.md", "LICENSE"].includes(name)) throw new Error("Unexpected archive entry");
    const header = Buffer.alloc(512);
    header.write(name, 0, 100, "utf8");
    const octal = (value, offset, width) => header.write(`${value.toString(8).padStart(width - 1, "0")}\0`, offset, width, "ascii");
    octal(mode, 100, 8); octal(0, 108, 8); octal(0, 116, 8); octal(data.length, 124, 12); octal(0, 136, 12);
    header.fill(32, 148, 156); header[156] = 48; header.write("ustar\0", 257, 6); header.write("00", 263, 2);
    const sum = header.reduce((total, byte) => total + byte, 0);
    header.write(`${sum.toString(8).padStart(6, "0")}\0 `, 148, 8, "ascii");
    blocks.push(header, data, Buffer.alloc((512 - data.length % 512) % 512));
  }
  blocks.push(Buffer.alloc(1024));
  return gzipSync(Buffer.concat(blocks), { level: 9 });
}

export function inspectArchive(bytes) {
  const body = gunzipSync(bytes); const names = []; let offset = 0;
  while (offset + 512 <= body.length) {
    const header = body.subarray(offset, offset + 512);
    if (header.every((byte) => byte === 0)) break;
    const name = header.subarray(0, 100).toString().split("\0")[0];
    const size = Number.parseInt(header.subarray(124, 136).toString().replaceAll("\0", "").trim(), 8);
    if (!Number.isSafeInteger(size) || size <= 0 || offset + 512 + size > body.length) throw new Error("Invalid archive payload");
    if (header[156] !== 48) throw new Error("Release archives must contain regular files only");
    if (name === "runmoor" && Number.parseInt(header.subarray(100, 108).toString(), 8) !== 0o755) throw new Error("Binary is not executable");
    names.push(name); offset += 512 + Math.ceil(size / 512) * 512;
  }
  if (JSON.stringify(names) !== JSON.stringify(["runmoor", "README.md", "LICENSE"])) throw new Error("Unexpected release archive inventory");
  return names;
}

export function buildRelease(plan, output, selected = platforms) {
  mkdirSync(output, { recursive: true });
  const temp = mkdtempSync(path.join(tmpdir(), "runmoor-build-"));
  try {
    for (const platform of selected) {
      if (!platforms.includes(platform)) throw new Error("Unsupported release platform");
      const [GOOS, GOARCH] = platform.split("-");
      const binary = path.join(temp, "runmoor");
      execFileSync("go", ["build", "-trimpath", "-buildvcs=false", "-ldflags", `-s -w -X github.com/delinoio/oss/cmds/runmoor/internal/runmoor.Revision=${plan.revision}`, "-o", binary, "./cmds/runmoor"],
        { cwd: root, stdio: "inherit", env: { ...process.env, GOOS, GOARCH, CGO_ENABLED: "0" } });
      const bytes = archive([
        { name: "runmoor", data: readFileSync(binary), mode: 0o755 },
        { name: "README.md", data: readFileSync(path.join(root, "cmds/runmoor/README.md")), mode: 0o644 },
        { name: "LICENSE", data: readFileSync(path.join(root, "LICENSE")), mode: 0o644 },
      ]);
      inspectArchive(bytes);
      writeFileSync(path.join(output, `runmoor-${platform}.tar.gz`), bytes);
    }
  } finally { rmSync(temp, { recursive: true, force: true }); }
}
export function checksums(directory) {
  const files = readdirSync(directory).sort();
  if (files.some((name) => ![...archiveNames, "SHA256SUMS"].includes(name))) throw new Error("Unexpected files in unsigned artifact directory");
  const lines = [...archiveNames].sort().map((name) => {
    const bytes = readFileSync(path.join(directory, name)); inspectArchive(bytes);
    return `${createHash("sha256").update(bytes).digest("hex")}  ${name}`;
  });
  const content = `${lines.join("\n")}\n`; writeFileSync(path.join(directory, "SHA256SUMS"), content); return content;
}
export function verify(directory, signed = false, { identity, verifyBlob = verifyWithCosign } = {}) {
  const names = [...archiveNames, "SHA256SUMS"];
  const expected = signed ? [...names, ...names.map((name) => `${name}.sigstore.json`)] : names;
  if (JSON.stringify(readdirSync(directory).sort()) !== JSON.stringify(expected.sort())) throw new Error("Artifact inventory does not match release contract");
  const expectedChecksums = [...archiveNames].sort().map((name) => {
    const bytes = readFileSync(path.join(directory, name)); inspectArchive(bytes);
    return `${createHash("sha256").update(bytes).digest("hex")}  ${name}`;
  }).join("\n") + "\n";
  if (readFileSync(path.join(directory, "SHA256SUMS"), "utf8") !== expectedChecksums) throw new Error("Checksum mismatch");
  if (signed && !/^https:\/\/github\.com\/delinoio\/oss\/\.github\/workflows\/release-runmoor\.yml@refs\/(?:heads\/main|tags\/runmoor@v\d+\.\d+\.\d+)$/u.test(identity ?? "")) throw new Error("An exact authorized signing workflow identity is required");
  if (signed) for (const name of names) {
    const bundle = JSON.parse(readFileSync(path.join(directory, `${name}.sigstore.json`), "utf8"));
    if (!bundle.mediaType || !bundle.verificationMaterial || (!bundle.messageSignature && !bundle.dsseEnvelope)) throw new Error("Invalid Sigstore bundle structure");
    verifyBlob(path.join(directory, name), path.join(directory, `${name}.sigstore.json`), identity);
  }
}
function verifyWithCosign(artifact, bundle, identity) {
  execFileSync("cosign", ["verify-blob", "--bundle", bundle, "--certificate-identity", identity,
    "--certificate-oidc-issuer", "https://token.actions.githubusercontent.com", artifact], { stdio: "inherit" });
}
function options(args) {
  const out = {};
  for (let i = 0; i < args.length; i += 2) {
    if (!args[i]?.startsWith("--") || args[i + 1] === undefined) throw new Error("Options require --name value pairs");
    const key = args[i].slice(2);
    if (!["version", "revision", "ref", "mode", "output", "platform", "signed", "identity"].includes(key) || key in out) throw new Error("Unknown or duplicate release option");
    out[key] = args[i + 1];
  }
  return out;
}
export function main(args) {
  const [command, ...rest] = args; const flags = options(rest);
  if (!flags.output) throw new Error("An explicit output directory/file is required");
  if (command === "checksums") return checksums(flags.output);
  if (command === "verify") return verify(flags.output, flags.signed === "true", { identity: flags.identity });
  const plan = releasePlan(flags);
  if (command === "plan") { mkdirSync(path.dirname(flags.output), { recursive: true }); writeFileSync(flags.output, `${JSON.stringify(plan, null, 2)}\n`); return plan; }
  if (command === "build") return buildRelease(plan, flags.output, flags.platform ? [flags.platform] : platforms);
  throw new Error("Unknown release command");
}
if (process.argv[1] && existsSync(process.argv[1]) && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try { main(process.argv.slice(2)); } catch (error) { console.error(`runmoor.release: ${error.message}`); process.exitCode = 1; }
}
