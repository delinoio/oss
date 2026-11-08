// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { appendFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { version } from "../../packages/ccow/scripts/package.mjs";
import {
  githubRequest,
  GitHubRequestError,
  prepare,
  reconcile,
  transport,
  verifyPublished,
} from "./ccow-publish.mjs";

const repository = "delinoio/oss";
const sourceWorkflow = ".github/workflows/release-ccow.yml";
const recoveryWorkflow = ".github/workflows/recover-ccow-release.yml";
export const Mode = Object.freeze({
  Resolve: "resolve",
  Publish: "publish",
  VerifyPublished: "verify-published",
});

export function recoverySource(run, sourcePackage, artifacts, runId) {
  assert.match(runId, /^[1-9][0-9]*$/u);
  assert(Number.isSafeInteger(Number(runId)));
  assert.equal(run.id, Number(runId));
  assert.equal(run.repository.full_name, repository);
  assert.equal(run.head_repository.full_name, repository);
  assert.equal(run.path, sourceWorkflow);
  assert.equal(run.event, "workflow_dispatch");
  assert.equal(run.head_branch, "main");
  assert.equal(run.status, "completed");
  assert.equal(run.conclusion, "failure");
  assert.match(run.head_sha, /^[a-f0-9]{40}$/u);
  assert(!/^0+$/u.test(run.head_sha));
  assert.equal(
    sourcePackage.version,
    version,
    "Recovery tooling must match the source version",
  );
  const names = ["candidate", "signatures"].map(
    (kind) => `ccow-${kind}-${run.head_sha}`,
  );
  for (const name of names) {
    const matches = artifacts.filter((artifact) => artifact.name === name);
    assert.equal(matches.length, 1, "Ambiguous or missing original artifact");
    assert.equal(matches[0].expired, false, "Original artifact expired");
  }
  return { revision: run.head_sha, version, runId };
}

export function resolveRecovery(runId, request = githubRequest) {
  assert.match(runId, /^[1-9][0-9]*$/u);
  assert(Number.isSafeInteger(Number(runId)));
  const base = `repos/${repository}`;
  const run = request(`${base}/actions/runs/${runId}`);
  assert.match(run.head_sha, /^[a-f0-9]{40}$/u);
  const contents = request(
    `${base}/contents/packages/ccow/package.json?ref=${run.head_sha}`,
  );
  assert.equal(contents.encoding, "base64");
  const sourcePackage = JSON.parse(
    Buffer.from(contents.content, "base64").toString("utf8"),
  );
  const artifacts = [];
  for (let page = 1; ; page++) {
    const response = request(
      `${base}/actions/runs/${runId}/artifacts?per_page=100&page=${page}`,
    );
    assert(Array.isArray(response.artifacts));
    artifacts.push(...response.artifacts);
    if (response.artifacts.length < 100) break;
    assert(page < 100, "Artifact pagination exceeded its bound");
  }
  return recoverySource(run, sourcePackage, artifacts, runId);
}

function context() {
  assert.equal(process.env.GITHUB_REPOSITORY, repository);
  assert.equal(process.env.GITHUB_EVENT_NAME, "workflow_dispatch");
  assert.equal(process.env.GITHUB_REF, "refs/heads/main");
  assert.equal(
    process.env.GITHUB_WORKFLOW_REF,
    `${repository}/${recoveryWorkflow}@refs/heads/main`,
  );
  assert.equal(
    execFileSync("git", ["rev-parse", "HEAD"], { encoding: "utf8" }).trim(),
    process.env.GITHUB_SHA,
  );
  assert.equal(
    execFileSync("git", ["status", "--porcelain"], { encoding: "utf8" }).trim(),
    "",
  );
}

if (
  process.argv[1] &&
  path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  try {
    context();
    const [mode, runId, artifacts, signatures] = process.argv.slice(2);
    assert(Object.values(Mode).includes(mode));
    assert(process.env.GH_TOKEN, "GitHub credentials missing");
    const readToken =
      mode === Mode.Publish
        ? process.env.CCOW_ACTIONS_TOKEN
        : process.env.GH_TOKEN;
    assert(readToken, "Read-only source-run credentials missing");
    const source = resolveRecovery(runId, (endpoint) =>
      githubRequest(endpoint, {
        env: { ...process.env, GH_TOKEN: readToken },
      }),
    );
    if (mode === Mode.Resolve) {
      assert(process.env.GITHUB_OUTPUT);
      appendFileSync(
        process.env.GITHUB_OUTPUT,
        `revision=${source.revision}\nversion=${source.version}\n`,
      );
      console.log(JSON.stringify({ event: "ccow_recovery_source", ...source }));
    } else {
      assert(
        artifacts && signatures,
        "Retained candidate and signatures required",
      );
      const plan = prepare({
        ...source,
        artifacts,
        signatures,
        ref: "refs/heads/main",
      });
      console.log(
        JSON.stringify({ event: "ccow_recovery_publication", ...source }),
      );
      console.log(
        JSON.stringify(
          await (mode === Mode.VerifyPublished ? verifyPublished : reconcile)(
            plan,
            transport(),
          ),
        ),
      );
    }
  } catch (error) {
    if (error instanceof GitHubRequestError)
      console.error(
        JSON.stringify({
          event: "ccow_transport_failure",
          code: error.code,
          status: error.status,
          exitStatus: error.exitStatus,
        }),
      );
    console.error(
      "CacheCow recovery failed. Preserve the original tag, draft, and retained artifacts; inspect the source run before retrying.",
    );
    process.exitCode = 1;
  }
}
