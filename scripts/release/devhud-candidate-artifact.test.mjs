import assert from "node:assert/strict";
import test from "node:test";

import { candidateArtifactName, releaseConfigurationArtifactName, resolveCandidateArtifact } from "./devhud-candidate-artifact.mjs";

const revision = "a".repeat(40);
const jsonResponse = (body, { status = 200, link } = {}) => new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json", ...(link ? { link } : {}) } });

function requestFixture({ runs = [], artifacts = new Map(), configurations = [] } = {}) {
  const requests = [];
  const fetchImpl = async (input) => {
    const url = String(input);
    requests.push(url);
    if (url.includes("/actions/workflows/")) return jsonResponse({ workflow_runs: runs });
    if (url.includes("/actions/artifacts?")) return jsonResponse({ artifacts: configurations });
    const runId = /\/actions\/runs\/(\d+)\/artifacts/u.exec(url)?.[1];
    if (runId) return jsonResponse({ artifacts: artifacts.get(runId) ?? [] });
    throw new Error(`unexpected request: ${url}`);
  };
  return { requests, fetchImpl };
}

const options = { repository: "delinoio/oss", workflow: "release-devhud.yml", version: "0.1.0", revision, currentRunId: "200", currentRunAttempt: "1", token: "token" };

function withoutConfiguration(candidate) {
  return {
    ...candidate,
    configurationArtifactId: "",
    configurationArtifactName: releaseConfigurationArtifactName({ version: options.version, revision, candidateRunId: candidate.runId, candidateRunAttempt: candidate.runAttempt }),
    configurationRunId: "",
    configurationReused: false,
  };
}

test("candidate artifact names bind version, revision, run, and attempt", () => {
  assert.equal(candidateArtifactName({ version: "0.1.0", revision, runId: "123", runAttempt: "2" }), `devhud-v0.1.0-private-signed-candidate-${revision}-123-2`);
  assert.throws(() => candidateArtifactName({ version: "0.1.0", revision: "bad", runId: "123", runAttempt: "2" }), /revision/u);
});

test("a first release selects the current run for a new candidate", async () => {
  const { fetchImpl } = requestFixture();
  assert.deepEqual(await resolveCandidateArtifact(options, fetchImpl), withoutConfiguration({
    artifactId: "",
    artifactName: `devhud-v0.1.0-private-signed-candidate-${revision}-200-1`,
    runId: "200",
    runAttempt: "1",
    reused: false,
  }));
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
  assert.deepEqual(await resolveCandidateArtifact(options, fetchImpl), withoutConfiguration({ artifactId: "501", artifactName: firstName, runId: "100", runAttempt: "2", reused: true }));
});

test("recovery searches every attempt of a historical rerun", async () => {
  const retainedName = candidateArtifactName({ version: "0.1.0", revision, runId: "100", runAttempt: "1" });
  const runs = [{ id: 100, run_attempt: 3, event: "workflow_dispatch", head_branch: "main", head_sha: revision }];
  const artifacts = new Map([["100", [{ id: 551, name: retainedName, expired: false, size_in_bytes: 1024, workflow_run: { head_sha: revision } }]]]);
  const { requests, fetchImpl } = requestFixture({ runs, artifacts });
  assert.deepEqual(await resolveCandidateArtifact(options, fetchImpl), withoutConfiguration({ artifactId: "551", artifactName: retainedName, runId: "100", runAttempt: "1", reused: true }));
  for (let attempt = 1; attempt <= 3; attempt += 1) {
    const name = candidateArtifactName({ version: "0.1.0", revision, runId: "100", runAttempt: String(attempt) });
    assert.ok(requests.some((url) => url.includes(encodeURIComponent(name))), `historical attempt ${attempt} must be searched`);
  }
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
  assert.deepEqual(await resolveCandidateArtifact(rerun, fetchImpl), withoutConfiguration({ artifactId: "601", artifactName: firstName, runId: "200", runAttempt: "1", reused: true }));
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

test("recovery discovers the candidate-bound configuration from a later main run", async () => {
  const candidateName = candidateArtifactName({ version: "0.1.0", revision, runId: "100", runAttempt: "1" });
  const configurationName = releaseConfigurationArtifactName({ version: "0.1.0", revision, candidateRunId: "100", candidateRunAttempt: "1" });
  const runs = [{ id: 100, run_attempt: 1, event: "workflow_dispatch", head_branch: "main", head_sha: revision }];
  const artifacts = new Map([["100", [{ id: 501, name: candidateName, expired: false, size_in_bytes: 1024, workflow_run: { head_sha: revision } }]]]);
  const configurations = [{ id: 701, name: configurationName, expired: false, size_in_bytes: 256, workflow_run: { id: 300, head_branch: "main", head_sha: "b".repeat(40) } }];
  const { fetchImpl } = requestFixture({ runs, artifacts, configurations });
  assert.deepEqual(await resolveCandidateArtifact(options, fetchImpl), {
    artifactId: "501", artifactName: candidateName, runId: "100", runAttempt: "1", reused: true,
    configurationArtifactId: "701", configurationArtifactName: configurationName, configurationRunId: "300", configurationReused: true,
  });
});

test("configuration lookup fails closed for duplicate or expired bindings", async () => {
  const name = releaseConfigurationArtifactName({ version: "0.1.0", revision, candidateRunId: "200", candidateRunAttempt: "1" });
  const configuration = (id, expired = false) => ({ id, name, expired, size_in_bytes: 1, workflow_run: { id: 300, head_branch: "main" } });
  await assert.rejects(resolveCandidateArtifact(options, requestFixture({ configurations: [configuration(701), configuration(702)] }).fetchImpl), /multiple retained release configurations/u);
  await assert.rejects(resolveCandidateArtifact(options, requestFixture({ configurations: [configuration(701, true)] }).fetchImpl), /expired/u);
});
