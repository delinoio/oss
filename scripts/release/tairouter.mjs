import { appendFileSync, readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import {
  Project,
  sourceMetadata,
  tagRevision,
  githubRequest,
} from "./project.mjs";

export async function releasePlan(env, request = githubRequest) {
  if (env.GITHUB_REPOSITORY !== "delinoio/oss")
    throw new Error("Unexpected release repository");
  if (!/^[a-f0-9]{40}$/u.test(env.GITHUB_SHA ?? ""))
    throw new Error("Invalid source revision");
  const plan = sourceMetadata(
    {
      project: Project.TaiRouter,
      event: env.GITHUB_EVENT_NAME,
      ref: env.GITHUB_REF,
      requestedVersion: env.REQUESTED_VERSION,
      requestedDryRun: env.DRY_RUN,
    },
    (file) => readFileSync(new URL(`../../${file}`, import.meta.url), "utf8"),
  );
  if (
    plan.dry_run === "false" &&
    (await tagRevision(plan.tag, request)) !== env.GITHUB_SHA
  )
    throw new Error(
      "Publication requires the existing tag to match the source revision",
    );
  return plan;
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const plan = await releasePlan(process.env);
  if (process.env.GITHUB_OUTPUT)
    appendFileSync(
      process.env.GITHUB_OUTPUT,
      Object.entries(plan)
        .map(([key, value]) => `${key}=${value}\n`)
        .join(""),
    );
  console.log(JSON.stringify({ ...plan, revision: process.env.GITHUB_SHA }));
}
