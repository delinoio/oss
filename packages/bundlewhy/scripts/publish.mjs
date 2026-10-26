import { parseArgs } from "node:util";
import path from "node:path";
import {
  ensure,
  event,
  isMain,
  npm,
  packageRoot,
  registry,
  revision,
} from "./common.mjs";
import { verifySet } from "./package.mjs";

import { publishArtifacts } from "../../clibox/scripts/publish.mjs";
export async function main() {
  const { values } = parseArgs({
    options: {
      directory: {
        type: "string",
        default: path.join(packageRoot, "dist/tarballs"),
      },
      publish: { type: "boolean", default: false },
    },
  });
  const sourceRevision = revision();
  const artifacts = verifySet(values.directory, sourceRevision);
  if (values.publish) {
    ensure(
      process.env.GITHUB_REPOSITORY === "delinoio/oss" &&
        process.env.GITHUB_REF ===
          `refs/tags/bundlewhy@v${artifacts[0].version}` &&
        process.env.GITHUB_SHA === sourceRevision,
      "npm publication requires the exact first-party release tag and commit",
    );
    ensure(
      process.env.ACTIONS_ID_TOKEN_REQUEST_URL &&
        process.env.ACTIONS_ID_TOKEN_REQUEST_TOKEN,
      "npm publication requires GitHub Actions OIDC",
    );
  }
  await publishArtifacts(artifacts, {
    report: event,
    dryRun: !values.publish,
    publish: (artifact) => {
      try {
        npm([
          "publish",
          path.resolve(values.directory, artifact.filename),
          "--access",
          "public",
          "--provenance",
          "--ignore-scripts",
          "--registry",
          registry,
        ]);
      } catch {
        throw new Error(
          `npm publication failed for ${artifact.name}; inspect npm permissions and rerun the failed job using the retained artifacts`,
        );
      }
    },
  });
}

if (isMain(import.meta.url))
  main().catch((error) => {
    console.error(
      JSON.stringify({
        event: "bundlewhy_publish_failed",
        message: error.message,
      }),
    );
    process.exitCode = 1;
  });
