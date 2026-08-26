#!/usr/bin/env node

import { writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";

import { validateVersion } from "./devhud-release.mjs";

const RUN_ID = /^[1-9]\d*$/u;
const REVISION = /^[a-f0-9]{40}$/u;

export function candidateArtifactName({ version, revision, runId, runAttempt }) {
  validateVersion(version);
  if (!REVISION.test(revision)) throw new Error("candidate revision must be an exact lowercase 40-hex commit");
  if (!RUN_ID.test(String(runId)) || !RUN_ID.test(String(runAttempt))) throw new Error("candidate run identity must use positive decimal integers");
  return `devhud-v${version}-private-signed-candidate-${revision}-${runId}-${runAttempt}`;
}

function nextLink(header) {
  if (!header) return null;
  for (const entry of header.split(",")) {
    const match = /^\s*<([^>]+)>;\s*rel="([^"]+)"\s*$/u.exec(entry);
    if (match?.[2] === "next") return match[1];
  }
  return null;
}

async function githubJson(url, token, fetchImpl) {
  const response = await fetchImpl(url, {
    headers: {
      accept: "application/vnd.github+json",
      authorization: `Bearer ${token}`,
      "x-github-api-version": "2026-03-10",
    },
  });
  if (!response.ok) throw new Error(`GitHub candidate lookup failed with HTTP ${response.status}`);
  return { body: await response.json(), next: nextLink(response.headers.get("link")) };
}

async function pages(url, token, fetchImpl) {
  const values = [];
  for (let next = url; next;) {
    const response = await githubJson(next, token, fetchImpl);
    values.push(response.body);
    next = response.next;
  }
  return values;
}

export async function resolveCandidateArtifact({ repository, workflow, version, revision, currentRunId, currentRunAttempt, token }, fetchImpl = fetch) {
  if (!/^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/u.test(repository ?? "")) throw new Error("candidate repository must be owner/name");
  if (!/^[A-Za-z0-9_.-]+\.ya?ml$/u.test(workflow ?? "")) throw new Error("candidate workflow must be a workflow filename");
  const currentName = candidateArtifactName({ version, revision, runId: currentRunId, runAttempt: currentRunAttempt });
  if (typeof token !== "string" || token === "") throw new Error("GITHUB_TOKEN is required for candidate lookup");

  const root = `https://api.github.com/repos/${repository}`;
  const runPages = await pages(`${root}/actions/workflows/${encodeURIComponent(workflow)}/runs?branch=main&event=workflow_dispatch&per_page=100`, token, fetchImpl);
  const runs = runPages.flatMap((page) => page.workflow_runs ?? [])
    .filter((run) => String(run.id) !== String(currentRunId) && run.event === "workflow_dispatch" && run.head_branch === "main" && run.head_sha === revision)
    .sort((left, right) => Number(left.id) - Number(right.id));

  const retained = [];
  for (const run of runs) {
    const name = candidateArtifactName({ version, revision, runId: run.id, runAttempt: run.run_attempt });
    const artifactPages = await pages(`${root}/actions/runs/${run.id}/artifacts?name=${encodeURIComponent(name)}&per_page=100`, token, fetchImpl);
    const artifacts = artifactPages.flatMap((page) => page.artifacts ?? []).filter((artifact) => artifact.name === name && artifact.workflow_run?.head_sha === revision);
    if (artifacts.length > 1) throw new Error(`multiple retained candidates found for workflow run ${run.id}`);
    if (artifacts.length === 0) continue;
    const [artifact] = artifacts;
    if (artifact.expired === true) throw new Error(`retained candidate ${artifact.id} has expired`);
    if (!RUN_ID.test(String(artifact.id)) || !Number.isSafeInteger(artifact.size_in_bytes) || artifact.size_in_bytes <= 0) throw new Error("retained candidate metadata is invalid");
    retained.push({ artifactId: String(artifact.id), artifactName: name, runId: String(run.id), runAttempt: String(run.run_attempt), reused: true });
  }

  return retained[0] ?? {
    artifactId: "",
    artifactName: currentName,
    runId: String(currentRunId),
    runAttempt: String(currentRunAttempt),
    reused: false,
  };
}

function parse(arguments_) {
  const options = {};
  for (let index = 0; index < arguments_.length; index += 2) {
    const name = arguments_[index];
    const value = arguments_[index + 1];
    if (!name?.startsWith("--") || value === undefined) throw new Error(`invalid argument: ${name ?? "missing"}`);
    options[name.slice(2)] = value;
  }
  for (const name of ["repository", "workflow", "version", "revision", "current-run-id", "current-run-attempt", "output"]) if (!options[name]) throw new Error(`--${name} is required`);
  return options;
}

export async function main(arguments_ = process.argv.slice(2), environment = process.env) {
  const options = parse(arguments_);
  const candidate = await resolveCandidateArtifact({
    repository: options.repository,
    workflow: options.workflow,
    version: options.version,
    revision: options.revision,
    currentRunId: options["current-run-id"],
    currentRunAttempt: options["current-run-attempt"],
    token: environment.GITHUB_TOKEN,
  });
  writeFileSync(resolve(options.output), `${JSON.stringify(candidate, null, 2)}\n`, { mode: 0o600 });
}

if (import.meta.url === pathToFileURL(process.argv[1]).href) {
  try { await main(); } catch (error) {
    process.stderr.write(`[devhud.candidate] ${error.message}\n`);
    process.exit(1);
  }
}
