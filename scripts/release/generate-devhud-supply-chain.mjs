#!/usr/bin/env node

import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { pathToFileURL } from "node:url";

import { artifactGroups, loadReleaseMetadata, repositoryRoot } from "./devhud-release.mjs";

const artifacts = Object.values(artifactGroups).flat();
const WORKFLOW_INVOCATION_ID = /^https:\/\/github\.com\/delinoio\/oss\/actions\/runs\/[1-9]\d*\/attempts\/[1-9]\d*$/u;
const UTC_RFC3339_TIMESTAMP = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?Z$/u;

function sha256(path) {
  return createHash("sha256").update(readFileSync(path)).digest("hex");
}

function sourceRevision(root) {
  return execFileSync("git", ["rev-parse", "HEAD"], { cwd: root, encoding: "utf8" }).trim();
}

function provenanceTimestamp(value, name) {
  if (typeof value !== "string" || !UTC_RFC3339_TIMESTAMP.test(value)) throw new Error(`${name} must be a UTC RFC 3339 timestamp`);
  const instant = new Date(value);
  if (Number.isNaN(instant.valueOf())) throw new Error(`${name} must be a UTC RFC 3339 timestamp`);
  return instant.toISOString();
}

export function validateProvenanceMetadata({ invocationId, startedOn, finishedOn }) {
  if (typeof invocationId !== "string" || !WORKFLOW_INVOCATION_ID.test(invocationId)) {
    throw new Error("provenance invocation ID must identify a delinoio/oss GitHub Actions run attempt");
  }
  const normalizedStartedOn = provenanceTimestamp(startedOn, "provenance startedOn");
  const normalizedFinishedOn = provenanceTimestamp(finishedOn, "provenance finishedOn");
  if (Date.parse(normalizedFinishedOn) < Date.parse(normalizedStartedOn)) {
    throw new Error("provenance finishedOn must not precede startedOn");
  }
  return { invocationId, startedOn: normalizedStartedOn, finishedOn: normalizedFinishedOn };
}

export function provenanceStatement({ artifact, digest, version, revision, invocationId, startedOn, finishedOn, materials }) {
  const execution = validateProvenanceMetadata({ invocationId, startedOn, finishedOn });
  return {
    _type: "https://in-toto.io/Statement/v1",
    subject: [{ name: artifact, digest: { sha256: digest } }],
    predicateType: "https://slsa.dev/provenance/v1",
    predicate: {
      buildDefinition: {
        buildType: "https://github.com/delinoio/oss/blob/main/docs/project-devhud.md#private-build-packaging",
        externalParameters: { project: "devhud", version, tag: `devhud@v${version}`, publication: "disabled" },
        internalParameters: { mode: "manual-signed-private" },
        resolvedDependencies: materials,
      },
      runDetails: {
        builder: { id: "https://github.com/delinoio/oss/.github/workflows/package-devhud-private.yml" },
        metadata: execution,
        byproducts: [],
      },
    },
  };
}

export function validateSpdx(document, artifact) {
  if (document.spdxVersion !== "SPDX-2.3" || document.dataLicense !== "CC0-1.0" || document.SPDXID !== "SPDXRef-DOCUMENT") {
    throw new Error(`invalid SPDX 2.3 document for ${artifact}`);
  }
  if (!Array.isArray(document.packages) || document.packages.length === 0) throw new Error(`SPDX document has no packages for ${artifact}`);
}

export function generateSupplyChain({ artifactsDirectory, outputDirectory, invocationId, startedOn, finishedOn, root = repositoryRoot }) {
  const metadata = loadReleaseMetadata();
  const revision = sourceRevision(root);
  const execution = validateProvenanceMetadata({ invocationId, startedOn, finishedOn });
  const materials = ["pnpm-lock.yaml", "Cargo.lock", "go.sum", "apps/devhud/cef-pins.json"].map((path) => ({
    uri: `git+https://github.com/delinoio/oss@${revision}#${path}`,
    digest: { sha256: sha256(join(root, path)) },
  }));
  const sbomDirectory = join(outputDirectory, "sbom");
  const provenanceDirectory = join(outputDirectory, "provenance");
  mkdirSync(sbomDirectory, { recursive: true });
  mkdirSync(provenanceDirectory, { recursive: true });
  for (const artifact of artifacts) {
    const artifactPath = join(artifactsDirectory, artifact);
    const sbomPath = join(sbomDirectory, `${artifact}.spdx.json`);
    const document = JSON.parse(readFileSync(sbomPath, "utf8"));
    validateSpdx(document, artifact);
    const statement = provenanceStatement({ artifact, digest: sha256(artifactPath), version: metadata.version, revision, ...execution, materials });
    writeFileSync(join(provenanceDirectory, `${artifact}.intoto.jsonl`), `${JSON.stringify(statement)}\n`);
  }
  return artifacts.length;
}

function parseArguments(arguments_) {
  const result = {};
  for (let index = 0; index < arguments_.length; index += 1) {
    const name = arguments_[index];
    if (!["--artifacts-dir", "--output-dir", "--invocation-id", "--started-on", "--finished-on"].includes(name)) throw new Error(`unknown argument: ${name}`);
    const value = arguments_[++index];
    if (!value || value.startsWith("--")) throw new Error(`${name} requires a value`);
    result[name.slice(2)] = value;
  }
  for (const name of ["artifacts-dir", "output-dir", "invocation-id", "started-on", "finished-on"]) if (!result[name]) throw new Error(`--${name} is required`);
  return result;
}

export function main(arguments_ = process.argv.slice(2)) {
  const options = parseArguments(arguments_);
  const count = generateSupplyChain({
    artifactsDirectory: resolve(options["artifacts-dir"]),
    outputDirectory: resolve(options["output-dir"]),
    invocationId: options["invocation-id"],
    startedOn: options["started-on"],
    finishedOn: options["finished-on"],
  });
  console.error(`[devhud.supply-chain] validated ${count} SPDX SBOMs and generated digest-bound provenance`);
}

if (import.meta.url === pathToFileURL(process.argv[1]).href) {
  try { main(); } catch (error) {
    console.error(`[devhud.supply-chain] ${error.message}`);
    process.exit(1);
  }
}
