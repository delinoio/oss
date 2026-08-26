import assert from "node:assert/strict";
import test from "node:test";

import { candidateArtifactName, resolveCandidateArtifact } from "./devhud-candidate-artifact.mjs";

const revision = "a".repeat(40);
const jsonResponse = (body, { status = 200, link } = {}) => new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json", ...(link ? { link } : {}) } });

function requestFixture({ runs = [], artifacts = new Map() } = {}) {
  const requests = [];
  const fetchImpl = async (input) => {
    const url = String(input);
    requests.push(url);
    if (url.includes("/actions/workflows/")) return jsonResponse({ workflow_runs: runs });
    const runId = /\/actions\/runs\/(\d+)\/artifacts/u.exec(url)?.[1];
    if (runId) return jsonResponse({ artifacts: artifacts.get(runId) ?? [] });
    throw new Error(`unexpected request: ${url}`);
  };
  return { requests, fetchImpl };
}

const options = { repository: "delinoio/oss", workflow: "release-devhud.yml", version: "0.1.0", revision, currentRunId: "200", currentRunAttempt: "1", token: "token" };

test("candidate artifact names bind version, revision, run, and attempt", () => {
  assert.equal(candidateArtifactName({ version: "0.1.0", revision, runId: "123", runAttempt: "2" }), `devhud-v0.1.0-private-signed-candidate-${revision}-123-2`);
  assert.throws(() => candidateArtifactName({ version: "0.1.0", revision: "bad", runId: "123", runAttempt: "2" }), /revision/u);
});

test("a first release selects the current run for a new candidate", async () => {
  const { fetchImpl } = requestFixture();
  assert.deepEqual(await resolveCandidateArtifact(options, fetchImpl), {
    artifactId: "",
    artifactName: `devhud-v0.1.0-private-signed-candidate-${revision}-200-1`,
    runId: "200",
    runAttempt: "1",
    reused: false,
  });
});

test("recovery reuses the oldest retained exact-revision candidate", async () => {
  const firstName = candidateArtifactName({ version: "0.1.0", revision, runId: "100", runAttempt: "2" });
  const secondName = candidateArtifactName({ version: "0.1.0", revision, runId: "150", runAttempt: "1" });
  const runs = [
    { id: 150, run_attempt: 1, event: "workflow_dispatch", head_branch: "main", head_sha: revision },
    { id: 90, run_attempt: 1, event: "workflow_dispatch", head_branch: "main", head_sha: "b".repeat(40) },
    { id: 100, run_attempt: 2, event: "workflow_dispatch", head_branch: "main", head_sha: revision },
  ];
  const artifacts = new Map([
    ["100", [{ id: 501, name: firstName, expired: false, size_in_bytes: 1024, workflow_run: { head_sha: revision } }]],
    ["150", [{ id: 502, name: secondName, expired: false, size_in_bytes: 1024, workflow_run: { head_sha: revision } }]],
  ]);
  const { fetchImpl } = requestFixture({ runs, artifacts });
  assert.deepEqual(await resolveCandidateArtifact(options, fetchImpl), { artifactId: "501", artifactName: firstName, runId: "100", runAttempt: "2", reused: true });
});

test("a rerun reuses the candidate from an earlier attempt of the current run", async () => {
  const rerun = { ...options, currentRunAttempt: "3" };
  const firstName = candidateArtifactName({ version: "0.1.0", revision, runId: "200", runAttempt: "1" });
  const secondName = candidateArtifactName({ version: "0.1.0", revision, runId: "200", runAttempt: "2" });
  const artifacts = new Map([["200", [
    { id: 601, name: firstName, expired: false, size_in_bytes: 1024, workflow_run: { head_sha: revision } },
    { id: 602, name: secondName, expired: false, size_in_bytes: 1024, workflow_run: { head_sha: revision } },
  ]]]);
  const { requests, fetchImpl } = requestFixture({ artifacts });
  assert.deepEqual(await resolveCandidateArtifact(rerun, fetchImpl), { artifactId: "601", artifactName: firstName, runId: "200", runAttempt: "1", reused: true });
  assert.ok(requests.some((url) => url.includes(encodeURIComponent(firstName))));
  assert.ok(!requests.some((url) => url.includes("-200-3")), "the active attempt must not be considered reusable");
});

test("candidate lookup fails closed for expired, duplicate, or failed GitHub results", async () => {
  const name = candidateArtifactName({ version: "0.1.0", revision, runId: "100", runAttempt: "1" });
  const runs = [{ id: 100, run_attempt: 1, event: "workflow_dispatch", head_branch: "main", head_sha: revision }];
  const expired = requestFixture({ runs, artifacts: new Map([["100", [{ id: 501, name, expired: true, size_in_bytes: 1, workflow_run: { head_sha: revision } }]]]) });
  await assert.rejects(resolveCandidateArtifact(options, expired.fetchImpl), /expired/u);
  const duplicate = requestFixture({ runs, artifacts: new Map([["100", [501, 502].map((id) => ({ id, name, expired: false, size_in_bytes: 1, workflow_run: { head_sha: revision } }))]]) });
  await assert.rejects(resolveCandidateArtifact(options, duplicate.fetchImpl), /multiple retained/u);
  await assert.rejects(resolveCandidateArtifact(options, async () => jsonResponse({}, { status: 503 })), /HTTP 503/u);
});
