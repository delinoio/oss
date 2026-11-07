// SPDX-License-Identifier: Apache-2.0
import { setTimeout as sleep } from "node:timers/promises";
import { parseArgs } from "node:util";
import path from "node:path";
import {
  LAUNCHER_NAME,
  ensure,
  event,
  isMain,
  npm,
  nativeName,
  packageRoot,
  registry,
  targets,
  trustedRepository,
  trustedWorkflow,
} from "./common.mjs";
import { verifyPlaceholders } from "./package.mjs";

export async function registryVersion(artifact, request = fetch) {
  const response = await request(
    `${registry}/${encodeURIComponent(artifact.name)}/${artifact.version}`,
    { redirect: "error", signal: AbortSignal.timeout(30000) },
  );
  if (response.status === 404) return null;
  ensure(
    response.ok,
    `Cannot inspect npm package ${artifact.name}: HTTP ${response.status}`,
  );
  const body = await response.json();
  ensure(
    body.name === artifact.name && body.version === artifact.version,
    `Invalid registry metadata for ${artifact.name}`,
  );
  return body;
}

// Publishes each missing version once, in order, and never overwrites. An
// existing version is reused without comparing bytes: the 0.0.0 placeholders
// carry no executable content, and a maintainer's bootstrap upload cannot be
// byte-identical to a CI upload across npm versions. Remove this exception and
// compare `dist.integrity` as the other npm distributions do before this
// package set ships real binaries.
export async function publishArtifacts(
  artifacts,
  {
    dryRun = true,
    lookup = registryVersion,
    publish,
    delay = sleep,
    report = event,
  } = {},
) {
  if (dryRun) {
    report("publish_dry_run", { packages: artifacts });
    return;
  }
  ensure(typeof publish === "function", "A publisher is required");
  for (const artifact of artifacts) {
    let found = await lookup(artifact);
    const reused = found !== null;
    if (!reused) {
      await publish(artifact);
      // npm scans accepted uploads before exposing version metadata, so poll
      // for readback without repeating the immutable upload.
      for (let attempt = 0; attempt <= 120 && found === null; attempt++) {
        found = await lookup(artifact);
        if (found !== null) break;
        report("publish_pending", {
          name: artifact.name,
          version: artifact.version,
          attempt: attempt + 1,
        });
        if (attempt < 120) await delay(10000);
      }
    }
    ensure(
      found !== null,
      `Published package was not confirmed: ${artifact.name}@${artifact.version}`,
    );
    report("publish", {
      name: artifact.name,
      version: artifact.version,
      integrity: artifact.integrity,
      reused,
    });
  }
}

// Publication inherits the terminal instead of capturing output. A maintainer's
// account can require publication-time 2FA, and npm only prompts for it or opens
// its browser approval when stdin and stdout are TTYs. Inheriting also keeps
// npm's own error output visible in workflow logs.
export function publishTarball(
  artifact,
  directory,
  { provenance = false, run = npm } = {},
) {
  try {
    run(
      [
        "publish",
        path.resolve(directory, artifact.filename),
        "--access",
        "public",
        // Provenance needs the CI OIDC identity; a local upload cannot sign.
        ...(provenance ? ["--provenance"] : []),
        "--ignore-scripts",
        "--registry",
        registry,
      ],
      { stdio: "inherit" },
    );
  } catch {
    throw new Error(
      `npm publication failed for ${artifact.name}; inspect npm permissions and rerun using the retained artifacts`,
    );
  }
}

// `npm trust` needs the package to exist, so this follows the first upload.
export function trustCommands() {
  return [...targets.map(nativeName), LAUNCHER_NAME].map(
    (name) =>
      `npm trust github ${name} --repo ${trustedRepository} --file ${trustedWorkflow} --allow-publish`,
  );
}

export async function main(env = process.env) {
  const { values } = parseArgs({
    options: {
      directory: {
        type: "string",
        default: path.join(packageRoot, "dist/tarballs"),
      },
      publish: { type: "boolean", default: false },
      bootstrap: { type: "boolean", default: false },
      "print-trust-commands": { type: "boolean", default: false },
    },
  });
  if (values["print-trust-commands"]) {
    console.log(trustCommands().join("\n"));
    return;
  }
  ensure(
    !(values.publish && values.bootstrap),
    "Choose either --publish or --bootstrap",
  );
  const artifacts = verifyPlaceholders(values.directory);
  if (values.publish) {
    ensure(
      env.GITHUB_ACTIONS === "true" &&
        env.GITHUB_REPOSITORY === trustedRepository &&
        env.GITHUB_REF === "refs/heads/main" &&
        env.GITHUB_WORKFLOW_REF?.includes(
          `/.github/workflows/${trustedWorkflow}@`,
        ),
      "CI publication requires the first-party main release workflow",
    );
    ensure(
      env.ACTIONS_ID_TOKEN_REQUEST_URL && env.ACTIONS_ID_TOKEN_REQUEST_TOKEN,
      "npm publication requires GitHub Actions OIDC",
    );
  }
  if (values.bootstrap) {
    // The first version of a new package cannot use trusted publishing. This
    // path uses the maintainer's own npm login and is refused in CI.
    ensure(env.GITHUB_ACTIONS !== "true", "Bootstrap publishing is local only");
    event("bootstrap_account", { user: npm(["whoami"]).trim() });
  }
  const live = values.publish || values.bootstrap;
  await publishArtifacts(artifacts, {
    dryRun: !live,
    publish: (artifact) =>
      publishTarball(artifact, values.directory, {
        provenance: values.publish,
      }),
  });
  if (values.bootstrap) {
    event("bootstrap_next_step", {
      message:
        "Configure trusted publishers with: node packages/binpm/scripts/publish.mjs --print-trust-commands",
    });
  }
}

if (isMain(import.meta.url))
  main().catch((error) => {
    console.error(
      JSON.stringify({
        event: "binpm_npm_publish_failed",
        message: error.message,
      }),
    );
    process.exitCode = 1;
  });
