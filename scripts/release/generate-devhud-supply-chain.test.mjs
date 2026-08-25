import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

import { artifactGroups } from "./devhud-release.mjs";
import { generateSupplyChain, validateSpdx } from "./generate-devhud-supply-chain.mjs";

test("generates an SPDX SBOM and digest-bound SLSA provenance for every artifact", () => {
  const root = mkdtempSync(join(tmpdir(), "devhud-supply-chain-"));
  const artifactsDirectory = join(root, "artifacts");
  const outputDirectory = join(root, "metadata");
  mkdirSync(artifactsDirectory);
  const artifacts = Object.values(artifactGroups).flat();
  for (const artifact of artifacts) writeFileSync(join(artifactsDirectory, artifact), `artifact:${artifact}`);
  const count = generateSupplyChain({
    artifactsDirectory,
    outputDirectory,
    runSyft: (artifactPath, outputPath) => writeFileSync(outputPath, JSON.stringify({
      spdxVersion: "SPDX-2.3",
      dataLicense: "CC0-1.0",
      SPDXID: "SPDXRef-DOCUMENT",
      packages: [{ name: artifactPath, SPDXID: "SPDXRef-Package" }],
    })),
  });
  assert.equal(count, artifacts.length);
  const artifact = artifacts[0];
  const statement = JSON.parse(readFileSync(join(outputDirectory, "provenance", `${artifact}.intoto.jsonl`), "utf8"));
  assert.equal(statement.subject[0].name, artifact);
  assert.match(statement.subject[0].digest.sha256, /^[a-f0-9]{64}$/u);
  assert.equal(statement.predicate.buildDefinition.externalParameters.publication, "disabled");
});

test("rejects a non-SPDX document", () => {
  assert.throws(() => validateSpdx({ spdxVersion: "SPDX-2.2" }, "bad.bin"), /invalid SPDX/u);
});
