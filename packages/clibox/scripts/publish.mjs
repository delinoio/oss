import { setTimeout as sleep } from "node:timers/promises";
import { parseArgs } from "node:util";
import path from "node:path";
import { ensure, event, isMain, npm, packageRoot, registry, revision } from "./common.mjs";
import { verifySet } from "./package.mjs";

export async function registryIntegrity(artifact, request = fetch) {
  const response = await request(`${registry}/${encodeURIComponent(artifact.name)}/${artifact.version}`, { redirect: "error", signal: AbortSignal.timeout(30000) });
  if (response.status === 404) return null;
  ensure(response.ok, `Cannot inspect npm package ${artifact.name}: HTTP ${response.status}`);
  const body = await response.json();
  ensure(body.name === artifact.name && body.version === artifact.version && typeof body.dist?.integrity === "string", `Invalid registry metadata for ${artifact.name}`);
  return body.dist.integrity;
}

export async function publishArtifacts(artifacts, { dryRun = true, lookup = registryIntegrity, publish, delay = sleep, report = event } = {}) {
  // Validate every existing remote version before the first write, including the
  // main package. npm cannot overwrite versions after a partial publication.
  if (dryRun) { report("publish_dry_run", { packages: artifacts }); return; }
  ensure(typeof publish === "function", "A publisher is required");
  for (const artifact of artifacts) {
    const found = await lookup(artifact);
    ensure(found === null || found === artifact.integrity, `Conflicting published integrity: ${artifact.name}@${artifact.version}`);
  }
  for (const artifact of artifacts) {
    let found = await lookup(artifact);
    ensure(found === null || found === artifact.integrity, `Conflicting published integrity: ${artifact.name}@${artifact.version}`);
    const reused = found !== null;
    if (!reused) {
      await publish(artifact);
      for (let attempt = 0; attempt < 10; attempt++) {
        found = await lookup(artifact);
        if (found !== null) break;
        if (attempt < 9) await delay(3000);
      }
    }
    ensure(found === artifact.integrity, `Published package integrity was not confirmed: ${artifact.name}@${artifact.version}`);
    report("publish", { name: artifact.name, version: artifact.version, integrity: artifact.integrity, reused });
  }
}

export async function main() {
  const { values } = parseArgs({ options: { directory: { type: "string", default: path.join(packageRoot, "dist/tarballs") }, publish: { type: "boolean", default: false } } });
  const sourceRevision = revision();
  const artifacts = verifySet(values.directory, sourceRevision);
  if (values.publish) {
    ensure(process.env.GITHUB_REPOSITORY === "delinoio/oss" && process.env.GITHUB_REF === `refs/tags/clibox@v${artifacts[0].version}` && process.env.GITHUB_SHA === sourceRevision, "npm publication requires the exact first-party release tag and commit");
    ensure(process.env.CLIBOX_NPM_PUBLISH_ENABLED === "true", "npm trusted publishing is not enabled");
    ensure(process.env.ACTIONS_ID_TOKEN_REQUEST_URL && process.env.ACTIONS_ID_TOKEN_REQUEST_TOKEN, "npm publication requires GitHub Actions OIDC");
  }
  await publishArtifacts(artifacts, {
    dryRun: !values.publish,
    publish: (artifact) => {
      try {
        npm(["publish", path.resolve(values.directory, artifact.filename), "--access", "public", "--provenance", "--ignore-scripts", "--registry", registry]);
      } catch { throw new Error(`npm publication failed for ${artifact.name}; inspect npm permissions and rerun the failed job using the retained artifacts`); }
    },
  });
}

if (isMain(import.meta.url)) main().catch((error) => { console.error(JSON.stringify({ event: "clibox_publish_failed", message: error.message })); process.exitCode = 1; });
