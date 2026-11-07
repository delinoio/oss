// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";
import { targets, nativeName } from "../scripts/common.mjs";
import {
  packPlaceholders,
  placeholderManifests,
  verifyPlaceholders,
} from "../scripts/package.mjs";
import { publishArtifacts, trustCommands } from "../scripts/publish.mjs";

const tempDirectory = (t) => {
  const directory = mkdtempSync(path.join(tmpdir(), "binpm npm test "));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  return directory;
};

test("placeholder set names the launcher and one package per release target", () => {
  const manifests = placeholderManifests();
  assert.deepEqual(
    manifests.map(({ name }) => name),
    [
      "@delino/binpm-darwin-x64",
      "@delino/binpm-darwin-arm64",
      "@delino/binpm-win32-x64-msvc",
      "@delino/binpm-win32-arm64-msvc",
      "@delino/binpm-linux-x64-gnu",
      "@delino/binpm-linux-arm64-gnu",
      "@delino/binpm",
    ],
  );
  for (const manifest of manifests) {
    assert.equal(manifest.version, "0.0.0");
    assert.equal(manifest.license, "Apache-2.0");
    // A reservation must not run code or expose a command.
    assert.equal(manifest.bin, undefined);
    assert.equal(manifest.scripts, undefined);
    assert.equal(manifest.dependencies, undefined);
    assert.equal(manifest.optionalDependencies, undefined);
  }
  for (const target of targets) {
    const manifest = manifests.find(({ name }) => name === nativeName(target));
    assert.deepEqual(manifest.os, [target.os]);
    assert.deepEqual(manifest.cpu, [target.cpu]);
    assert.deepEqual(manifest.libc, target.libc ? [target.libc] : undefined);
  }
});

test("only the reservation version can be packaged", () => {
  assert.throws(
    () => placeholderManifests("0.1.0"),
    /reservation placeholder/u,
  );
});

test("packaging is verifiable and verification rejects tampering", (t) => {
  const directory = tempDirectory(t);
  const packed = packPlaceholders(directory);
  assert.deepEqual(verifyPlaceholders(directory), packed);
  writeFileSync(path.join(directory, "delino-binpm-0.0.0.tgz"), "tampered");
  assert.throws(() => verifyPlaceholders(directory), /Integrity mismatch/u);
});

const artifacts = [
  { name: "a", version: "0.0.0", integrity: "x" },
  { name: "b", version: "0.0.0", integrity: "y" },
];

test("publication writes missing versions in order and reuses existing ones", async () => {
  const published = new Set(["a"]);
  const order = [];
  await publishArtifacts(artifacts, {
    dryRun: false,
    lookup: async ({ name }) => (published.has(name) ? { name } : null),
    publish: async ({ name }) => {
      order.push(name);
      published.add(name);
    },
    delay: async () => {},
    report: () => {},
  });
  assert.deepEqual(order, ["b"]);
});

test("publication polls readback without repeating the upload", async () => {
  let lookups = 0;
  let uploads = 0;
  await publishArtifacts([artifacts[1]], {
    dryRun: false,
    lookup: async () => (++lookups > 3 ? { name: "b" } : null),
    publish: async () => {
      uploads += 1;
    },
    delay: async () => {},
    report: () => {},
  });
  assert.equal(uploads, 1);
});

test("publication fails when readback never confirms the upload", async () => {
  await assert.rejects(
    publishArtifacts([artifacts[0]], {
      dryRun: false,
      lookup: async () => null,
      publish: async () => {},
      delay: async () => {},
      report: () => {},
    }),
    /was not confirmed/u,
  );
});

test("dry runs never publish and print trusted-publisher commands per package", async () => {
  await publishArtifacts(artifacts, {
    publish: () => assert.fail("dry run must not publish"),
    report: () => {},
  });
  const commands = trustCommands();
  assert.equal(commands.length, 7);
  for (const command of commands)
    assert.match(
      command,
      /^npm trust github \S+ --repo delinoio\/oss --file release-binpm-npm\.yml --allow-publish$/u,
    );
});
