import { setTimeout as sleep } from "node:timers/promises";
import { parseArgs } from "node:util";
import path from "node:path";
import { ensure, event, isMain, npm, packageRoot, registry, revision } from "./common.mjs";
import { verifySet } from "./package.mjs";

export async function registryIntegrity(artifact, request = fetch) {
  const response = await request(`${registry}/${encodeURIComponent(artifact.name)}/${artifact.version}`, { redirect: "error", signal: AbortSignal.timeout(30000) });
  if (response.status === 404) return null;
  ensure(response.ok, `npm registry inspection failed for ${artifact.name}: HTTP ${response.status}`);
  const body = await response.json();
  ensure(body.name === artifact.name && body.version === artifact.version && typeof body.dist?.integrity === "string", "Invalid npm registry metadata");
  return body.dist.integrity;
}

export async function publishArtifacts(artifacts, { dryRun = true, lookup = registryIntegrity, publish, delay = sleep, report = event } = {}) {
  ensure(artifacts.length === 7 && artifacts.slice(0, 6).every(({ name }) => name !== "@delino/pnport") && artifacts[6].name === "@delino/pnport", "Native packages must precede the launcher");
  if (dryRun) { report("publish_dry_run", { packages: artifacts }); return; }
  ensure(typeof publish === "function", "Publisher required");
  // Validate the complete remote set before the first irreversible npm write.
  for (const artifact of artifacts) {
    const found = await lookup(artifact);
    ensure(found === null || found === artifact.integrity, `Conflicting npm integrity: ${artifact.name}`);
  }
  for (const artifact of artifacts) {
    let found = await lookup(artifact);
    ensure(found === null || found === artifact.integrity, `Conflicting npm integrity: ${artifact.name}`);
    const reused = found !== null;
    if (!reused) {
      await publish(artifact);
      // npm's post-upload scanning delays metadata visibility. Never repeat an
      // accepted immutable upload while polling readback.
      for (let attempt = 0; attempt <= 120; attempt++) {
        found = await lookup(artifact);
        if (found !== null) break;
        report("publish_pending", { name: artifact.name, version: artifact.version, attempt: attempt + 1 });
        if (attempt < 120) await delay(10000);
      }
    }
    ensure(found === artifact.integrity, `npm readback did not confirm ${artifact.name}`);
    report("publish", { name: artifact.name, version: artifact.version, integrity: artifact.integrity, reused });
  }
}

export async function main() {
  const { values } = parseArgs({ options: { directory: { type: "string", default: path.join(packageRoot, "dist") }, publish: { type: "boolean", default: false } } });
  const sourceRevision = revision();
  const artifacts = verifySet(values.directory, sourceRevision).packages;
  if (values.publish) {
    ensure(process.env.GITHUB_REPOSITORY === "delinoio/oss" && process.env.GITHUB_REF === `refs/tags/pnport@v${artifacts[0].version}` && process.env.GITHUB_SHA === sourceRevision, "Publication requires the exact first-party tag and commit");
    ensure(process.env.ACTIONS_ID_TOKEN_REQUEST_URL && process.env.ACTIONS_ID_TOKEN_REQUEST_TOKEN, "npm OIDC authority required");
  }
  await publishArtifacts(artifacts, {
    dryRun: !values.publish,
    publish: (artifact) => {
      try {
        npm(["publish", path.resolve(values.directory, "tarballs", artifact.filename), "--access", "public", "--provenance", "--ignore-scripts", "--registry", registry]);
      } catch { throw new Error(`npm publication failed for ${artifact.name}; recover using the identical retained candidate`); }
    },
  });
}

if (isMain(import.meta.url)) main().catch((error) => { console.error(JSON.stringify({ event: "pnport_publish_failed", message: error.message })); process.exitCode = 1; });
