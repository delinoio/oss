import { execFileSync } from "node:child_process";
import path from "node:path";
import { parseArgs } from "node:util";
import { fileURLToPath } from "node:url";
import { setTimeout as sleep } from "node:timers/promises";
import { ensure, packageRoot, registry, sourceRevision, verifySet } from "./package.mjs";

export async function registryIntegrity(artifact, request = fetch) {
  const response = await request(`${registry}/${encodeURIComponent(artifact.name)}/${artifact.version}`, { redirect: "error", signal: AbortSignal.timeout(30000) });
  if (response.status === 404) return null;
  ensure(response.ok, `npm registry lookup failed for ${artifact.name}: HTTP ${response.status}`);
  const metadata = await response.json();
  ensure(metadata.name === artifact.name && metadata.version === artifact.version && typeof metadata.dist?.integrity === "string", "Invalid npm registry metadata");
  return metadata.dist.integrity;
}

export async function publishArtifacts(artifacts, { lookup = registryIntegrity, publish, delay = sleep, report = console.log } = {}) {
  ensure(artifacts.length === 7 && artifacts.at(-1).name === "@delino/react-forge", "Native packages must precede the main package");
  ensure(typeof publish === "function", "Publisher required");
  const existing = await Promise.all(artifacts.map((artifact) => lookup(artifact)));
  for (let i = 0; i < artifacts.length; i++) ensure(existing[i] === null || existing[i] === artifacts[i].integrity, `Conflicting published integrity: ${artifacts[i].name}`);
  for (const artifact of artifacts) {
    let found = await lookup(artifact);
    ensure(found === null || found === artifact.integrity, `Conflicting published integrity: ${artifact.name}`);
    if (found === null) {
      await publish(artifact);
      // Registry scanning can delay metadata visibility. Never upload the same
      // immutable version twice during one attempt.
      for (let attempt = 0; attempt <= 120; attempt++) {
        found = await lookup(artifact);
        if (found !== null) break;
        report(JSON.stringify({ event: "react_forge_publish_pending", name: artifact.name, attempt: attempt + 1 }));
        if (attempt < 120) await delay(10000);
      }
    }
    ensure(found === artifact.integrity, `Published integrity was not confirmed: ${artifact.name}`);
    report(JSON.stringify({ event: "react_forge_publish_confirmed", name: artifact.name, version: artifact.version }));
  }
}

export async function main() {
  const { values } = parseArgs({ options: { output: { type: "string", default: path.join(packageRoot, "dist/release") }, publish: { type: "boolean", default: false } } });
  const artifacts = verifySet(values.output, sourceRevision());
  if (!values.publish) {
    console.log(JSON.stringify({ event: "react_forge_publish_dry_run", packages: artifacts }));
    return;
  }
  ensure(process.env.GITHUB_REPOSITORY === "delinoio/oss" && process.env.GITHUB_REF === `refs/tags/react-forge@v${artifacts[0].version}` && process.env.GITHUB_SHA === sourceRevision(), "Publication requires the exact first-party tag and commit");
  ensure(process.env.ACTIONS_ID_TOKEN_REQUEST_URL && process.env.ACTIONS_ID_TOKEN_REQUEST_TOKEN, "GitHub Actions OIDC is required");
  await publishArtifacts(artifacts, {
    publish: (artifact) => {
      const file = path.resolve(values.output, "tarballs", artifact.filename);
      const windows = process.platform === "win32";
      const command = windows ? process.execPath : "npm";
      const prefix = windows ? [path.join(path.dirname(process.execPath), "node_modules/npm/bin/npm-cli.js")] : [];
      execFileSync(command, [...prefix, "publish", file, "--access", "public", "--provenance", "--ignore-scripts", "--registry", registry], { stdio: "inherit" });
    },
  });
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) main().catch((error) => {
  console.error(JSON.stringify({ event: "react_forge_publish_failed", message: error.message }));
  process.exitCode = 1;
});
