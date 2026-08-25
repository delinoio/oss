import assert from "node:assert/strict";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

import {
  artifactGroups,
  loadReleaseMetadata,
  releasePlan,
  signingInputs,
  signingStatus,
  sourceVersions,
  updaterTargets,
  validateExtensionParity,
  validateSourceVersions,
  validateStoreBuildNumber,
  validateStoreBuildSources,
  validateVersion,
} from "./devhud-release.mjs";

test("release version and tag follow the project convention", () => {
  const metadata = loadReleaseMetadata();
  validateSourceVersions(metadata.version, sourceVersions());
  const plan = releasePlan({ metadata, environment: {}, signed: false });
  assert.equal(plan.tag, `devhud@v${metadata.version}`);
  assert.equal(plan.readiness, "plan-only");
  assert.deepEqual(plan.publication, { pushesTag: false, createsRelease: false, submitsStores: false, pushesImages: false, deploys: false });
  assert.equal(plan.signingMaterial.length, signingInputs.length);
  assert.ok(plan.signingMaterial.every(({ present }) => present === false));
});

test("artifact and updater matrices are complete and package-kind specific", () => {
  assert.equal(artifactGroups.desktop.length, 12);
  assert.equal(artifactGroups.stores.length, 2);
  assert.equal(artifactGroups.extension.length, 2);
  assert.equal(artifactGroups.oci.length, 2);
  assert.equal(updaterTargets.length, 10);
  for (const target of updaterTargets) assert.ok(target.artifact.includes(target.packageKind));
  assert.equal(new Set(updaterTargets.map(({ id }) => id)).size, updaterTargets.length);
});

test("version and store build bounds fail closed", () => {
  assert.equal(validateVersion("0.1.0"), "0.1.0");
  for (const version of ["v1.0.0", "1.0", "1.0.0-rc.1", "1.0.0+build", "01.0.0", "65536.0.0"]) assert.throws(() => validateVersion(version));
  assert.equal(validateStoreBuildNumber("1"), 1);
  for (const build of ["0", "-1", "1.2", "2100000001"]) assert.throws(() => validateStoreBuildNumber(build));
  assert.doesNotThrow(() => validateStoreBuildSources(loadReleaseMetadata().storeBuildNumber));
});

test("signing status exposes names and booleans without values", () => {
  const environment = Object.fromEntries(signingInputs.map((name) => [name, `secret-${name}`]));
  const serialized = JSON.stringify(signingStatus(environment));
  assert.ok(signingInputs.every((name) => serialized.includes(name)));
  assert.ok(!serialized.includes("secret-"));
});

test("extension publication copies must be byte-equivalent", () => {
  const root = mkdtempSync(join(tmpdir(), "devhud-release-test-"));
  writeFileSync(join(root, "devhud-chrome-web-store.zip"), "same bytes");
  writeFileSync(join(root, "devhud-chrome-github-validation.zip"), "same bytes");
  validateExtensionParity(root);
  writeFileSync(join(root, "devhud-chrome-github-validation.zip"), "different bytes");
  assert.throws(() => validateExtensionParity(root), /byte-equivalent/u);
});
