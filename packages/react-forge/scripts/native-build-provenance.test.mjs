import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import {
  bindingFilename,
  readNativeBuildProvenance,
  sourceRevision,
  writeNativeBuildProvenance,
} from "./native-build-provenance.mjs";

const host = { id: "fixture-host" };
const binary = Buffer.from("fixture native binding");

async function fixture(run) {
  const directory = await mkdtemp(join(tmpdir(), "react-forge-native-build-"));
  try {
    await writeFile(join(directory, bindingFilename(host)), binary);
    await run(directory, join(directory, "release", "native-build.json"));
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
}

for (const profile of ["dev", "release"]) {
  test(`native build provenance records the copied ${profile} binding`, async () => {
    await fixture(async (directory, metadataPath) => {
      await writeNativeBuildProvenance(directory, host, profile);
      const metadata = JSON.parse(await readFile(metadataPath, "utf8"));
      assert.deepEqual(metadata, {
        schemaVersion: 1,
        bindingFile: bindingFilename(host),
        profile,
        sha256: createHash("sha256").update(binary).digest("hex"),
        sourceRevision: sourceRevision(),
      });
      assert.equal(await readNativeBuildProvenance(directory, host), profile);
    });
  });
}

test("benchmark profile follows the binding record, not the current selector", async () => {
  await fixture(async (directory) => {
    await writeNativeBuildProvenance(directory, host, "release");
    const previous = process.env.REACT_FORGE_NATIVE_PROFILE;
    try {
      process.env.REACT_FORGE_NATIVE_PROFILE = "dev";
      assert.equal(await readNativeBuildProvenance(directory, host), "release");
      delete process.env.REACT_FORGE_NATIVE_PROFILE;
      assert.equal(await readNativeBuildProvenance(directory, host), "release");
    } finally {
      if (previous === undefined) delete process.env.REACT_FORGE_NATIVE_PROFILE;
      else process.env.REACT_FORGE_NATIVE_PROFILE = previous;
    }
  });
});

test("missing or malformed provenance rejects before benchmark sampling", async () => {
  await fixture(async (directory, metadataPath) => {
    await assert.rejects(
      readNativeBuildProvenance(directory, host),
      /missing or invalid/u,
    );
    await writeNativeBuildProvenance(directory, host, "dev");
    const valid = JSON.parse(await readFile(metadataPath, "utf8"));
    for (const invalid of [
      "{",
      JSON.stringify({ ...valid, schemaVersion: 2 }),
      JSON.stringify({ ...valid, bindingFile: "other.node" }),
      JSON.stringify({ ...valid, profile: "fast" }),
      JSON.stringify({ ...valid, sha256: "not a digest" }),
      JSON.stringify({ ...valid, sourceRevision: "not a revision" }),
      JSON.stringify({ ...valid, unexpected: true }),
    ]) {
      await writeFile(metadataPath, invalid);
      await assert.rejects(
        readNativeBuildProvenance(directory, host),
        /provenance/u,
      );
    }
  });
});

test("changed or missing binding rejects instead of trusting old provenance", async () => {
  await fixture(async (directory) => {
    await writeNativeBuildProvenance(directory, host, "release");
    const bindingPath = join(directory, bindingFilename(host));
    await writeFile(bindingPath, "different bytes");
    await assert.rejects(
      readNativeBuildProvenance(directory, host),
      /digest mismatch/u,
    );
    await rm(bindingPath);
    await assert.rejects(
      readNativeBuildProvenance(directory, host),
      /binding is missing/u,
    );
  });
});

test("provenance is not written for a failed binding copy", async () => {
  const directory = await mkdtemp(join(tmpdir(), "react-forge-native-build-"));
  try {
    await assert.rejects(writeNativeBuildProvenance(directory, host, "dev"));
    await assert.rejects(
      readFile(join(directory, "release", "native-build.json")),
    );
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});
