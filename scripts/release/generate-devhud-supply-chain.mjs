#!/usr/bin/env node

import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { pathToFileURL } from "node:url";

import { artifactGroups, loadReleaseMetadata, repositoryRoot } from "./devhud-release.mjs";

const artifacts = Object.values(artifactGroups).flat();

function sha256(path) {
  return createHash("sha256").update(readFileSync(path)).digest("hex");
}

function sourceRevision(root) {
  return execFileSync("git", ["rev-parse", "HEAD"], { cwd: root, encoding: "utf8" }).trim();
}

function sourceTimestamp(root) {
  return new Date(execFileSync("git", ["show", "-s", "--format=%cI", "HEAD"], { cwd: root, encoding: "utf8" }).trim()).toISOString();
}

export function provenanceStatement({ artifact, digest, version, revision, timestamp, materials }) {
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
        metadata: { invocationId: revision, startedOn: timestamp, finishedOn: timestamp },
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

export function generateSupplyChain({ artifactsDirectory, outputDirectory, runSyft, root = repositoryRoot }) {
  const metadata = loadReleaseMetadata();
  const revision = sourceRevision(root);
  const timestamp = sourceTimestamp(root);
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
    mkdirSync(dirname(sbomPath), { recursive: true });
    runSyft(artifactPath, sbomPath);
    const document = JSON.parse(readFileSync(sbomPath, "utf8"));
    if (!Array.isArray(document.packages) || document.packages.length === 0) {
      document.packages = [{
        name: artifact,
        SPDXID: `SPDXRef-Artifact-${sha256(artifactPath).slice(0, 16)}`,
        downloadLocation: "NOASSERTION",
        filesAnalyzed: false,
        checksums: [{ algorithm: "SHA256", checksumValue: sha256(artifactPath) }],
      }];
      writeFileSync(sbomPath, `${JSON.stringify(document, null, 2)}\n`);
    }
    validateSpdx(document, artifact);
    const statement = provenanceStatement({ artifact, digest: sha256(artifactPath), version: metadata.version, revision, timestamp, materials });
    writeFileSync(join(provenanceDirectory, `${artifact}.intoto.jsonl`), `${JSON.stringify(statement)}\n`);
  }
  return artifacts.length;
}

function parseArguments(arguments_) {
  const result = {};
  for (let index = 0; index < arguments_.length; index += 1) {
    const name = arguments_[index];
    if (!["--artifacts-dir", "--output-dir"].includes(name)) throw new Error(`unknown argument: ${name}`);
    const value = arguments_[++index];
    if (!value || value.startsWith("--")) throw new Error(`${name} requires a value`);
    result[name.slice(2)] = value;
  }
  for (const name of ["artifacts-dir", "output-dir"]) if (!result[name]) throw new Error(`--${name} is required`);
  return result;
}

export function main(arguments_ = process.argv.slice(2)) {
  const options = parseArguments(arguments_);
  const count = generateSupplyChain({
    artifactsDirectory: resolve(options["artifacts-dir"]),
    outputDirectory: resolve(options["output-dir"]),
    runSyft: (artifactPath, outputPath) => execFileSync("syft", ["scan", `file:${artifactPath}`, "--output", `spdx-json=${outputPath}`], { stdio: "inherit" }),
  });
  console.error(`[devhud.supply-chain] generated ${count} SPDX SBOM and provenance pairs`);
}

if (import.meta.url === pathToFileURL(process.argv[1]).href) {
  try { main(); } catch (error) {
    console.error(`[devhud.supply-chain] ${error.message}`);
    process.exit(1);
  }
}
