import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

import { artifactGroups } from "./devhud-release.mjs";
import { generateSupplyChain, validateProvenanceMetadata, validateSpdx } from "./generate-devhud-supply-chain.mjs";

const invocation = {
  invocationId: "https://github.com/delinoio/oss/actions/runs/123456789/attempts/2",
  startedOn: "2026-08-25T10:00:00Z",
  finishedOn: "2026-08-25T10:30:00Z",
};

function writeSboms(outputDirectory, packagesForArtifact = () => [{ name: "DevHUD", SPDXID: "SPDXRef-Package" }]) {
  const sbomDirectory = join(outputDirectory, "sbom");
  mkdirSync(sbomDirectory, { recursive: true });
  for (const artifact of Object.values(artifactGroups).flat()) {
    writeFileSync(join(sbomDirectory, `${artifact}.spdx.json`), JSON.stringify({
      spdxVersion: "SPDX-2.3",
      dataLicense: "CC0-1.0",
      SPDXID: "SPDXRef-DOCUMENT",
      packages: packagesForArtifact(artifact),
    }));
  }
}

test("validates prebuilt SPDX SBOMs and generates digest-bound SLSA provenance for every artifact", () => {
  const root = mkdtempSync(join(tmpdir(), "devhud-supply-chain-"));
  const artifactsDirectory = join(root, "artifacts");
  const outputDirectory = join(root, "metadata");
  mkdirSync(artifactsDirectory);
  const artifacts = Object.values(artifactGroups).flat();
  for (const artifact of artifacts) writeFileSync(join(artifactsDirectory, artifact), `artifact:${artifact}`);
  writeSboms(outputDirectory);
  const count = generateSupplyChain({
    artifactsDirectory,
    outputDirectory,
    ...invocation,
  });
  assert.equal(count, artifacts.length);
  const artifact = artifacts[0];
  const statement = JSON.parse(readFileSync(join(outputDirectory, "provenance", `${artifact}.intoto.jsonl`), "utf8"));
  assert.equal(statement.subject[0].name, artifact);
  assert.match(statement.subject[0].digest.sha256, /^[a-f0-9]{64}$/u);
  assert.equal(statement.predicate.buildDefinition.externalParameters.publication, "disabled");
  assert.deepEqual(statement.predicate.runDetails.metadata, {
    invocationId: invocation.invocationId,
    startedOn: "2026-08-25T10:00:00.000Z",
    finishedOn: "2026-08-25T10:30:00.000Z",
  });
});

test("rejects a non-SPDX document", () => {
  assert.throws(() => validateSpdx({ spdxVersion: "SPDX-2.2" }, "bad.bin"), /invalid SPDX/u);
});

test("fails the candidate when Syft produces no component inventory", () => {
  const root = mkdtempSync(join(tmpdir(), "devhud-empty-sbom-"));
  const artifactsDirectory = join(root, "artifacts");
  const outputDirectory = join(root, "metadata");
  mkdirSync(artifactsDirectory);
  const artifacts = Object.values(artifactGroups).flat();
  for (const artifact of artifacts) writeFileSync(join(artifactsDirectory, artifact), `artifact:${artifact}`);
  writeSboms(outputDirectory, (artifact) => artifact === artifacts[0] ? [] : [{ name: "DevHUD", SPDXID: "SPDXRef-Package" }]);
  assert.throws(() => generateSupplyChain({
    artifactsDirectory,
    outputDirectory,
    ...invocation,
  }), /has no packages/u);
});

test("fails when a producing job omits its artifact SBOM", () => {
  const root = mkdtempSync(join(tmpdir(), "devhud-missing-sbom-"));
  const artifactsDirectory = join(root, "artifacts");
  mkdirSync(artifactsDirectory);
  for (const artifact of Object.values(artifactGroups).flat()) writeFileSync(join(artifactsDirectory, artifact), `artifact:${artifact}`);
  assert.throws(() => generateSupplyChain({
    artifactsDirectory,
    outputDirectory: join(root, "metadata"),
    ...invocation,
  }), /ENOENT/u);
});

test("provenance metadata requires the exact run-attempt identity and ordered timestamps", () => {
  assert.deepEqual(validateProvenanceMetadata(invocation), {
    invocationId: invocation.invocationId,
    startedOn: "2026-08-25T10:00:00.000Z",
    finishedOn: "2026-08-25T10:30:00.000Z",
  });
  assert.throws(() => validateProvenanceMetadata({ ...invocation, invocationId: "b7769a5d0a" }), /run attempt/u);
  assert.throws(() => validateProvenanceMetadata({ ...invocation, finishedOn: "2026-08-25T09:59:59Z" }), /must not precede/u);
});
