// SPDX-License-Identifier: Apache-2.0
import { spawnSync } from "node:child_process";
import { appendFileSync } from "node:fs";
import { Identity } from "./beta.mjs";
export function verifyProvenance(artifact, run, input, receipt = false) {
  const prefix = receipt
    ? "delidev-mobile-receipts-"
    : `delidev-mobile-candidate-${input.sourceSha}-${input.version}-${input.iosBuild}-${input.target === "ios" ? "ios" : input.androidCode}`;
  if (
    !artifact ||
    !Number.isSafeInteger(artifact.workflow_run?.id) ||
    artifact.workflow_run.id < 1 ||
    artifact.workflow_run.id !== run?.id ||
    artifact.expired ||
    !artifact.name.startsWith(prefix) ||
    (!receipt && artifact.name !== prefix) ||
    artifact.workflow_run?.head_sha !== input.sourceSha ||
    run?.head_sha !== input.sourceSha ||
    run.event !== "workflow_dispatch" ||
    run.path !== ".github/workflows/delidev-mobile-beta.yml" ||
    (!receipt && run.conclusion !== "success")
  )
    throw new Error("Original mobile artifact provenance mismatch");
  return {
    identity: Identity,
    artifactId: artifact.id,
    runId: run.id,
    sourceSha: input.sourceSha,
  };
}
if (process.argv[1]?.endsWith("/provenance.mjs"))
  try {
    const e = process.env;
    if (!/^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/.test(e.GITHUB_REPOSITORY ?? ""))
      throw new Error("Invalid repository identity");
    const get = (path) => {
      const r = spawnSync("gh", ["api", path], {
        encoding: "utf8",
        maxBuffer: 2 << 20,
      });
      if (r.status !== 0) throw new Error("Artifact ownership lookup failed");
      return JSON.parse(r.stdout);
    };
    for (const [id, receipt] of [
      [e.ARTIFACT_ID, false],
      [e.RECEIPT_ID, true],
    ])
      if (id) {
        if (!/^\d+$/.test(id)) throw new Error("Invalid artifact identity");
        const artifact = get(
            `repos/${e.GITHUB_REPOSITORY}/actions/artifacts/${id}`,
          ),
          run = get(
            `repos/${e.GITHUB_REPOSITORY}/actions/runs/${artifact.workflow_run?.id}`,
          );
        const proof = verifyProvenance(
          artifact,
          run,
          {
            target: e.DELIDEV_MOBILE_TARGET ?? "both",
            sourceSha: e.DELIDEV_MOBILE_SOURCE_SHA,
            version: e.DELIDEV_MOBILE_VERSION,
            iosBuild: e.DELIDEV_MOBILE_IOS_BUILD,
            androidCode: e.DELIDEV_MOBILE_ANDROID_CODE,
          },
          receipt,
        );
        // Cross-run downloads must use the verified owner, never the current run.
        if (e.GITHUB_OUTPUT) appendFileSync(e.GITHUB_OUTPUT,
          `${receipt ? "receipt" : "candidate"}_run_id=${proof.runId}\n`);
      }
  } catch {
    process.stderr.write(
      "Original mobile artifact ownership could not be verified.\n",
    );
    process.exitCode = 1;
  }
